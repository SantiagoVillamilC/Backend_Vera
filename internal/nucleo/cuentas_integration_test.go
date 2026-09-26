//go:build integration

package nucleo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCuentasInternasSembradas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	esperadas := map[string]struct{ naturaleza, clase string }{
		CuentaCustodiaBanco:            {"activo", "custodia"},
		CuentaLiquidacionPendienteRiel: {"pasivo", "liquidacion_pendiente"},
		CuentaGMFPorPagar:              {"pasivo", "gmf_por_pagar"},
	}

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			for id, want := range esperadas {
				var naturaleza, clase string
				if err := tx.QueryRow(ctx, "SELECT naturaleza, clase FROM cuenta WHERE id = $1", id).
					Scan(&naturaleza, &clase); err != nil {
					return struct{}{}, fmt.Errorf("cuenta interna %q: %w", id, err)
				}
				if naturaleza != want.naturaleza || clase != want.clase {
					t.Errorf("cuenta %s = (%s, %s), want (%s, %s)", id, naturaleza, clase, want.naturaleza, want.clase)
				}
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("leer las cuentas internas: %v", err)
	}
}

func TestAbrirSubcuentasDeUsuarioEsIdempotente(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	usuarioID := uuid.NewString()

	creadas, err := s.AbrirSubcuentasDeUsuario(ctx, usuarioID)
	if err != nil || creadas != 2 {
		t.Fatalf("primer intento: creadas = %d, err = %v; want 2, nil", creadas, err)
	}

	repetidas, err := s.AbrirSubcuentasDeUsuario(ctx, usuarioID)
	if err != nil {
		t.Fatalf("segundo intento: %v", err)
	}
	if repetidas != 0 {
		t.Errorf("segundo intento: creadas = %d, want 0 (idempotente)", repetidas)
	}
	t.Logf("segundo intento: creadas = %d", repetidas)

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			var filas int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM cuenta WHERE titular_id = $1", usuarioID).
				Scan(&filas); err != nil {
				return struct{}{}, err
			}
			if filas != 2 {
				t.Errorf("filas de %s = %d, want 2 sin duplicados", usuarioID, filas)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("contar subcuentas: %v", err)
	}
}

func TestCuentaConNombreDuplicadoSeRechaza(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	id := "activo:custodia:" + uuid.NewString()

	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			if _, err := tx.Exec(ctx,
				"INSERT INTO cuenta (id, naturaleza, clase) VALUES ($1, 'activo', 'custodia')", id); err != nil {
				return struct{}{}, err
			}
			_, err := tx.Exec(ctx,
				"INSERT INTO cuenta (id, naturaleza, clase) VALUES ($1, 'activo', 'custodia')", id)
			return struct{}{}, err
		})
	if err == nil {
		t.Fatal("el segundo INSERT con el mismo nombre no falló")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.UniqueViolation {
		t.Fatalf("error = %v, want SQLSTATE %s", err, pgerrcode.UniqueViolation)
	}
	if pgErr.ConstraintName != "cuenta_pkey" {
		t.Errorf("constraint = %q, want cuenta_pkey", pgErr.ConstraintName)
	}
	if !strings.Contains(pgErr.Message, "cuenta_pkey") {
		t.Errorf("mensaje = %q, want que nombre la restricción de unicidad", pgErr.Message)
	}
	t.Logf("segundo INSERT rechazado: %v", err)
}

func TestUsuarioDePruebaDeFase0TieneSusSubcuentas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	auth.SembrarParteDePrueba(t, pool)

	s := NewServicio(pool, nil, nil, nil, nil)
	if _, err := s.AbrirSubcuentasDeUsuario(ctx, "00000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatalf("abrir las subcuentas del usuario de prueba: %v", err)
	}

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			ids := []string{
				NombreCuentaDisponible("00000000-0000-4000-8000-000000000001"),
				NombreCuentaRetenido("00000000-0000-4000-8000-000000000001"),
			}
			for _, id := range ids {
				var titular string
				if err := tx.QueryRow(ctx, "SELECT titular_id FROM cuenta WHERE id = $1", id).Scan(&titular); err != nil {
					return struct{}{}, fmt.Errorf("subcuenta %q: %w", id, err)
				}
				if titular != "00000000-0000-4000-8000-000000000001" {
					t.Errorf("titular_id de %s = %q, want el usuario de prueba", id, titular)
				}
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("leer las subcuentas del usuario de prueba: %v", err)
	}
}
