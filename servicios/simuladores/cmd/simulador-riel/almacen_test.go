package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func almacenDePrueba(t *testing.T) *Almacen {
	t.Helper()

	almacen, err := AbrirAlmacen(filepath.Join(t.TempDir(), "riel.db"))
	if err != nil {
		t.Fatalf("abrir almacén: %v", err)
	}
	t.Cleanup(func() { _ = almacen.Cerrar() })
	return almacen
}

func operacionDePrueba(id string) Operacion {
	return Operacion{
		TransferenciaID: id,
		IdempotencyKey:  "k-" + id,
		Llave:           "3001234567",
		MontoCentavos:   1000000,
		Moneda:          "COP",
		Estado:          estadoConfirmada,
		OcurridoEn:      time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
	}
}

func TestRegistrarYLeerPorTransferencia(t *testing.T) {
	t.Parallel()

	almacen := almacenDePrueba(t)
	ctx := t.Context()
	id := uuid.NewString()
	guardada, reintento, err := almacen.Registrar(ctx, operacionDePrueba(id), time.Now())
	if err != nil {
		t.Fatalf("Registrar: %v", err)
	}
	if reintento {
		t.Error("el primer registro llegó marcado como reintento")
	}
	if guardada.ID == 0 {
		t.Error("el registro no devolvió id")
	}

	leida, err := almacen.PorTransferencia(ctx, id)
	if err != nil {
		t.Fatalf("PorTransferencia: %v", err)
	}
	if leida.MontoCentavos != 1000000 || leida.Estado != estadoConfirmada {
		t.Errorf("leída = %+v, quiero confirmada de 1000000", leida)
	}
}

func TestReenvioEsIdempotentePorIdentificador(t *testing.T) {
	t.Parallel()

	almacen := almacenDePrueba(t)
	ctx := t.Context()
	id := uuid.NewString()
	if _, _, err := almacen.Registrar(ctx, operacionDePrueba(id), time.Now()); err != nil {
		t.Fatalf("primer registro: %v", err)
	}
	guardada, reintento, err := almacen.Registrar(ctx, operacionDePrueba(id), time.Now())
	if err != nil {
		t.Fatalf("reenvío: %v", err)
	}
	if !reintento {
		t.Error("el reenvío no quedó marcado como reintento")
	}
	if guardada.TransferenciaID != id {
		t.Errorf("identificador = %q, quiero %q", guardada.TransferenciaID, id)
	}
	operaciones, err := almacen.Operaciones(ctx)
	if err != nil {
		t.Fatalf("Operaciones: %v", err)
	}
	if len(operaciones) != 1 {
		t.Errorf("operaciones = %d, quiero 1 sin duplicar el reenvío", len(operaciones))
	}
}

func TestReenvioConOtrosDatosConflicta(t *testing.T) {
	t.Parallel()

	almacen := almacenDePrueba(t)
	ctx := t.Context()
	id := uuid.NewString()
	if _, _, err := almacen.Registrar(ctx, operacionDePrueba(id), time.Now()); err != nil {
		t.Fatalf("primer registro: %v", err)
	}
	distinta := operacionDePrueba(id)
	distinta.MontoCentavos = 2000000
	if _, _, err := almacen.Registrar(ctx, distinta, time.Now()); !errors.Is(err, ErrOperacionEnConflicto) {
		t.Fatalf("error = %v, quiero ErrOperacionEnConflicto", err)
	}
}

func TestReenvioConOtraLlaveConflicta(t *testing.T) {
	t.Parallel()

	almacen := almacenDePrueba(t)
	ctx := t.Context()
	id := uuid.NewString()
	if _, _, err := almacen.Registrar(ctx, operacionDePrueba(id), time.Now()); err != nil {
		t.Fatalf("primer registro: %v", err)
	}
	otraLlave := operacionDePrueba(id)
	otraLlave.Llave = "3009999999"
	if _, _, err := almacen.Registrar(ctx, otraLlave, time.Now()); !errors.Is(err, ErrOperacionEnConflicto) {
		t.Fatalf("error = %v, quiero ErrOperacionEnConflicto", err)
	}
}

func TestOperacionAusenteDevuelveNoEncontrada(t *testing.T) {
	t.Parallel()

	almacen := almacenDePrueba(t)
	if _, err := almacen.PorTransferencia(t.Context(), uuid.NewString()); !errors.Is(err, ErrOperacionNoEncontrada) {
		t.Fatalf("error = %v, quiero ErrOperacionNoEncontrada", err)
	}
}
