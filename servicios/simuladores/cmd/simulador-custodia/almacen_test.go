package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// abrirAlmacenDePrueba abre un almacén sobre un archivo temporal y lo cierra al terminar.
func abrirAlmacenDePrueba(t *testing.T) (*Almacen, string) {
	t.Helper()

	ruta := filepath.Join(t.TempDir(), "movimiento.db")
	almacen, err := AbrirAlmacen(ruta)
	if err != nil {
		t.Fatalf("abrir almacén: %v", err)
	}
	t.Cleanup(func() { _ = almacen.Cerrar() })
	return almacen, ruta
}

// nuevoMovimiento arma un movimiento válido con el tipo y el monto dados.
func nuevoMovimiento(referencia, tipo string, monto Centavos, ocurrido time.Time) Movimiento {
	return Movimiento{Referencia: referencia, Tipo: tipo, MontoCentavos: monto, OcurridoEn: ocurrido}
}

// Criterio 6 y 7: el reenvío no duplica y los datos distintos son conflicto.
func TestAlmacenReenvioYConflicto(t *testing.T) {
	almacen, _ := abrirAlmacenDePrueba(t)
	ctx := t.Context()
	ocurrido := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	ahora := time.Now()

	primero, reintento, err := almacen.Registrar(ctx, nuevoMovimiento("dep-1", tipoDeposito, 250000, ocurrido), ahora)
	if err != nil || reintento {
		t.Fatalf("primer registro = (%+v, %v, %v), quiero un movimiento nuevo", primero, reintento, err)
	}

	segundo, reintento, err := almacen.Registrar(ctx, nuevoMovimiento("dep-1", tipoDeposito, 250000, ocurrido), ahora)
	if err != nil || !reintento {
		t.Fatalf("reenvío = (%+v, %v, %v), quiero reintento = true", segundo, reintento, err)
	}
	if segundo.ID != primero.ID {
		t.Errorf("id del reenvío = %d, quiero %d", segundo.ID, primero.ID)
	}

	casos := map[string]Movimiento{
		"otro monto":     nuevoMovimiento("dep-1", tipoDeposito, 999, ocurrido),
		"tipo contrario": nuevoMovimiento("dep-1", tipoRetiro, 250000, ocurrido),
	}
	for nombre, conflicto := range casos {
		if _, _, err := almacen.Registrar(ctx, conflicto, ahora); !errors.Is(err, ErrReferenciaEnConflicto) {
			t.Errorf("%s: error = %v, quiero ErrReferenciaEnConflicto", nombre, err)
		}
	}

	movimientos, err := almacen.Movimientos(ctx)
	if err != nil {
		t.Fatalf("leer movimientos: %v", err)
	}
	if len(movimientos) != 1 {
		t.Fatalf("movimientos = %d, quiero 1", len(movimientos))
	}
}

// Criterio 9.2: el saldo es depósitos menos retiros y solo cuenta lo ocurrido hasta el corte inclusive.
func TestAlmacenSaldoAcotadoAlCorte(t *testing.T) {
	almacen, _ := abrirAlmacenDePrueba(t)
	ctx := t.Context()
	ahora := time.Now()

	base := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	entradas := []Movimiento{
		nuevoMovimiento("dep-1", tipoDeposito, 1000, base),
		nuevoMovimiento("ret-1", tipoRetiro, 500, base.Add(5*time.Minute)),
		nuevoMovimiento("dep-2", tipoDeposito, 2000, base.Add(10*time.Minute)),
	}
	for _, entrada := range entradas {
		if _, _, err := almacen.Registrar(ctx, entrada, ahora); err != nil {
			t.Fatalf("registrar %s: %v", entrada.Referencia, err)
		}
	}

	tests := map[string]struct {
		corte time.Time
		want  Centavos
	}{
		"antes de todo":   {corte: base.Add(-time.Minute), want: 0},
		"corte inclusive": {corte: base, want: 1000},
		"tras el retiro":  {corte: base.Add(5 * time.Minute), want: 500},
		"tras el segundo": {corte: base.Add(10 * time.Minute), want: 2500},
	}
	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			saldo, err := almacen.SaldoHasta(ctx, tc.corte)
			if err != nil {
				t.Fatalf("SaldoHasta: %v", err)
			}
			if saldo != tc.want {
				t.Errorf("saldo = %d, quiero %d", saldo, tc.want)
			}
		})
	}
}

// Criterio 10: reabrir el archivo conserva los movimientos y el saldo.
func TestAlmacenSobreviveAReabrirElArchivo(t *testing.T) {
	almacen, ruta := abrirAlmacenDePrueba(t)
	ctx := t.Context()
	corte := time.Date(2026, 9, 17, 12, 0, 1, 0, time.UTC)

	if _, _, err := almacen.Registrar(ctx, nuevoMovimiento("dep-1", tipoDeposito, 250000,
		time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)), time.Now()); err != nil {
		t.Fatalf("registrar: %v", err)
	}
	if err := almacen.Cerrar(); err != nil {
		t.Fatalf("cerrar: %v", err)
	}

	reabierto, err := AbrirAlmacen(ruta)
	if err != nil {
		t.Fatalf("reabrir: %v", err)
	}
	defer func() { _ = reabierto.Cerrar() }()

	saldo, err := reabierto.SaldoHasta(ctx, corte)
	if err != nil {
		t.Fatalf("saldo tras reabrir: %v", err)
	}
	if saldo != 250000 {
		t.Errorf("saldo tras reabrir = %d, quiero 250000", saldo)
	}
}
