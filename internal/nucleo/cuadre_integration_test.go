//go:build integration

package nucleo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type parDeCuentas struct {
	debito  string
	credito string
}

func cuentasDePrueba(t *testing.T, ctx context.Context, tx transaccionDeNucleo) parDeCuentas {
	t.Helper()

	sufijo := uuid.NewString()
	activo := "activo:custodia:" + sufijo
	pasivo := "pasivo:usuario:" + sufijo + ":disponible"
	if _, err := tx.Exec(ctx,
		"INSERT INTO cuenta (id, naturaleza, clase, titular_id) VALUES ($1, 'activo', 'custodia', NULL), ($2, 'pasivo', 'usuario', $3)",
		activo, pasivo, sufijo); err != nil {
		t.Fatalf("crear cuentas: %v", err)
	}
	return parDeCuentas{debito: activo, credito: pasivo}
}

func asientoConMontos(t *testing.T, ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries, cuentas parDeCuentas, montos []int64) int64 {
	t.Helper()
	return asientoConMontosEn(t, ctx, tx, q, cuentas, montos, time.Now().UTC())
}

func asientoConMontosEn(t *testing.T, ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries, cuentas parDeCuentas, montos []int64, ocurridoEn time.Time) int64 {
	t.Helper()

	asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
		Tipo:        "prueba",
		OcurridoEn:  pgtype.Timestamptz{Time: ocurridoEn, Valid: true},
		Descripcion: pgtype.Text{String: "asiento de cuadre", Valid: true},
	})
	if err != nil {
		t.Fatalf("insertar asiento: %v", err)
	}
	for _, monto := range montos {
		cuenta := cuentas.debito
		if monto < 0 {
			cuenta = cuentas.credito
		}
		if _, err := q.InsertarLinea(ctx, repositorio.InsertarLineaParams{
			EntryID:  asiento.ID,
			CuentaID: cuenta,
			Monto:    monto,
			Moneda:   "COP",
		}); err != nil {
			t.Fatalf("insertar linea: %v", err)
		}
	}
	return asiento.ID
}

func TestAsientoDescuadradoSeRechazaAlConfirmar(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	var idAsiento int64

	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			idAsiento = asientoConMontos(t, ctx, tx, q, cuentasDePrueba(t, ctx, tx), []int64{1000000, -999999})
			return struct{}{}, nil
		})
	if err == nil {
		t.Fatal("el COMMIT de un asiento descuadrado no fallo")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.RaiseException {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.RaiseException)
	}
	if !strings.Contains(pgErr.Message, "descuadrado") {
		t.Errorf("mensaje = %q, want que mencione descuadrado", pgErr.Message)
	}
	t.Logf("COMMIT rechazado: %v", err)

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var asientos, lineas int
			if err := tx.QueryRow(ctx,
				"SELECT (SELECT count(*) FROM journal_entry WHERE id = $1), (SELECT count(*) FROM posting WHERE entry_id = $1)",
				idAsiento).Scan(&asientos, &lineas); err != nil {
				return struct{}{}, err
			}
			if asientos != 0 || lineas != 0 {
				t.Errorf("tras el rechazo quedaron asiento=%d lineas=%d, want 0 y 0", asientos, lineas)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("verificar que no quedo nada escrito: %v", err)
	}
}

func TestAsientoConUnaSolaLineaSeRechaza(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	var idAsiento int64

	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			idAsiento = asientoConMontos(t, ctx, tx, q, cuentasDePrueba(t, ctx, tx), []int64{1000000})
			return struct{}{}, nil
		})
	if err == nil {
		t.Fatal("el COMMIT de un asiento de una sola linea no fallo")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.RaiseException {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.RaiseException)
	}
	if !strings.Contains(pgErr.Message, "minimo 2") {
		t.Errorf("mensaje = %q, want que mencione minimo 2", pgErr.Message)
	}
	t.Logf("COMMIT rechazado: %v", err)

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var asientos, lineas int
			if err := tx.QueryRow(ctx,
				"SELECT (SELECT count(*) FROM journal_entry WHERE id = $1), (SELECT count(*) FROM posting WHERE entry_id = $1)",
				idAsiento).Scan(&asientos, &lineas); err != nil {
				return struct{}{}, err
			}
			if asientos != 0 || lineas != 0 {
				t.Errorf("tras el rechazo quedaron asiento=%d lineas=%d, want 0 y 0", asientos, lineas)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("verificar que no quedo nada escrito: %v", err)
	}
}

func TestAsientoSinLineasSeRechazaAlConfirmar(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	var idAsiento int64

	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
				Tipo:        "prueba",
				OcurridoEn:  pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				Descripcion: pgtype.Text{String: "asiento sin lineas", Valid: true},
			})
			if err != nil {
				return struct{}{}, err
			}
			idAsiento = asiento.ID
			return struct{}{}, nil
		})
	if err == nil {
		t.Fatal("el COMMIT de un asiento sin lineas no fallo")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.RaiseException {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.RaiseException)
	}
	if !strings.Contains(pgErr.Message, "minimo 2") {
		t.Errorf("mensaje = %q, want que mencione minimo 2", pgErr.Message)
	}
	t.Logf("COMMIT rechazado: %v", err)

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var asientos, lineas int
			if err := tx.QueryRow(ctx,
				"SELECT (SELECT count(*) FROM journal_entry WHERE id = $1), (SELECT count(*) FROM posting WHERE entry_id = $1)",
				idAsiento).Scan(&asientos, &lineas); err != nil {
				return struct{}{}, err
			}
			if asientos != 0 || lineas != 0 {
				t.Errorf("tras el rechazo quedaron asiento=%d lineas=%d, want 0 y 0", asientos, lineas)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("verificar que no quedo nada escrito: %v", err)
	}
}

func TestAsientoCuadradoEntraSinError(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	var idAsiento int64

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			idAsiento = asientoConMontos(t, ctx, tx, q, cuentasDePrueba(t, ctx, tx), []int64{1000000, -1000000})
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("el COMMIT de un asiento cuadrado fallo: %v", err)
	}

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var lineas int
			var suma int64
			if err := tx.QueryRow(ctx,
				"SELECT count(*), COALESCE(SUM(monto), 0)::bigint FROM posting WHERE entry_id = $1",
				idAsiento).Scan(&lineas, &suma); err != nil {
				return struct{}{}, err
			}
			if lineas != 2 || suma != 0 {
				t.Errorf("asiento persistido lineas=%d suma=%d, want 2 y 0", lineas, suma)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("leer el asiento cuadrado: %v", err)
	}
}

// Apéndice C: las dos verificaciones del invariante deben devolver cero filas.
func TestVerificacionesDelApendiceCDevuelvenCeroFilas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	consultas := map[string]string{
		"asientos descuadrados": `SELECT entry_id FROM posting GROUP BY entry_id HAVING SUM(monto) <> 0`,
		"asientos de una linea": `SELECT je.id FROM journal_entry je LEFT JOIN posting p ON p.entry_id = je.id GROUP BY je.id HAVING COUNT(p.id) < 2`,
	}
	for nombre, sentencia := range consultas {
		t.Run(nombre, func(t *testing.T) {
			if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
				func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
					rows, err := tx.Query(ctx, sentencia)
					if err != nil {
						return struct{}{}, err
					}
					defer rows.Close()
					filas := 0
					for rows.Next() {
						filas++
						var id int64
						if err := rows.Scan(&id); err != nil {
							return struct{}{}, err
						}
					}
					if err := rows.Err(); err != nil {
						return struct{}{}, err
					}
					if filas != 0 {
						t.Errorf("la consulta devolvio %d filas, want 0", filas)
					}
					return struct{}{}, nil
				}); err != nil {
				t.Fatalf("consulta %s: %v", nombre, err)
			}
		})
	}
}
