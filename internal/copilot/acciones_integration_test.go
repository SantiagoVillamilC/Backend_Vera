//go:build integration

package copilot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAccionesPendientes(t *testing.T) {
	admin := poolPersistencia(t, "DATABASE_URL")
	pool := poolPersistencia(t, "APP_DATABASE_URL")
	usuario := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	otro := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if _, err := admin.Exec(t.Context(), "INSERT INTO auth.partes(id,tipo) VALUES ($1,'persona'),($2,'persona')", usuario, otro); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DELETE FROM asistente.accion_pendiente WHERE usuario_id=$1", usuario)
		_, _ = admin.Exec(context.Background(), "DELETE FROM asistente.conversacion WHERE usuario_id=$1", usuario)
		_, _ = admin.Exec(context.Background(), "DELETE FROM auth.partes WHERE id IN ($1,$2)", usuario, otro)
	})
	conv, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.Conversacion, error) {
		return q.CrearConversacion(t.Context(), repositorio.CrearConversacionParams{UsuarioID: usuario})
	})
	if err != nil {
		t.Fatal(err)
	}
	crear := func() (repositorio.AccionPendiente, error) {
		return EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
			return q.CrearAccion(t.Context(), repositorio.CrearAccionParams{ConversacionID: conv.ID, UsuarioID: usuario, Tipo: "transferencia_interna", Destino: "+573001234567", NombreMostrado: "Laura R.", Monto: nucleo.Centavos(9007199254740993).Int64(), RequiereCodigo: true})
		})
	}
	accion, err := crear()
	if err != nil {
		t.Fatal(err)
	}
	if nucleo.Centavos(accion.Monto) != nucleo.Centavos(9007199254740993) || accion.Estado != "pendiente" {
		t.Fatalf("accion: %v", accion)
	}
	_, err = crear()
	exigirSQLState(t, err, "23505")
	_, err = EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		return q.ObtenerAccion(t.Context(), repositorio.ObtenerAccionParams{ID: accion.ID, UsuarioID: otro})
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("accion ajena: %v", err)
	}
	cancelada, err := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		return q.CancelarAccion(t.Context(), repositorio.CancelarAccionParams{ID: accion.ID, UsuarioID: usuario})
	})
	if err != nil || cancelada.Estado != "cancelada" || !cancelada.ResueltaEn.Valid {
		t.Fatalf("cancelar: %v %v", cancelada, err)
	}
	_, err = EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		return q.CancelarAccion(t.Context(), repositorio.CancelarAccionParams{ID: accion.ID, UsuarioID: usuario})
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("CAS final: %v", err)
	}
	nueva, err := crear()
	if err != nil {
		t.Fatalf("nueva despues de cancelar: %v", err)
	}
	if nueva.ExpiraEn.Time.Sub(nueva.CreadaEn.Time) != 5*time.Minute {
		t.Fatal("vigencia distinta a cinco minutos")
	}
	// Dos decisiones concurrentes no pueden resolver dos veces la misma acción.
	resultados := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() {
		_, e := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
			return q.CancelarAccion(t.Context(), repositorio.CancelarAccionParams{ID: nueva.ID, UsuarioID: usuario})
		})
		resultados <- e
	})
	wg.Go(func() {
		_, e := EnTransaccionAsistente(t.Context(), pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
			return q.FallarAccion(t.Context(), repositorio.FallarAccionParams{ID: nueva.ID, UsuarioID: usuario, CodigoError: pgtype.Text{String: "InsufficientFunds", Valid: true}})
		})
		resultados <- e
	})
	wg.Wait()
	close(resultados)
	exitos := 0
	for e := range resultados {
		if e == nil {
			exitos++
		} else if !errors.Is(e, pgx.ErrNoRows) {
			t.Fatal(e)
		}
	}
	if exitos != 1 {
		t.Fatalf("resoluciones concurrentes=%d", exitos)
	}
}

// Las filas de núcleo son únicamente referencias de prueba; no se ejecuta transferencia ni ledger.
func accionEnTransaccion(t *testing.T, expirada bool) (pgx.Tx, *repositorio.Queries, pgtype.UUID, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	p := poolPersistencia(t, "DATABASE_URL")
	tx, err := p.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	u := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	c := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	a := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	tr := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	cuenta := "prueba_accion_" + uuid.NewString()
	if _, err = tx.Exec(t.Context(), "INSERT INTO auth.partes(id,tipo) VALUES ($1,'persona')", u); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "INSERT INTO asistente.conversacion(id,usuario_id,usuario_financiero_id) VALUES ($1,$2,$2::uuid::text)", c, u); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "INSERT INTO nucleo.cuenta(id,naturaleza,clase) VALUES ($1,'pasivo','prueba')", cuenta); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "INSERT INTO nucleo.transferencia(id,usuario_id,cuenta_origen,llave_destino,monto,idempotency_key,estado) VALUES ($1,$2,$3,'destino',100,'prueba','confirmada')", tr, uuid.UUID(u.Bytes).String(), cuenta); err != nil {
		t.Fatal(err)
	}
	creada := time.Now().Add(-time.Minute)
	if expirada {
		creada = time.Now().Add(-10 * time.Minute)
	}
	if _, err = tx.Exec(t.Context(), `INSERT INTO asistente.accion_pendiente(id,usuario_id,conversacion_id,tipo,destino,nombre_mostrado,monto,requiere_codigo,creada_en,expira_en,usuario_financiero_id)
VALUES ($1,$2,$3,'transferencia_interna','+573001234567','Laura R.',100,false,$4,$4::timestamptz+interval '5 minutes',$2::uuid::text)`, a, u, c, creada); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), "SET LOCAL ROLE rol_asistente"); err != nil {
		t.Fatal(err)
	}
	return tx, repositorio.New(tx), a, u, tr
}

func TestAccionMatrizDeTransiciones(t *testing.T) {
	estados := []string{"pendiente", "confirmada", "cancelada", "vencida", "fallida"}
	for _, origen := range estados {
		for _, destino := range estados {
			t.Run(origen+" a "+destino, func(t *testing.T) {
				tx, _, id, _, transferencia := accionEnTransaccion(t, origen == "vencida")
				cambiar := func(estado string) error {
					_, err := tx.Exec(t.Context(), `UPDATE asistente.accion_pendiente SET estado=$2,
resuelta_en=CASE WHEN $2='pendiente' THEN NULL ELSE clock_timestamp() END,
transferencia_id=CASE WHEN $2='confirmada' THEN $3::uuid ELSE NULL END,
codigo_error=CASE WHEN $2='fallida' THEN 'InsufficientFunds' ELSE NULL END WHERE id=$1`, id, estado, transferencia)
					return err
				}
				if origen != "pendiente" {
					if err := cambiar(origen); err != nil {
						t.Fatal(err)
					}
				}
				err := cambiar(destino)
				permitida := origen == "pendiente" && destino != "vencida"
				if permitida {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					exigirSQLState(t, err, "23514")
				}
			})
		}
	}
}

func TestAccionExpiracionYCAS(t *testing.T) {
	t.Run("confirmacion vencida no escribe", func(t *testing.T) {
		_, q, id, u, tr := accionEnTransaccion(t, true)
		_, err := q.ConfirmarAccion(t.Context(), repositorio.ConfirmarAccionParams{ID: id, UsuarioID: u, TransferenciaID: tr})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("confirmacion expirada: %v", err)
		}
		vencida, err := q.VencerAccion(t.Context(), repositorio.VencerAccionParams{ID: id, UsuarioID: u})
		if err != nil || vencida.Estado != "vencida" {
			t.Fatalf("vencer: %v %v", vencida, err)
		}
	})
	t.Run("barrido solo vence pendientes expiradas", func(t *testing.T) {
		_, q, id, u, _ := accionEnTransaccion(t, true)
		n, err := q.VencerAcciones(t.Context(), 10)
		if err != nil || n != 1 {
			t.Fatalf("barrido: %d %v", n, err)
		}
		a, err := q.ObtenerAccion(t.Context(), repositorio.ObtenerAccionParams{ID: id, UsuarioID: u})
		if err != nil || a.Estado != "vencida" {
			t.Fatal(a, err)
		}
	})
	t.Run("confirmada se relee para idempotencia", func(t *testing.T) {
		_, q, id, u, tr := accionEnTransaccion(t, false)
		a, err := q.ConfirmarAccion(t.Context(), repositorio.ConfirmarAccionParams{ID: id, UsuarioID: u, TransferenciaID: tr})
		if err != nil || a.Estado != "confirmada" || a.TransferenciaID != tr {
			t.Fatal(a, err)
		}
		_, err = q.ConfirmarAccion(t.Context(), repositorio.ConfirmarAccionParams{ID: id, UsuarioID: u, TransferenciaID: tr})
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("confirmacion doble: %v", err)
		}
		releida, err := q.ObtenerAccion(t.Context(), repositorio.ObtenerAccionParams{ID: id, UsuarioID: u})
		if err != nil || releida.TransferenciaID != tr {
			t.Fatal(releida, err)
		}
	})
}

func TestAccionRestriccionesYPermisos(t *testing.T) {
	casos := map[string]struct{ sql, codigo string }{
		"monto inmutable":              {"UPDATE asistente.accion_pendiente SET monto=200 WHERE id=$1", "42501"},
		"destino inmutable":            {"UPDATE asistente.accion_pendiente SET destino='otro' WHERE id=$1", "42501"},
		"usuario inmutable":            {"UPDATE asistente.accion_pendiente SET usuario_id=gen_random_uuid() WHERE id=$1", "42501"},
		"vigencia inmutable":           {"UPDATE asistente.accion_pendiente SET expira_en=now()+interval '1 day' WHERE id=$1", "42501"},
		"sin borrado":                  {"DELETE FROM asistente.accion_pendiente WHERE id=$1", "42501"},
		"estado desconocido":           {"UPDATE asistente.accion_pendiente SET estado='ejecutando',resuelta_en=clock_timestamp() WHERE id=$1", "23514"},
		"fallida sin codigo":           {"UPDATE asistente.accion_pendiente SET estado='fallida',resuelta_en=clock_timestamp() WHERE id=$1", "23514"},
		"confirmada sin transferencia": {"UPDATE asistente.accion_pendiente SET estado='confirmada',resuelta_en=clock_timestamp() WHERE id=$1", "23514"},
		"resolucion sin fecha":         {"UPDATE asistente.accion_pendiente SET estado='cancelada' WHERE id=$1", "23514"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			tx, _, id, _, _ := accionEnTransaccion(t, false)
			_, err := tx.Exec(t.Context(), caso.sql, id)
			exigirSQLState(t, err, caso.codigo)
		})
	}
	t.Run("trigger protege monto aun con permisos de propietario", func(t *testing.T) {
		tx, _, id, _, _ := accionEnTransaccion(t, false)
		if _, err := tx.Exec(t.Context(), "RESET ROLE"); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(t.Context(), "UPDATE asistente.accion_pendiente SET monto=200 WHERE id=$1", id)
		exigirSQLState(t, err, "23514")
	})
	t.Run("no rebaja verificacion", func(t *testing.T) {
		tx, _, id, _, _ := accionEnTransaccion(t, false)
		if _, err := tx.Exec(t.Context(), "UPDATE asistente.accion_pendiente SET requiere_codigo=true WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		_, err := tx.Exec(t.Context(), "UPDATE asistente.accion_pendiente SET requiere_codigo=false WHERE id=$1", id)
		exigirSQLState(t, err, "23514")
	})
}

func TestAccionDatosDeCreacion(t *testing.T) {
	for _, nombre := range []string{"monto cero", "monto negativo", "destino vacio", "tipo desconocido", "llave sin tipo", "interna con tipo llave", "propietario ajeno", "llave valida"} {
		t.Run(nombre, func(t *testing.T) {
			_, q, id, u, _ := accionEnTransaccion(t, false)
			anterior, err := q.CancelarAccion(t.Context(), repositorio.CancelarAccionParams{ID: id, UsuarioID: u})
			if err != nil {
				t.Fatal(err)
			}
			p := repositorio.CrearAccionParams{ConversacionID: anterior.ConversacionID, UsuarioID: u, Tipo: "transferencia_interna", Destino: "+573001234567", NombreMostrado: "Laura R.", Monto: 100}
			switch nombre {
			case "monto cero":
				p.Monto = 0
			case "monto negativo":
				p.Monto = -1
			case "destino vacio":
				p.Destino = " "
			case "tipo desconocido":
				p.Tipo = "pago"
			case "llave sin tipo":
				p.Tipo = "transferencia_llave"
			case "interna con tipo llave":
				p.TipoLlave = pgtype.Text{String: "celular", Valid: true}
			case "propietario ajeno":
				p.UsuarioID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
			case "llave valida":
				p.Tipo = "transferencia_llave"
				p.TipoLlave = pgtype.Text{String: "celular", Valid: true}
			}
			_, err = q.CrearAccion(t.Context(), p)
			switch nombre {
			case "propietario ajeno":
				if !errors.Is(err, pgx.ErrNoRows) {
					t.Fatal(err)
				}
			case "llave valida":
				if err != nil {
					t.Fatal(err)
				}
			default:
				exigirSQLState(t, err, "23514")
			}
		})
	}
	t.Run("FK de propietario no permite conversacion ajena", func(t *testing.T) {
		tx, q, id, u, _ := accionEnTransaccion(t, false)
		a, err := q.CancelarAccion(t.Context(), repositorio.CancelarAccionParams{ID: id, UsuarioID: u})
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(t.Context(), `INSERT INTO asistente.accion_pendiente(usuario_id,conversacion_id,tipo,destino,nombre_mostrado,monto,requiere_codigo,usuario_financiero_id)
VALUES (gen_random_uuid(),$1,'transferencia_interna','destino','Laura R.',100,false,'prueba')`, a.ConversacionID)
		exigirSQLState(t, err, "23503")
	})
	t.Run("confirmar exige referencia a transferencia real", func(t *testing.T) {
		_, q, id, u, _ := accionEnTransaccion(t, false)
		_, err := q.ConfirmarAccion(t.Context(), repositorio.ConfirmarAccionParams{ID: id, UsuarioID: u, TransferenciaID: pgtype.UUID{Bytes: uuid.New(), Valid: true}})
		exigirSQLState(t, err, "23503")
	})
	t.Run("confirmar con codigo exige desafio asociado", func(t *testing.T) {
		tx, q, id, u, tr := accionEnTransaccion(t, false)
		if _, err := tx.Exec(t.Context(), "UPDATE asistente.accion_pendiente SET requiere_codigo=true WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
		_, err := q.ConfirmarAccion(t.Context(), repositorio.ConfirmarAccionParams{ID: id, UsuarioID: u, TransferenciaID: tr})
		exigirSQLState(t, err, "23514")
	})
}
