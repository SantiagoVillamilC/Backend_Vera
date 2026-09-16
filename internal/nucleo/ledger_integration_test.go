//go:build integration

package nucleo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestRepositorioDelNucleoRegistraAsientoConLineas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	sufijo := uuid.NewString()
	cuentaActivo := "activo:custodia:" + sufijo
	cuentaPasivo := "pasivo:usuario:" + sufijo + ":disponible"

	var idAsiento int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			var rol string
			if err := tx.QueryRow(ctx, "SELECT current_user").Scan(&rol); err != nil {
				return struct{}{}, err
			}
			if rol != RolNucleo {
				t.Errorf("current_user = %q, want %q", rol, RolNucleo)
			}

			if _, err := tx.Exec(ctx,
				"INSERT INTO cuenta (id, naturaleza, clase, titular_id) VALUES ($1, 'activo', 'custodia', NULL), ($2, 'pasivo', 'usuario', $3)",
				cuentaActivo, cuentaPasivo, sufijo); err != nil {
				return struct{}{}, err
			}

			asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
				Tipo:        "prueba",
				OcurridoEn:  pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				Descripcion: pgtype.Text{String: "asiento de prueba", Valid: true},
			})
			if err != nil {
				return struct{}{}, err
			}
			idAsiento = asiento.ID

			lineas := []repositorio.InsertarLineaParams{
				{EntryID: asiento.ID, CuentaID: cuentaActivo, Monto: 1000000, Moneda: "COP"},
				{EntryID: asiento.ID, CuentaID: cuentaPasivo, Monto: -1000000, Moneda: "COP"},
			}
			for _, linea := range lineas {
				if _, err := q.InsertarLinea(ctx, linea); err != nil {
					return struct{}{}, err
				}
			}

			var suma int64
			if err := tx.QueryRow(ctx,
				"SELECT COALESCE(SUM(monto), 0)::bigint FROM posting WHERE entry_id = $1", asiento.ID).
				Scan(&suma); err != nil {
				return struct{}{}, err
			}
			if suma != 0 {
				t.Errorf("suma de las líneas = %d, want 0", suma)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("registrar asiento: %v", err)
	}

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var lineas int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM posting WHERE entry_id = $1", idAsiento).Scan(&lineas); err != nil {
				return struct{}{}, err
			}
			if lineas != 2 {
				t.Errorf("líneas persistidas = %d, want 2", lineas)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("leer asiento: %v", err)
	}
}

func TestRolNucleoNoPuedeModificarNiBorrarElLedger(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	intentos := map[string]string{
		"update journal_entry": "UPDATE journal_entry SET descripcion = 'x'",
		"delete journal_entry": "DELETE FROM journal_entry",
		"update posting":       "UPDATE posting SET monto = 1",
		"delete posting":       "DELETE FROM posting",
	}
	for nombre, sentencia := range intentos {
		t.Run(nombre, func(t *testing.T) {
			_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
				func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
					_, err := tx.Exec(ctx, sentencia)
					return struct{}{}, err
				})
			if err == nil {
				t.Fatalf("%s no falló: rol_nucleo modificó el ledger", sentencia)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.InsufficientPrivilege {
				t.Fatalf("%s error = %v, want SQLSTATE %s", sentencia, err, pgerrcode.InsufficientPrivilege)
			}
			t.Logf("%s -> %v", sentencia, err)
		})
	}
}

func TestMontoDelLedgerEsBigint(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	if _, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var tipo string
			if err := tx.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
				WHERE table_schema = 'nucleo' AND table_name = 'posting' AND column_name = 'monto'`).Scan(&tipo); err != nil {
				return struct{}{}, err
			}
			if tipo != "bigint" {
				t.Errorf("posting.monto data_type = %q, want bigint", tipo)
			}

			rows, err := tx.Query(ctx, `SELECT table_name, column_name, data_type FROM information_schema.columns
				WHERE table_schema = 'nucleo' AND data_type IN ('numeric', 'double precision', 'real')`)
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var tabla, columna, tipoAjeno string
				if err := rows.Scan(&tabla, &columna, &tipoAjeno); err != nil {
					return struct{}{}, err
				}
				t.Errorf("el ledger tiene %s.%s en %s, want solo bigint", tabla, columna, tipoAjeno)
			}
			return struct{}{}, rows.Err()
		}); err != nil {
		t.Fatalf("information_schema: %v", err)
	}
}
