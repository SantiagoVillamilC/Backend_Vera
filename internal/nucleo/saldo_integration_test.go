//go:build integration

package nucleo

import (
	"context"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type lineaDeMoneda struct {
	cuenta string
	monto  int64
	moneda string
}

// asientoConMonedas registra un asiento con la moneda explícita de cada línea.
func asientoConMonedas(t *testing.T, ctx context.Context, q *repositorio.Queries, lineas []lineaDeMoneda) {
	t.Helper()

	asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
		Tipo:        "prueba",
		OcurridoEn:  pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		Descripcion: pgtype.Text{String: "asiento con monedas", Valid: true},
	})
	if err != nil {
		t.Fatalf("insertar asiento: %v", err)
	}
	for _, linea := range lineas {
		if _, err := q.InsertarLinea(ctx, repositorio.InsertarLineaParams{
			EntryID:  asiento.ID,
			CuentaID: linea.cuenta,
			Monto:    linea.monto,
			Moneda:   linea.moneda,
		}); err != nil {
			t.Fatalf("insertar linea: %v", err)
		}
	}
}

// HU-NF-04 Criterio 3: una cuenta sin movimientos suma cero y no falla.
func TestSaldoDeCuentaSinMovimientosEsCero(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)

	saldo, err := s.Saldo(ctx, NombreCuentaDisponible(uuid.NewString()))
	if err != nil {
		t.Fatalf("Saldo de una cuenta sin movimientos: %v", err)
	}
	if saldo != 0 {
		t.Errorf("monto = %d, quiero 0", saldo)
	}
}

// HU-NF-04 Criterios 4 y 5: el saldo es la suma de las líneas, ya con el signo del usuario.
func TestSaldoDeCuentaSumaSoloSusLineas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)

	var cuentas parDeCuentas
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			cuentas = cuentasDePrueba(t, ctx, tx)
			asientoConMontos(t, ctx, tx, q, cuentas, []int64{1000000, -1000000})
			asientoConMontos(t, ctx, tx, q, cuentas, []int64{500000, -500000})
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("sembrar los asientos: %v", err)
	}

	saldo, err := s.Saldo(ctx, cuentas.credito)
	if err != nil {
		t.Fatalf("Saldo: %v", err)
	}
	if saldo != Centavos(1500000) {
		t.Errorf("monto = %d, quiero 1500000 (solo las líneas de esa cuenta, con el signo del usuario)", saldo)
	}
}

// La suma del saldo se acota a la moneda de la cuenta: una línea en otra moneda no se mezcla.
func TestSaldoDeCuentaAcotaLaSumaALaMonedaDeLaCuenta(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)

	var cuentas parDeCuentas
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			cuentas = cuentasDePrueba(t, ctx, tx)
			asientoConMonedas(t, ctx, q, []lineaDeMoneda{
				{cuenta: cuentas.credito, monto: -1000000, moneda: "COP"},
				{cuenta: cuentas.credito, monto: -400000, moneda: "USD"},
				{cuenta: cuentas.debito, monto: 1400000, moneda: "COP"},
			})
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("sembrar el asiento de dos monedas: %v", err)
	}

	saldo, err := s.Saldo(ctx, cuentas.credito)
	if err != nil {
		t.Fatalf("Saldo: %v", err)
	}
	if saldo != Centavos(1000000) {
		t.Errorf("monto = %d, quiero 1000000 (solo la línea COP de la cuenta, sin la de USD)", saldo)
	}
}
