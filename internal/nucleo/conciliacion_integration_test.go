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
	"github.com/jackc/pgx/v5/pgxpool"
)

var ventanaPrueba = time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)

func afirmarViolacionUnica(t *testing.T, err error, indice string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want *pgconn.PgError", err)
	}
	if pgErr.Code != pgerrcode.UniqueViolation {
		t.Fatalf("SQLSTATE = %s, want %s", pgErr.Code, pgerrcode.UniqueViolation)
	}
	if pgErr.ConstraintName != indice {
		t.Fatalf("constraint = %q, want %q", pgErr.ConstraintName, indice)
	}
	t.Logf("violación única literal: SQLSTATE %s, constraint %s", pgErr.Code, pgErr.ConstraintName)
}

func insertarDescuadreDePrueba(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tipo string, transferenciaID pgtype.UUID, referencia pgtype.Text) (repositorio.Descuadre, error) {
	t.Helper()
	return enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Descuadre, error) {
			return q.InsertarDescuadre(ctx, repositorio.InsertarDescuadreParams{
				Tipo:              tipo,
				TransferenciaID:   transferenciaID,
				ReferenciaExterna: referencia,
				MontoInterno:      pgtype.Int8{Int64: 100000, Valid: true},
				MontoExterno:      pgtype.Int8{Int64: 99999, Valid: true},
				VentanaDesde:      pgtype.Timestamptz{Time: ventanaPrueba, Valid: true},
				VentanaHasta:      pgtype.Timestamptz{Time: ventanaPrueba.Add(time.Hour), Valid: true},
			})
		})
}

func TestDescuadreSinResolverPorTransferenciaNoSeDuplica(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	transferencia := pgtype.UUID{Bytes: uuid.New(), Valid: true}

	primero, err := insertarDescuadreDePrueba(t, ctx, pool, "monto_distinto", transferencia, pgtype.Text{})
	if err != nil {
		t.Fatalf("primer descuadre: %v", err)
	}
	if _, err := insertarDescuadreDePrueba(t, ctx, pool, "monto_distinto", transferencia, pgtype.Text{}); err == nil {
		t.Fatal("el segundo descuadre sin resolver del mismo objeto no falló")
	} else {
		afirmarViolacionUnica(t, err, "descuadre_identidad_unico")
	}

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (int64, error) {
			return q.ResolverDescuadre(ctx, repositorio.ResolverDescuadreParams{
				ID:         primero.ID,
				Resolucion: pgtype.Text{String: "mando el extracto", Valid: true},
			})
		}); err != nil {
		t.Fatalf("resolver el descuadre: %v", err)
	}

	segundo, err := insertarDescuadreDePrueba(t, ctx, pool, "monto_distinto", transferencia, pgtype.Text{})
	if err != nil {
		t.Fatalf("una diferencia ya resuelta debe admitir una nueva del mismo objeto: %v", err)
	}
	if segundo.ID == primero.ID {
		t.Errorf("el segundo descuadre reutilizó el id %d", primero.ID)
	}
}

func TestDescuadreSinResolverPorReferenciaExternaNoSeDuplica(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	referencia := pgtype.Text{String: "ext-" + uuid.NewString(), Valid: true}

	if _, err := insertarDescuadreDePrueba(t, ctx, pool, "extracto_sin_libro", pgtype.UUID{}, referencia); err != nil {
		t.Fatalf("primer descuadre: %v", err)
	}
	if _, err := insertarDescuadreDePrueba(t, ctx, pool, "extracto_sin_libro", pgtype.UUID{}, referencia); err == nil {
		t.Fatal("el segundo descuadre sin resolver de la misma referencia no falló")
	} else {
		afirmarViolacionUnica(t, err, "descuadre_identidad_unico")
	}
}

func TestDescuadreExigeUnaIdentidad(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	_, err := insertarDescuadreDePrueba(t, ctx, pool, "monto_distinto", pgtype.UUID{}, pgtype.Text{})
	if err == nil {
		t.Fatal("un descuadre sin transferencia ni referencia externa entró")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.CheckViolation {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.CheckViolation)
	}
	if pgErr.ConstraintName != "descuadre_identidad_check" {
		t.Fatalf("constraint = %q, want %q", pgErr.ConstraintName, "descuadre_identidad_check")
	}
}

func TestIndicesDeLaConciliacionExisten(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	esperados := []string{
		"journal_entry_ocurrido_en",
		"journal_entry_transferencia_id",
		"posting_por_asiento",
		"transferencia_activas_por_actualizada",
		"descuadre_identidad_unico",
	}

	var encontrados []string
	if err := pool.QueryRow(ctx,
		`SELECT array_agg(indexname) FROM pg_indexes WHERE schemaname = 'nucleo' AND indexname = ANY($1)`,
		esperados).Scan(&encontrados); err != nil {
		t.Fatalf("leer pg_indexes: %v", err)
	}
	if len(encontrados) != len(esperados) {
		t.Fatalf("índices encontrados = %v, want los %d del diseño", encontrados, len(esperados))
	}
}

func TestCursorDeConciliacionGuardaElFinalDeLaVentana(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	primera := ventanaPrueba
	segunda := ventanaPrueba.Add(time.Minute)

	for _, hasta := range []time.Time{primera, segunda} {
		if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
			func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.ConciliacionCorrida, error) {
				return q.InsertarCorridaConciliacion(ctx, pgtype.Timestamptz{Time: hasta, Valid: true})
			}); err != nil {
			t.Fatalf("insertar corrida: %v", err)
		}
	}

	var ultima pgtype.Timestamptz
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			leida, err := q.UltimaVentanaConciliada(ctx)
			ultima = leida
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("leer el cursor: %v", err)
	}
	if !ultima.Time.Equal(segunda) {
		t.Errorf("última ventana = %v, want %v", ultima.Time, segunda)
	}
}

func sembrarCuentasDeConciliacion(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	sufijo := uuid.NewString()
	activo := "activo:custodia:" + sufijo
	pasivo := "pasivo:usuario:" + sufijo + ":disponible"
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				"INSERT INTO cuenta (id, naturaleza, clase, titular_id) VALUES ($1, 'activo', 'custodia', NULL), ($2, 'pasivo', 'usuario', $3)",
				activo, pasivo, sufijo)
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("sembrar cuentas: %v", err)
	}
	return activo, pasivo
}

func sembrarAsientoDeConciliacion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, debito, credito string, ocurridoEn time.Time) int64 {
	t.Helper()
	id, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (int64, error) {
			asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
				Tipo:       "prueba_conciliacion",
				OcurridoEn: pgtype.Timestamptz{Time: ocurridoEn, Valid: true},
			})
			if err != nil {
				return 0, err
			}
			if _, err := q.InsertarLinea(ctx, repositorio.InsertarLineaParams{EntryID: asiento.ID, CuentaID: debito, Monto: 100, Moneda: "COP"}); err != nil {
				return 0, err
			}
			if _, err := q.InsertarLinea(ctx, repositorio.InsertarLineaParams{EntryID: asiento.ID, CuentaID: credito, Monto: -100, Moneda: "COP"}); err != nil {
				return 0, err
			}
			return asiento.ID, nil
		})
	if err != nil {
		t.Fatalf("sembrar asiento: %v", err)
	}
	return id
}

func TestLeerAsientosDeVentanaRespetaElBordeHasta(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	debito, credito := sembrarCuentasDeConciliacion(t, ctx, pool)

	base := time.Date(1999, 6, 1, 0, 0, 0, 0, time.UTC)
	dentro := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, base)
	fuera := sembrarAsientoDeConciliacion(t, ctx, pool, debito, credito, base.Add(time.Hour))

	filas := leerVentanaDeAsientos(t, ctx, pool, base, base.Add(time.Hour))
	ids := map[int64]bool{}
	for _, fila := range filas {
		ids[fila.ID] = true
	}
	if !ids[dentro] {
		t.Errorf("el asiento en el borde desde (%d) no apareció en la ventana", dentro)
	}
	if ids[fuera] {
		t.Errorf("el asiento en el borde hasta (%d) apareció: el borde debe ser exclusivo", fuera)
	}
}

func leerVentanaDeAsientos(t *testing.T, ctx context.Context, pool *pgxpool.Pool, desde, hasta time.Time) []repositorio.JournalEntry {
	t.Helper()
	var filas []repositorio.JournalEntry
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			leidas, err := q.LeerAsientosDeVentana(ctx, repositorio.LeerAsientosDeVentanaParams{
				Desde: pgtype.Timestamptz{Time: desde, Valid: true},
				Hasta: pgtype.Timestamptz{Time: hasta, Valid: true},
			})
			filas = leidas
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("leer la ventana: %v", err)
	}
	return filas
}

func TestLeerTransferenciasActivasDeVentanaSoloDevuelveLasActivas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	debito, _ := sembrarCuentasDeConciliacion(t, ctx, pool)
	base := time.Date(1999, 7, 1, 0, 0, 0, 0, time.UTC)

	activa := sembrarTransferenciaDeConciliacion(t, ctx, pool, debito, "despachada", base)
	confirmada := sembrarTransferenciaDeConciliacion(t, ctx, pool, debito, "confirmada", base.Add(time.Minute))
	fuera := sembrarTransferenciaDeConciliacion(t, ctx, pool, debito, "despachada", base.Add(time.Hour))

	filas := leerTransferenciasActivas(t, ctx, pool, base, base.Add(time.Hour))
	encontradas := map[[16]byte]bool{}
	for _, fila := range filas {
		encontradas[fila.ID.Bytes] = true
	}
	if !encontradas[activa.Bytes] {
		t.Errorf("la transferencia despachada en la ventana (%s) no apareció", activa)
	}
	if encontradas[confirmada.Bytes] {
		t.Errorf("la transferencia confirmada (%s) apareció: no es estado activo", confirmada)
	}
	if encontradas[fuera.Bytes] {
		t.Errorf("la transferencia fuera de la ventana (%s) apareció", fuera)
	}
}

func leerTransferenciasActivas(t *testing.T, ctx context.Context, pool *pgxpool.Pool, desde, hasta time.Time) []repositorio.LeerTransferenciasActivasDeVentanaRow {
	t.Helper()
	var filas []repositorio.LeerTransferenciasActivasDeVentanaRow
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			leidas, err := q.LeerTransferenciasActivasDeVentana(ctx, repositorio.LeerTransferenciasActivasDeVentanaParams{
				Desde: pgtype.Timestamptz{Time: desde, Valid: true},
				Hasta: pgtype.Timestamptz{Time: hasta, Valid: true},
			})
			filas = leidas
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("leer transferencias activas: %v", err)
	}
	return filas
}

func sembrarTransferenciaDeConciliacion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cuentaOrigen, estado string, actualizadaEn time.Time) pgtype.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				`INSERT INTO transferencia (id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, actualizada_en)
				 VALUES ($1, $2, $3, 'llave-prueba', 1000, 'COP', $4, $5, $6)`,
				id, uuid.NewString(), cuentaOrigen, uuid.NewString(), estado, actualizadaEn)
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("sembrar transferencia: %v", err)
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
