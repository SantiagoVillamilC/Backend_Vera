package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPaginaHistorial rechaza límites y cursores inválidos o de otra lista.
var ErrPaginaHistorial = errors.New("asistente: página de historial inválida")

// ConsultaHistorial limita la lectura; un cursor ausente solicita la primera página.
type ConsultaHistorial struct {
	Limite int
	Cursor *string
}

// PaginaHistorial contiene sólo proyecciones públicas y una continuación opaca.
type PaginaHistorial[T any] struct {
	Elementos []T
	Limite    int
	Siguiente *string
}

// ConversacionHistorial no contiene identidad, protocolo ni reservas del orquestador.
type ConversacionHistorial struct {
	ID                        uuid.UUID
	Titulo                    string
	CreadaEn, UltimaActividad time.Time
}

// MensajeHistorial sólo representa mensajes visibles y el estado actual de su tarjeta.
type MensajeHistorial struct {
	ID                uuid.UUID
	Rol, Canal, Texto string
	CreadoEn          time.Time
	Accion            *TarjetaTexto
}

// ServicioHistorial aísla cada historial por actor y contexto financiero efectivo.
type ServicioHistorial struct {
	pool *pgxpool.Pool
	dias int
}

// NuevoServicioHistorial exige una retención positiva, expresada en días de 24 horas.
func NuevoServicioHistorial(pool *pgxpool.Pool, dias int) (*ServicioHistorial, error) {
	if pool == nil || dias < 1 || dias > 36500 {
		return nil, ErrSolicitudIA
	}
	return &ServicioHistorial{pool: pool, dias: dias}, nil
}

type cursorHistorial struct {
	Version  int       `json:"v"`
	Tipo     string    `json:"t"`
	Posicion int64     `json:"p"`
	ID       uuid.UUID `json:"id"`
	Ambito   string    `json:"a"`
}

func codificarCursorHistorial(c cursorHistorial) string {
	datos, _ := json.Marshal(c) // Sólo escalares; no hay valores que fallen al serializar.
	return base64.RawURLEncoding.EncodeToString(datos)
}

func leerCursorHistorial(token, tipo, ambito string) (cursorHistorial, error) {
	var c cursorHistorial
	if len(token) == 0 || len(token) > 512 {
		return c, ErrPaginaHistorial
	}
	datos, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		return c, ErrPaginaHistorial
	}
	if err := json.Unmarshal(datos, &c); err != nil || c.Version != 1 || c.Tipo != tipo || c.Ambito != ambito || c.Posicion <= 0 || c.ID == uuid.Nil {
		return cursorHistorial{}, ErrPaginaHistorial
	}
	if tipo == "mensajes" && c.Posicion > math.MaxInt32 {
		return cursorHistorial{}, ErrPaginaHistorial
	}
	// PostgreSQL y el contrato HTTP usan fechas del calendario civil (años 1 a 9999).
	if tipo == "conversaciones" && c.Posicion > 253402300799999999 {
		return cursorHistorial{}, ErrPaginaHistorial
	}
	return c, nil
}

func parametrosHistorial(ctx context.Context, p ConsultaHistorial, tipo string, conv uuid.UUID) (pgtype.UUID, string, cursorHistorial, error) {
	parte, err := parteDelAsistente(ctx)
	id, ok := identidad.DeContexto(ctx)
	if err != nil || !ok || id.UsuarioID == "" {
		return pgtype.UUID{}, "", cursorHistorial{}, ErrConsentimientoAsistente
	}
	if p.Limite < 1 || p.Limite > 100 {
		return pgtype.UUID{}, "", cursorHistorial{}, ErrPaginaHistorial
	}
	huella := sha256.Sum256([]byte(parte.String() + "\x00" + id.UsuarioID + "\x00" + conv.String()))
	c := cursorHistorial{Version: 1, Tipo: tipo, Ambito: hex.EncodeToString(huella[:])}
	if p.Cursor != nil {
		c, err = leerCursorHistorial(*p.Cursor, tipo, c.Ambito)
		if err != nil {
			return pgtype.UUID{}, "", cursorHistorial{}, err
		}
	}
	return uuidCiclo(parte), id.UsuarioID, c, nil
}

// ListarConversaciones ordena por última actividad, con UUID como desempate estable.
// El cursor conserva el ancla aunque se borre la conversación que la originó.
func (s *ServicioHistorial) ListarConversaciones(ctx context.Context, p ConsultaHistorial) (PaginaHistorial[ConversacionHistorial], error) {
	n := p.Limite
	if n < 1 || n > 100 {
		return PaginaHistorial[ConversacionHistorial]{}, ErrPaginaHistorial
	}
	limite := int32(n) + 1
	parte, financiero, c, err := parametrosHistorial(ctx, p, "conversaciones", uuid.Nil)
	if err != nil {
		return PaginaHistorial[ConversacionHistorial]{}, err
	}
	return EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (PaginaHistorial[ConversacionHistorial], error) {
		filas, err := q.ListarHistorialConversaciones(ctx, repositorio.ListarHistorialConversacionesParams{UsuarioID: parte, UsuarioFinancieroID: financiero, ConCursor: p.Cursor != nil, Actividad: pgtype.Timestamptz{Time: time.UnixMicro(c.Posicion), Valid: true}, Ancla: uuidCiclo(c.ID), Limite: limite})
		if err != nil {
			return PaginaHistorial[ConversacionHistorial]{}, err
		}
		pagina := PaginaHistorial[ConversacionHistorial]{Elementos: make([]ConversacionHistorial, 0, min(len(filas), p.Limite)), Limite: p.Limite}
		mas := len(filas) > p.Limite
		if mas {
			filas = filas[:p.Limite]
		}
		for _, f := range filas {
			pagina.Elementos = append(pagina.Elementos, ConversacionHistorial{ID: uuid.UUID(f.ID.Bytes), Titulo: f.Titulo.String, CreadaEn: f.CreadaEn.Time, UltimaActividad: f.UltimoMensajeEn.Time})
		}
		if mas {
			ultima := filas[len(filas)-1]
			c.Posicion, c.ID = ultima.UltimoMensajeEn.Time.UnixMicro(), uuid.UUID(ultima.ID.Bytes)
			siguiente := codificarCursorHistorial(c)
			pagina.Siguiente = &siguiente
		}
		return pagina, nil
	})
}

// ListarMensajes omite herramientas y turnos internos, sin exponer metadatos del modelo.
func (s *ServicioHistorial) ListarMensajes(ctx context.Context, conv uuid.UUID, p ConsultaHistorial) (PaginaHistorial[MensajeHistorial], error) {
	n := p.Limite
	if n < 1 || n > 100 {
		return PaginaHistorial[MensajeHistorial]{}, ErrPaginaHistorial
	}
	limite := int32(n) + 1
	parte, financiero, c, err := parametrosHistorial(ctx, p, "mensajes", conv)
	if err != nil {
		return PaginaHistorial[MensajeHistorial]{}, err
	}
	posicion := c.Posicion
	if posicion < 0 || posicion > math.MaxInt32 {
		return PaginaHistorial[MensajeHistorial]{}, ErrPaginaHistorial
	}
	ancla := int32(posicion)
	return EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (PaginaHistorial[MensajeHistorial], error) {
		_, err := q.ConversacionDeHistorial(ctx, repositorio.ConversacionDeHistorialParams{ID: uuidCiclo(conv), UsuarioID: parte, UsuarioFinancieroID: financiero})
		if errors.Is(err, pgx.ErrNoRows) {
			return PaginaHistorial[MensajeHistorial]{}, ErrConversacionTextoNoEncontrada
		}
		if err != nil {
			return PaginaHistorial[MensajeHistorial]{}, err
		}
		filas, err := q.ListarHistorialMensajes(ctx, repositorio.ListarHistorialMensajesParams{ConversacionID: uuidCiclo(conv), ConCursor: p.Cursor != nil, Ancla: ancla, Limite: limite})
		if err != nil {
			return PaginaHistorial[MensajeHistorial]{}, err
		}
		pagina := PaginaHistorial[MensajeHistorial]{Elementos: make([]MensajeHistorial, 0, min(len(filas), p.Limite)), Limite: p.Limite}
		mas := len(filas) > p.Limite
		if mas {
			filas = filas[:p.Limite]
		}
		for _, f := range filas {
			rol := "usuario"
			if f.Rol == "asistente" {
				rol = "vera"
			}
			m := MensajeHistorial{ID: uuid.UUID(f.ID.Bytes), Rol: rol, Canal: f.Canal, Texto: f.Contenido, CreadoEn: f.CreadaEn.Time}
			if f.AccionID.Valid {
				a, err := q.ObtenerAccion(ctx, repositorio.ObtenerAccionParams{ID: f.AccionID, UsuarioID: parte})
				if err != nil {
					return PaginaHistorial[MensajeHistorial]{}, err
				}
				if a.UsuarioFinancieroID != financiero {
					return PaginaHistorial[MensajeHistorial]{}, ErrConversacionTextoNoEncontrada
				}
				m.Accion = tarjetaDeHistorial(a)
			}
			pagina.Elementos = append(pagina.Elementos, m)
		}
		if mas {
			c.Posicion, c.ID = int64(filas[len(filas)-1].NumeroOrden), conv
			siguiente := codificarCursorHistorial(c)
			pagina.Siguiente = &siguiente
		}
		return pagina, nil
	})
}

func tarjetaDeHistorial(a repositorio.AccionPendiente) *TarjetaTexto {
	t := tarjetaAccion(a)
	if t.Estado == "pendiente" && !t.ExpiraEn.After(time.Now()) {
		t.Estado = "vencida"
	}
	return &t
}

// BorrarConversacion cancela y borra físicamente en una transacción, sin borrar el ledger.
func (s *ServicioHistorial) BorrarConversacion(ctx context.Context, conv uuid.UUID) error {
	parte, financiero, _, err := parametrosHistorial(ctx, ConsultaHistorial{Limite: 1}, "conversaciones", uuid.Nil)
	if err != nil {
		return err
	}
	_, err = EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (struct{}, error) {
		n, err := q.BorrarConversacionDeHistorial(ctx, repositorio.BorrarConversacionDeHistorialParams{ID: uuidCiclo(conv), UsuarioID: parte, UsuarioFinancieroID: financiero})
		if err == nil && n == 0 {
			err = ErrConversacionTextoNoEncontrada
		}
		return struct{}{}, err
	})
	return err
}

// BorrarConversaciones elimina sólo el historial del contexto activo del actor autenticado.
func (s *ServicioHistorial) BorrarConversaciones(ctx context.Context) error {
	parte, financiero, _, err := parametrosHistorial(ctx, ConsultaHistorial{Limite: 1}, "conversaciones", uuid.Nil)
	if err != nil {
		return err
	}
	_, err = EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (struct{}, error) {
		ids, err := q.BloquearHistorialParaBorrar(ctx, repositorio.BloquearHistorialParaBorrarParams{UsuarioID: parte, UsuarioFinancieroID: financiero})
		if err != nil {
			return struct{}{}, err
		}
		for _, id := range ids {
			if _, err := q.BorrarConversacionDeHistorial(ctx, repositorio.BorrarConversacionDeHistorialParams{ID: id, UsuarioID: parte, UsuarioFinancieroID: financiero}); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

// Retener procesa hasta mil conversaciones inactivas; cada lote se confirma por separado.
// SKIP LOCKED permite reintentos y varios workers sin competir con operaciones en curso.
func (s *ServicioHistorial) Retener(ctx context.Context) (int, error) {
	corte := pgtype.Timestamptz{Time: time.Now().Add(-time.Duration(s.dias) * 24 * time.Hour), Valid: true}
	total := 0
	for range 10 {
		n, err := EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (int, error) {
			ids, err := q.ConversacionesParaRetencion(ctx, corte)
			if err != nil {
				return 0, err
			}
			for _, id := range ids {
				if err := q.BorrarConversacionPorRetencion(ctx, id); err != nil {
					return 0, err
				}
			}
			return len(ids), nil
		})
		if err != nil {
			return total, err
		}
		total += n
		if n < 100 {
			break
		}
	}
	return total, nil
}
