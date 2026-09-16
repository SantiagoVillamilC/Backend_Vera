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

func depositoDePrueba(usuarioID, referencia, clave string) repositorio.InsertarDepositoParams {
	return repositorio.InsertarDepositoParams{
		ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
		UsuarioID:      usuarioID,
		Monto:          1000000,
		Moneda:         "COP",
		Referencia:     referencia,
		IdempotencyKey: clave,
	}
}

func asientoDeDeposito(depositoID pgtype.UUID) repositorio.InsertarAsientoParams {
	return repositorio.InsertarAsientoParams{
		Tipo:       "fondeo",
		DepositoID: depositoID,
		OcurridoEn: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
}

func enTransaccionDeDeposito(t *testing.T, pool *pgxpool.Pool, fn func(context.Context, *repositorio.Queries) error) error {
	t.Helper()

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, fn(ctx, q)
		})
	return err
}

func requerirUnicidad(t *testing.T, err error, restriccion string) {
	t.Helper()

	if err == nil {
		t.Fatal("la segunda inserción no falló")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.UniqueViolation {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.UniqueViolation)
	}
	if pgErr.ConstraintName != restriccion {
		t.Errorf("constraint = %q, want %q", pgErr.ConstraintName, restriccion)
	}
	t.Logf("segunda inserción rechazada: %v", err)
}

func TestDepositoConReferenciaDuplicadaSeRechaza(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	referencia := "dep-" + uuid.NewString()

	err := enTransaccionDeDeposito(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		if _, err := q.InsertarDeposito(ctx, depositoDePrueba(uuid.NewString(), referencia, "k1")); err != nil {
			return err
		}
		_, err := q.InsertarDeposito(ctx, depositoDePrueba(uuid.NewString(), referencia, "k2"))
		return err
	})
	requerirUnicidad(t, err, "deposito_referencia_unica")
}

func TestClaveDeIdempotenciaUnicaPorUsuario(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	usuario := uuid.NewString()
	clave := "k-" + uuid.NewString()

	err := enTransaccionDeDeposito(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		if _, err := q.InsertarDeposito(ctx, depositoDePrueba(usuario, uuid.NewString(), clave)); err != nil {
			return err
		}
		_, err := q.InsertarDeposito(ctx, depositoDePrueba(usuario, uuid.NewString(), clave))
		return err
	})
	requerirUnicidad(t, err, "deposito_idempotencia_unica")

	if err := enTransaccionDeDeposito(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		for range 2 {
			if _, err := q.InsertarDeposito(ctx, depositoDePrueba(uuid.NewString(), uuid.NewString(), clave)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("dos usuarios distintos con la misma clave: %v", err)
	}
}

func TestUnSoloAsientoPorDeposito(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	err := enTransaccionDeDeposito(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		dep, err := q.InsertarDeposito(ctx, depositoDePrueba(uuid.NewString(), uuid.NewString(), "k"))
		if err != nil {
			return err
		}
		if _, err := q.InsertarAsiento(ctx, asientoDeDeposito(dep.ID)); err != nil {
			return err
		}
		_, err = q.InsertarAsiento(ctx, asientoDeDeposito(dep.ID))
		return err
	})
	requerirUnicidad(t, err, "journal_entry_deposito_unico")

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			cuentas := cuentasDePrueba(t, ctx, tx)
			for range 2 {
				asientoConMontos(t, ctx, tx, q, cuentas, []int64{1000000, -1000000})
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("dos asientos sin deposito_id: %v", err)
	}
}

func TestAsientoConDosOrigenesSeRechaza(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	err := enTransaccionDeDeposito(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		dep, err := q.InsertarDeposito(ctx, depositoDePrueba(uuid.NewString(), uuid.NewString(), "k"))
		if err != nil {
			return err
		}
		asiento := asientoDeDeposito(dep.ID)
		asiento.TransferenciaID = pgtype.UUID{Bytes: uuid.New(), Valid: true}
		_, err = q.InsertarAsiento(ctx, asiento)
		return err
	})
	if err == nil {
		t.Fatal("el asiento con transferencia_id y deposito_id a la vez no falló")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.CheckViolation {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.CheckViolation)
	}
	if pgErr.ConstraintName != "journal_entry_origen_unico" {
		t.Errorf("constraint = %q, want %q", pgErr.ConstraintName, "journal_entry_origen_unico")
	}
	t.Logf("asiento con dos orígenes rechazado: %v", err)
}

func TestAsientoConUnOrigenONingunoEntra(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	var conDeposito, sinOrigen int64

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			cuentas := cuentasDePrueba(t, ctx, tx)

			dep, err := q.InsertarDeposito(ctx, depositoDePrueba(uuid.NewString(), uuid.NewString(), "k"))
			if err != nil {
				return struct{}{}, err
			}
			asiento, err := q.InsertarAsiento(ctx, asientoDeDeposito(dep.ID))
			if err != nil {
				return struct{}{}, err
			}
			for _, monto := range []int64{1000000, -1000000} {
				cuenta := cuentas.debito
				if monto < 0 {
					cuenta = cuentas.credito
				}
				if _, err := q.InsertarLinea(ctx, repositorio.InsertarLineaParams{
					EntryID: asiento.ID, CuentaID: cuenta, Monto: monto, Moneda: "COP",
				}); err != nil {
					return struct{}{}, err
				}
			}
			conDeposito = asiento.ID
			sinOrigen = asientoConMontos(t, ctx, tx, q, cuentas, []int64{1000000, -1000000})
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("asiento con un origen o sin ninguno: %v", err)
	}
	t.Logf("asiento con deposito_id = %d y asiento sin origen = %d entraron", conDeposito, sinOrigen)
}

func TestAsientoConDepositoInexistenteSeRechaza(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	err := enTransaccionDeDeposito(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		_, err := q.InsertarAsiento(ctx, asientoDeDeposito(pgtype.UUID{Bytes: uuid.New(), Valid: true}))
		return err
	})
	if err == nil {
		t.Fatal("un asiento con deposito_id inexistente no falló")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.ForeignKeyViolation {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.ForeignKeyViolation)
	}
	t.Logf("asiento huérfano rechazado: %v", err)
}

func TestCompareAndSetDelEstadoDelDeposito(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	clave := "k-" + uuid.NewString()

	err := enTransaccionDeDeposito(t, pool, func(ctx context.Context, q *repositorio.Queries) error {
		dep, err := q.InsertarDeposito(ctx, depositoDePrueba(uuid.NewString(), uuid.NewString(), clave))
		if err != nil {
			return err
		}
		if dep.Estado != "pendiente" {
			t.Errorf("estado inicial = %q, want pendiente", dep.Estado)
		}

		filas, err := q.ConfirmarDeposito(ctx, dep.ID)
		if err != nil || filas != 1 {
			t.Fatalf("primer CAS: filas = %d, err = %v; want 1, nil", filas, err)
		}
		t.Logf("ConfirmarDeposito sobre pendiente: filas = %d", filas)

		repeticion, err := q.ConfirmarDeposito(ctx, dep.ID)
		if err != nil {
			return err
		}
		if repeticion != 0 {
			t.Errorf("segundo CAS: filas = %d, want 0", repeticion)
		}
		t.Logf("segundo ConfirmarDeposito: filas = %d", repeticion)

		leido, err := q.ObtenerDeposito(ctx, dep.ID)
		if err != nil {
			return err
		}
		if leido.Estado != "confirmada" {
			t.Errorf("estado tras el CAS = %q, want confirmada", leido.Estado)
		}
		t.Logf("estado tras confirmar: %q", leido.Estado)

		rechazo, err := q.RechazarDeposito(ctx, dep.ID)
		if err != nil {
			return err
		}
		if rechazo != 0 {
			t.Errorf("rechazar una confirmada: filas = %d, want 0", rechazo)
		}
		t.Logf("RechazarDeposito sobre confirmada: filas = %d", rechazo)

		expiracion, err := q.ExpiarDeposito(ctx, dep.ID)
		if err != nil {
			return err
		}
		if expiracion != 0 {
			t.Errorf("expiar una confirmada: filas = %d, want 0", expiracion)
		}
		t.Logf("ExpiarDeposito sobre confirmada: filas = %d", expiracion)

		trasIlegales, err := q.ObtenerDeposito(ctx, dep.ID)
		if err != nil {
			return err
		}
		if trasIlegales.Estado != "confirmada" {
			t.Errorf("estado tras las transiciones ilegales = %q, want confirmada", trasIlegales.Estado)
		}
		t.Logf("estado tras las transiciones ilegales: %q", trasIlegales.Estado)
		return nil
	})
	if err != nil {
		t.Fatalf("compare-and-set: %v", err)
	}
}

func TestRolNucleoNoPuedeReescribirElMontoDelDeposito(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE deposito SET monto = 1")
			return struct{}{}, err
		})
	if err == nil {
		t.Fatal("rol_nucleo pudo reescribir deposito.monto")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.InsufficientPrivilege {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.InsufficientPrivilege)
	}
	t.Logf("UPDATE monto rechazado: %v", err)
}

func TestMontoDeDepositoEsBigint(t *testing.T) {
	pool := abrirPoolDePrueba(t)

	if _, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var tipo string
			if err := tx.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
				WHERE table_schema = 'nucleo' AND table_name = 'deposito' AND column_name = 'monto'`).Scan(&tipo); err != nil {
				return struct{}{}, err
			}
			if tipo != "bigint" {
				t.Errorf("deposito.monto data_type = %q, want bigint", tipo)
			}

			var flotantes int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
				WHERE table_schema = 'nucleo' AND table_name = 'deposito'
				  AND data_type IN ('numeric', 'double precision', 'real')`).Scan(&flotantes); err != nil {
				return struct{}{}, err
			}
			if flotantes != 0 {
				t.Errorf("deposito tiene %d columnas numeric o flotantes, want 0", flotantes)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("information_schema: %v", err)
	}
}
