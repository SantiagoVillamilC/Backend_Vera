//go:build integration

package copilot

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func poolPersistencia(t *testing.T, variable string) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(variable)
	if dsn == "" {
		t.Fatalf("%s obligatoria: se requiere PostgreSQL real", variable)
	}
	p, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestPersistenciaAislamientoOrdenYRollback(t *testing.T) {
	admin := poolPersistencia(t, "DATABASE_URL")
	pool := poolPersistencia(t, "APP_DATABASE_URL")
	usuario := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	otro := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := admin.Exec(t.Context(), "INSERT INTO auth.partes(id,tipo) VALUES ($1,'persona'),($2,'persona')", usuario, otro); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DELETE FROM asistente.conversacion WHERE usuario_id=$1", usuario)
		_, _ = admin.Exec(context.Background(), "DELETE FROM auth.partes WHERE id IN ($1,$2)", usuario, otro)
	})
	conv, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Conversacion, error) {
		return q.CrearConversacion(t.Context(), repositorio.CrearConversacionParams{UsuarioID: usuario, Titulo: pgtype.Text{String: "Prueba", Valid: true}})
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Conversacion, error) {
		return q.ObtenerConversacion(t.Context(), repositorio.ObtenerConversacionParams{ID: conv.ID, UsuarioID: otro})
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("conversacion ajena: %v", err)
	}
	for i := 1; i <= 25; i++ {
		msg, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Mensaje, error) {
			return q.AnexarMensaje(t.Context(), repositorio.AnexarMensajeParams{ConversacionID: conv.ID, UsuarioID: usuario, Rol: "usuario", Canal: "texto", Contenido: "hola", Metadatos: []byte("{}")})
		})
		if err != nil {
			t.Fatal(err)
		}
		if msg.NumeroOrden != int32(i) {
			t.Fatalf("orden=%d esperado=%d", msg.NumeroOrden, i)
		}
	}
	contexto, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) ([]repositorio.Mensaje, error) {
		return q.ContextoConversacion(t.Context(), repositorio.ContextoConversacionParams{ConversacionID: conv.ID, UsuarioID: usuario})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(contexto) != 20 || contexto[0].NumeroOrden != 6 || contexto[19].NumeroOrden != 25 {
		t.Fatalf("contexto incorrecto: %v", contexto)
	}
	abortar := errors.New("abortar turno")
	_, err = EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (struct{}, error) {
		_, e := q.AnexarMensaje(t.Context(), repositorio.AnexarMensajeParams{ConversacionID: conv.ID, UsuarioID: usuario, Rol: "herramienta", Canal: "texto", Contenido: "resultado", Metadatos: []byte("{}")})
		if e != nil {
			return struct{}{}, e
		}
		return struct{}{}, abortar
	})
	if !errors.Is(err, abortar) {
		t.Fatal(err)
	}
	actual, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Conversacion, error) {
		return q.ObtenerConversacion(t.Context(), repositorio.ObtenerConversacionParams{ID: conv.ID, UsuarioID: usuario})
	})
	if err != nil || actual.CantidadMensajes != 25 {
		t.Fatalf("rollback: %v %v", actual, err)
	}

	t.Run("lectura y escritura ajenas", func(t *testing.T) {
		_, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Mensaje, error) {
			return q.AnexarMensaje(t.Context(), repositorio.AnexarMensajeParams{ConversacionID: conv.ID, UsuarioID: otro, Rol: "usuario", Canal: "texto", Contenido: "intruso", Metadatos: []byte("{}")})
		})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("escritura ajena: %v", err)
		}
		rows, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) ([]repositorio.Mensaje, error) {
			return q.ContextoConversacion(t.Context(), repositorio.ContextoConversacionParams{ConversacionID: conv.ID, UsuarioID: otro})
		})
		if err != nil || len(rows) != 0 {
			t.Fatalf("lectura ajena: %v %v", rows, err)
		}
	})

	t.Run("restricciones y contador atomico", func(t *testing.T) {
		casos := map[string]func(*repositorio.AnexarMensajeParams){
			"rol":       func(p *repositorio.AnexarMensajeParams) { p.Rol = "system" },
			"canal":     func(p *repositorio.AnexarMensajeParams) { p.Canal = "audio" },
			"contenido": func(p *repositorio.AnexarMensajeParams) { p.Contenido = "   " },
			"tokens":    func(p *repositorio.AnexarMensajeParams) { p.TokensEntrada = pgtype.Int4{Int32: -1, Valid: true} },
			"metadatos": func(p *repositorio.AnexarMensajeParams) { p.Metadatos = []byte("[]") },
		}
		for nombre, alterar := range casos {
			t.Run(nombre, func(t *testing.T) {
				p := repositorio.AnexarMensajeParams{ConversacionID: conv.ID, UsuarioID: usuario, Rol: "usuario", Canal: "texto", Contenido: "hola", Metadatos: []byte("{}")}
				alterar(&p)
				_, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Mensaje, error) { return q.AnexarMensaje(t.Context(), p) })
				exigirSQLState(t, err, "23514")
			})
		}
	})

	t.Run("funciones y respuesta sin editar mensajes", func(t *testing.T) {
		_, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Mensaje, error) {
			return q.AnexarMensaje(t.Context(), repositorio.AnexarMensajeParams{ConversacionID: conv.ID, UsuarioID: usuario, Rol: "herramienta", Canal: "texto", Contenido: "resultado", Metadatos: []byte(`{"llamada_id":"llamada-1"}`)})
		})
		if err != nil {
			t.Fatal(err)
		}
		respuesta, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Mensaje, error) {
			return q.AnexarMensaje(t.Context(), repositorio.AnexarMensajeParams{ConversacionID: conv.ID, UsuarioID: usuario, Rol: "asistente", Canal: "texto", Contenido: "respuesta", Metadatos: []byte("{}"), RespuestaA: contexto[19].ID})
		})
		if err != nil || respuesta.NumeroOrden != 27 {
			t.Fatalf("respuesta: %v %v", respuesta, err)
		}
		pendientes, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) ([]repositorio.Mensaje, error) {
			return q.MensajesSinRespuesta(t.Context(), repositorio.MensajesSinRespuestaParams{ConversacionID: conv.ID, UsuarioID: usuario})
		})
		if err != nil || len(pendientes) != 24 {
			t.Fatalf("sin respuesta: %d %v", len(pendientes), err)
		}
		rows, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) ([]repositorio.Mensaje, error) {
			return q.ContextoConversacion(t.Context(), repositorio.ContextoConversacionParams{ConversacionID: conv.ID, UsuarioID: usuario})
		})
		if err != nil || len(rows) != 22 || rows[20].Rol != "herramienta" {
			t.Fatalf("contexto interno: %v %v", rows, err)
		}
	})

	t.Run("privilegios minimos y restauracion del rol", func(t *testing.T) {
		var rol string
		if err := pool.QueryRow(t.Context(), "SELECT current_user").Scan(&rol); err != nil || rol != "rol_api" {
			t.Fatalf("rol base: %s %v", rol, err)
		}
		_, err := pool.Exec(t.Context(), "SELECT id FROM asistente.conversacion")
		exigirSQLState(t, err, "42501")
		for _, sentencia := range []string{
			"UPDATE asistente.mensaje SET contenido='alterado'",
			"DELETE FROM asistente.mensaje",
			"TRUNCATE asistente.mensaje",
			"SELECT id FROM auth.partes",
			"SELECT id FROM nucleo.cuenta",
			"UPDATE asistente.conversacion SET usuario_id=gen_random_uuid()",
		} {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(t.Context(), "SET LOCAL ROLE rol_asistente"); err != nil {
				_ = tx.Rollback(t.Context())
				t.Fatal(err)
			}
			_, err = tx.Exec(t.Context(), sentencia)
			_ = tx.Rollback(t.Context())
			exigirSQLState(t, err, "42501")
		}
	})

	t.Run("anexos concurrentes sin orden duplicado", func(t *testing.T) {
		const cantidad = 12
		resultados := make(chan error, cantidad)
		var wg sync.WaitGroup
		for range cantidad {
			wg.Go(func() {
				_, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Mensaje, error) {
					return q.AnexarMensaje(t.Context(), repositorio.AnexarMensajeParams{ConversacionID: conv.ID, UsuarioID: usuario, Rol: "usuario", Canal: "voz", Contenido: "transcripcion", Metadatos: []byte("{}")})
				})
				resultados <- err
			})
		}
		wg.Wait()
		close(resultados)
		for err := range resultados {
			if err != nil {
				t.Fatal(err)
			}
		}
		var total, distintos, maximo int
		if err := admin.QueryRow(t.Context(), "SELECT count(*),count(DISTINCT numero_orden),max(numero_orden) FROM asistente.mensaje WHERE conversacion_id=$1", conv.ID).Scan(&total, &distintos, &maximo); err != nil {
			t.Fatal(err)
		}
		if total != 39 || distintos != total || maximo != total {
			t.Fatalf("orden concurrente: %d %d %d", total, distintos, maximo)
		}
	})
}

func exigirSQLState(t *testing.T, err error, codigo string) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != codigo {
		t.Fatalf("SQLSTATE esperado %s: %v", codigo, err)
	}
}
