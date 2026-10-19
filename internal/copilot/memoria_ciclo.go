package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrCicloNoDisponible cubre una referencia ajena, resuelta, archivada o en procesamiento.
var ErrCicloNoDisponible = errors.New("asistente: el turno no está disponible para procesar")

// MemoriaCicloPostgres usa transacciones cortas y un token de exclusión con vencimiento.
type MemoriaCicloPostgres struct{ pool *pgxpool.Pool }

// NuevaMemoriaCicloPostgres construye el adaptador sin acceder a la red.
func NuevaMemoriaCicloPostgres(pool *pgxpool.Pool) *MemoriaCicloPostgres {
	return &MemoriaCicloPostgres{pool: pool}
}

type sesionPostgres struct {
	pool         *pgxpool.Pool
	entrada      EntradaOrquestador
	parte, token pgtype.UUID
	canal        string
	historial    []TurnoIA
}

func uuidCiclo(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// Abrir toma el turno sólo si sigue siendo el último mensaje propio sin respuesta.
func (m *MemoriaCicloPostgres) Abrir(ctx context.Context, e EntradaOrquestador) (SesionCiclo, error) {
	parte, err := parteDelAsistente(ctx)
	if err != nil {
		return nil, err
	}
	if m == nil || m.pool == nil {
		return nil, ErrNoDisponibleIA
	}
	fin := time.Now().Add(45 * time.Second)
	if plazo, ok := ctx.Deadline(); ok && plazo.Before(fin) {
		fin = plazo
	}
	s := &sesionPostgres{pool: m.pool, entrada: e, parte: uuidCiclo(parte), token: uuidCiclo(uuid.New())}
	_, err = EnTransaccionAsistente(ctx, m.pool, func(q *repositorio.Queries) (struct{}, error) {
		_, err := q.AdquirirCiclo(ctx, repositorio.AdquirirCicloParams{Token: s.token, ExpiraEn: pgtype.Timestamptz{Time: fin, Valid: true}, ConversacionID: uuidCiclo(e.ConversacionID), UsuarioID: s.parte, MensajeID: uuidCiclo(e.MensajeID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return struct{}{}, ErrCicloNoDisponible
		}
		if err != nil {
			return struct{}{}, err
		}
		mensaje, err := q.MensajeDeCiclo(ctx, repositorio.MensajeDeCicloParams{MensajeID: uuidCiclo(e.MensajeID), ConversacionID: uuidCiclo(e.ConversacionID), UsuarioID: s.parte})
		if err != nil {
			return struct{}{}, err
		}
		s.canal = mensaje.Canal
		filas, err := q.ContextoConversacion(ctx, repositorio.ContextoConversacionParams{ConversacionID: uuidCiclo(e.ConversacionID), UsuarioID: s.parte})
		if err != nil {
			return struct{}{}, err
		}
		for _, f := range filas {
			var meta protocoloPersistido
			if err := json.Unmarshal(f.Metadatos, &meta); err != nil {
				return struct{}{}, ErrRespuestaIA
			}
			if meta.Protocolo != nil {
				if meta.Protocolo.Rol != f.Rol {
					return struct{}{}, ErrRespuestaIA
				}
				s.historial = append(s.historial, *meta.Protocolo)
			} else {
				// Las filas previas al orquestador sólo admiten texto público, no herramientas huérfanas.
				if f.Rol == "herramienta" || meta.Interno {
					return struct{}{}, ErrRespuestaIA
				}
				s.historial = append(s.historial, TurnoIA{Rol: f.Rol, Partes: []ParteIA{{Texto: f.Contenido}}})
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

type protocoloPersistido struct {
	Interno   bool     `json:"interno"`
	Protocolo *TurnoIA `json:"protocolo,omitempty"`
}

func (s *sesionPostgres) Historial() []TurnoIA { return s.historial }

func (s *sesionPostgres) validar(ctx context.Context, q *repositorio.Queries) error {
	_, err := q.ValidarCiclo(ctx, repositorio.ValidarCicloParams{ConversacionID: uuidCiclo(s.entrada.ConversacionID), UsuarioID: s.parte, Token: s.token})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCicloNoDisponible
	}
	return err
}

func (s *sesionPostgres) Guardar(ctx context.Context, r RegistroCiclo) error {
	_, err := EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (struct{}, error) {
		if err := s.validar(ctx, q); err != nil {
			return struct{}{}, err
		}
		if err := s.anexar(ctx, q, r.Respuesta.Turno, !r.Final, r, r.Final); err != nil {
			return struct{}{}, err
		}
		if len(r.Resultados) > 0 {
			turno := TurnoIA{Rol: "herramienta", Partes: make([]ParteIA, len(r.Resultados))}
			for i := range r.Resultados {
				turno.Partes[i] = ParteIA{Resultado: &r.Resultados[i]}
			}
			// El consumo se registra en el turno del modelo, no se duplica en herramientas.
			herramientas := r
			herramientas.Respuesta.TokensEntrada = 0
			herramientas.Respuesta.TokensSalida = 0
			herramientas.Latencia = 0
			if err := s.anexar(ctx, q, turno, true, herramientas, false); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	})
	return err
}

func (s *sesionPostgres) anexar(ctx context.Context, q *repositorio.Queries, t TurnoIA, interno bool, r RegistroCiclo, final bool) error {
	contenido := "Turno interno de funciones."
	if !interno {
		textos := make([]string, 0, len(t.Partes))
		for _, p := range t.Partes {
			if p.Texto != "" {
				textos = append(textos, p.Texto)
			}
		}
		contenido = strings.TrimSpace(strings.Join(textos, "\n"))
	}
	for _, p := range t.Partes {
		if p.Audio != nil {
			return ErrRespuestaIA
		}
	} // nunca persistir audio
	meta, err := json.Marshal(protocoloPersistido{Interno: interno, Protocolo: &t})
	if err != nil {
		return err
	}
	p := repositorio.AnexarMensajeParams{ConversacionID: uuidCiclo(s.entrada.ConversacionID), UsuarioID: s.parte, Rol: t.Rol, Canal: s.canal, Contenido: contenido, Metadatos: meta, NombreModelo: pgtype.Text{String: r.Modelo, Valid: true}, VersionInstrucciones: pgtype.Text{String: r.VersionInstrucciones, Valid: true}, TokensEntrada: pgtype.Int4{Int32: r.Respuesta.TokensEntrada, Valid: true}, TokensSalida: pgtype.Int4{Int32: r.Respuesta.TokensSalida, Valid: true}, LatenciaMs: pgtype.Int4{Int32: int32(min(max(r.Latencia.Milliseconds(), 0), int64(2147483647))), Valid: true}}
	if final {
		p.RespuestaA = uuidCiclo(s.entrada.MensajeID)
	}
	_, err = q.AnexarMensaje(ctx, p)
	return err
}

func (s *sesionPostgres) Accion(ctx context.Context, id uuid.UUID) (*AccionOrquestador, error) {
	a, err := EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		if err := s.validar(ctx, q); err != nil {
			return repositorio.AccionPendiente{}, err
		}
		return q.ObtenerAccion(ctx, repositorio.ObtenerAccionParams{ID: uuidCiclo(id), UsuarioID: s.parte})
	})
	if err != nil {
		return nil, err
	}
	if uuid.UUID(a.ConversacionID.Bytes) != s.entrada.ConversacionID || a.Estado != "pendiente" || !a.ExpiraEn.Time.After(time.Now()) {
		return nil, ErrCicloNoDisponible
	}
	return &AccionOrquestador{ID: uuid.UUID(a.ID.Bytes), Tipo: a.Tipo, NombreMostrado: a.NombreMostrado, CelularEnmascarado: a.CelularEnmascarado.String, EntidadDestino: a.EntidadDestino.String, Monto: nucleo.Centavos(a.Monto), RequiereCodigo: a.RequiereCodigo, ExpiraEn: a.ExpiraEn.Time}, nil
}

func (s *sesionPostgres) Cerrar(ctx context.Context) error {
	_, err := EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (struct{}, error) {
		return struct{}{}, q.LiberarCiclo(ctx, repositorio.LiberarCicloParams{ConversacionID: uuidCiclo(s.entrada.ConversacionID), UsuarioID: s.parte, Token: s.token})
	})
	return err
}

var _ MemoriaCiclo = (*MemoriaCicloPostgres)(nil)
