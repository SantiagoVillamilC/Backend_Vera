package copilot

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Errores estables de admisión; no revelan existencia de conversaciones ajenas.
var (
	ErrConversacionTextoNoEncontrada = errors.New("asistente: conversación no encontrada")
	ErrTextoEnProceso                = errors.New("asistente: conversación en procesamiento")
	ErrClaveTextoEnConflicto         = errors.New("asistente: clave de reintento reutilizada con otra solicitud")
)

// ProcesadorTexto es el ciclo ya autorizado; no acepta historial desde el cliente.
type ProcesadorTexto interface {
	Responder(context.Context, EntradaOrquestador) (SalidaOrquestador, error)
}

// TarjetaTexto es una proyección del registro persistido, sin destino ni origen internos.
type TarjetaTexto struct {
	ID                                                       uuid.UUID
	Estado, Tipo, Nombre, Celular, Entidad, TipoLlave, Llave string
	Monto                                                    nucleo.Centavos
	RequiereCodigo                                           bool
	ExpiraEn                                                 time.Time
}

// RespuestaTexto mantiene IDs de entrada también ante un fallo después de persistirla.
type RespuestaTexto struct {
	ConversacionID, MensajeUsuarioID, MensajeID uuid.UUID
	Texto                                       string
	Transcripcion                               string
	CreadoEn                                    time.Time
	Accion                                      *TarjetaTexto
}

// ServicioTexto admite entradas y recupera respuestas sin mantener transacciones durante la IA.
type ServicioTexto struct {
	pool       *pgxpool.Pool
	procesador ProcesadorTexto
}

// NuevoServicioTexto conecta persistencia y orquestación del servidor.
func NuevoServicioTexto(pool *pgxpool.Pool, p ProcesadorTexto) *ServicioTexto {
	return &ServicioTexto{pool, p}
}

type admisionTexto struct {
	entrada  EntradaOrquestador
	completa bool
}

// EnviarTexto reutiliza mensajes sin respuesta y claves HTTP, conservando una sola ejecución activa.
func (s *ServicioTexto) EnviarTexto(ctx context.Context, conversacion uuid.UUID, texto string, clave uuid.UUID) (RespuestaTexto, error) {
	id, _ := identidad.DeContexto(ctx)
	huella := sha256.Sum256([]byte(id.UsuarioID + "\x00" + conversacion.String() + "\x00" + strings.TrimSpace(texto)))
	return s.enviarMensaje(ctx, conversacion, texto, clave, CanalTexto, huella, [32]byte{})
}

func (s *ServicioTexto) enviarMensaje(ctx context.Context, conversacion uuid.UUID, texto string, clave uuid.UUID, canal CanalAsistente, huella, huellaAudio [32]byte) (RespuestaTexto, error) {
	var salida RespuestaTexto
	parte, err := parteDelAsistente(ctx)
	id, ok := identidad.DeContexto(ctx)
	if err != nil || !ok || id.UsuarioID == "" {
		return salida, ErrConsentimientoAsistente
	}
	texto = strings.TrimSpace(texto)
	maximo := 1000
	if canal == CanalVoz {
		maximo = 6000
	}
	if !utf8.ValidString(texto) || texto == "" || utf8.RuneCountInString(texto) > maximo || strings.ContainsRune(texto, 0) {
		return salida, ErrSolicitudIA
	}
	if s == nil || s.pool == nil || s.procesador == nil {
		return salida, ErrNoDisponibleIA
	}
	a, err := EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (admisionTexto, error) {
		if err := q.BloquearEntradaTexto(ctx, parte.String()); err != nil {
			return admisionTexto{}, err
		}
		convID, mensajeID := conversacion, uuid.Nil
		if clave != uuid.Nil {
			r, err := q.SolicitudTextoPorClave(ctx, repositorio.SolicitudTextoPorClaveParams{UsuarioID: uuidCiclo(parte), Clave: uuidCiclo(clave)})
			if err == nil {
				if subtle.ConstantTimeCompare(r.Huella, huella[:]) != 1 {
					return admisionTexto{}, ErrClaveTextoEnConflicto
				}
				convID = uuid.UUID(r.ConversacionID.Bytes)
				mensajeID = uuid.UUID(r.MensajeID.Bytes)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return admisionTexto{}, err
			}
		}
		var conv repositorio.Conversacion
		if convID == uuid.Nil {
			conv, err = q.CrearConversacion(ctx, repositorio.CrearConversacionParams{UsuarioID: uuidCiclo(parte), UsuarioFinancieroID: pgtype.Text{String: id.UsuarioID, Valid: true}})
		} else {
			conv, err = q.ConversacionParaTexto(ctx, repositorio.ConversacionParaTextoParams{ID: uuidCiclo(convID), UsuarioID: uuidCiclo(parte)})
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return admisionTexto{}, ErrConversacionTextoNoEncontrada
		}
		if err != nil {
			return admisionTexto{}, err
		}
		if conv.UsuarioFinancieroID != id.UsuarioID {
			return admisionTexto{}, ErrConversacionTextoNoEncontrada
		}
		convID = uuid.UUID(conv.ID.Bytes)
		if mensajeID != uuid.Nil {
			_, err := q.RespuestaTextoPersistida(ctx, repositorio.RespuestaTextoPersistidaParams{ConversacionID: conv.ID, UsuarioID: uuidCiclo(parte), MensajeID: uuidCiclo(mensajeID)})
			if err == nil {
				return admisionTexto{entrada: EntradaOrquestador{ConversacionID: convID, MensajeID: mensajeID}, completa: true}, nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return admisionTexto{}, err
			}
		}
		if conv.CicloExpiraEn.Valid && conv.CicloExpiraEn.Time.After(time.Now()) {
			return admisionTexto{}, ErrTextoEnProceso
		}
		if mensajeID == uuid.Nil {
			previo, err := q.UltimoUsuarioTexto(ctx, conv.ID)
			coincide := previo.Canal == string(canal) && previo.Contenido == texto
			if canal == CanalVoz {
				coincide = previo.Canal == "voz" && coincideHuellaAudio(previo.Metadatos, huellaAudio)
			}
			if err == nil && !previo.Respondido && coincide {
				mensajeID = uuid.UUID(previo.ID.Bytes)
			} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return admisionTexto{}, err
			}
			if mensajeID == uuid.Nil {
				meta := []byte(`{}`)
				if canal == CanalVoz {
					meta, _ = json.Marshal(map[string]string{"huella_audio": hex.EncodeToString(huellaAudio[:])})
				}
				m, err := q.AnexarMensaje(ctx, repositorio.AnexarMensajeParams{ConversacionID: conv.ID, UsuarioID: uuidCiclo(parte), Rol: "usuario", Canal: string(canal), Contenido: texto, Metadatos: meta})
				if err != nil {
					return admisionTexto{}, err
				}
				mensajeID = uuid.UUID(m.ID.Bytes)
			}
			if clave != uuid.Nil {
				if err := q.RegistrarSolicitudTexto(ctx, repositorio.RegistrarSolicitudTextoParams{UsuarioID: uuidCiclo(parte), Clave: uuidCiclo(clave), ConversacionID: conv.ID, MensajeID: uuidCiclo(mensajeID), Huella: huella[:]}); err != nil {
					return admisionTexto{}, err
				}
			}
		}
		token := uuid.New()
		fin := time.Now().Add(45 * time.Second)
		if plazo, ok := ctx.Deadline(); ok && plazo.Before(fin) {
			fin = plazo
		}
		n, err := q.ReservarEntradaTexto(ctx, repositorio.ReservarEntradaTextoParams{ConversacionID: conv.ID, UsuarioID: uuidCiclo(parte), Token: uuidCiclo(token), ExpiraEn: pgtype.Timestamptz{Time: fin, Valid: true}})
		if err != nil {
			return admisionTexto{}, err
		}
		if n != 1 {
			return admisionTexto{}, ErrTextoEnProceso
		}
		return admisionTexto{entrada: EntradaOrquestador{ConversacionID: convID, MensajeID: mensajeID, ReservaToken: token}}, nil
	})
	if err != nil {
		return salida, errorEntradaTexto(ctx, err)
	}
	salida.ConversacionID = a.entrada.ConversacionID
	salida.MensajeUsuarioID = a.entrada.MensajeID
	if !a.completa {
		defer func() {
			cierre, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_, _ = EnTransaccionAsistente(cierre, s.pool, func(q *repositorio.Queries) (struct{}, error) {
				return struct{}{}, q.LiberarCiclo(cierre, repositorio.LiberarCicloParams{ConversacionID: uuidCiclo(a.entrada.ConversacionID), UsuarioID: uuidCiclo(parte), Token: uuidCiclo(a.entrada.ReservaToken)})
			})
		}()
		if _, err := s.procesador.Responder(ctx, a.entrada); err != nil {
			return salida, err
		}
	}
	r, err := EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (RespuestaTexto, error) {
		m, err := q.RespuestaTextoPersistida(ctx, repositorio.RespuestaTextoPersistidaParams{ConversacionID: uuidCiclo(salida.ConversacionID), UsuarioID: uuidCiclo(parte), MensajeID: uuidCiclo(salida.MensajeUsuarioID)})
		if err != nil {
			return RespuestaTexto{}, err
		}
		r := salida
		r.MensajeID = uuid.UUID(m.ID.Bytes)
		r.Texto = m.Contenido
		r.CreadoEn = m.CreadaEn.Time
		if canal == CanalVoz {
			usuario, err := q.MensajeDeCiclo(ctx, repositorio.MensajeDeCicloParams{MensajeID: uuidCiclo(salida.MensajeUsuarioID), ConversacionID: m.ConversacionID, UsuarioID: uuidCiclo(parte)})
			if err != nil {
				return RespuestaTexto{}, err
			}
			r.Transcripcion = usuario.Contenido
		}
		if m.AccionID.Valid {
			a, err := q.ObtenerAccion(ctx, repositorio.ObtenerAccionParams{ID: m.AccionID, UsuarioID: uuidCiclo(parte)})
			if err != nil {
				return RespuestaTexto{}, err
			}
			if a.UsuarioFinancieroID != id.UsuarioID {
				return RespuestaTexto{}, ErrConversacionTextoNoEncontrada
			}
			estado := a.Estado
			if estado == "pendiente" && !a.ExpiraEn.Time.After(time.Now()) {
				estado = "vencida"
			}
			r.Accion = &TarjetaTexto{ID: uuid.UUID(a.ID.Bytes), Estado: estado, Tipo: a.Tipo, Nombre: a.NombreMostrado, Celular: a.CelularEnmascarado.String, Entidad: a.EntidadDestino.String, Monto: nucleo.Centavos(a.Monto), RequiereCodigo: a.RequiereCodigo, ExpiraEn: a.ExpiraEn.Time, TipoLlave: a.TipoLlave.String}
			if a.Tipo == "transferencia_llave" {
				r.Accion.Llave = "***"
			}
		}
		return r, nil
	})
	if err != nil {
		return salida, errorEntradaTexto(ctx, err)
	}
	return r, nil
}

func errorEntradaTexto(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	for _, permitido := range []error{ErrConversacionTextoNoEncontrada, ErrTextoEnProceso, ErrClaveTextoEnConflicto} {
		if errors.Is(err, permitido) {
			return permitido
		}
	}
	return ErrNoDisponibleIA
}
