//go:build integration

package nucleo

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestAvisoTrasAsientoDisparaDespuesDelCommit comprueba que el callback ve la liquidación ya confirmada.
func TestAvisoTrasAsientoDisparaDespuesDelCommit(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	transferenciaID, _ := sembrarReservaConOutbox(t, ctx, pool, usuario, monto)

	recibidos := map[string]int{}
	estadoEnElCallback := ""
	asientosEnElCallback := 0
	aviso := func(_ context.Context, usuarioID string, _ time.Time) {
		recibidos[usuarioID]++
		if usuarioID != usuario {
			return
		}
		estadoEnElCallback = leerEstadoTransferencia(t, ctx, pool, transferenciaID)
		asientosEnElCallback = contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada)
	}
	servicio := NewServicio(pool, nil, &rielDoble{}, nil, nil, ConAvisoTrasAsiento(aviso))
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}

	if recibidos[usuario] != 1 {
		t.Fatalf("avisos del titular = %d, quiero 1", recibidos[usuario])
	}
	if estadoEnElCallback != EstadoTransferenciaConfirmada {
		t.Errorf("estado en el callback = %q, quiero %q (el aviso debe correr después del commit)", estadoEnElCallback, EstadoTransferenciaConfirmada)
	}
	if asientosEnElCallback != 1 {
		t.Errorf("asientos 4a en el callback = %d, quiero 1", asientosEnElCallback)
	}
}

// TestAvisoTrasAsientoNoDisparaEnCompensacion verifica que un rechazo no emite el aviso de liquidación.
func TestAvisoTrasAsientoNoDisparaEnCompensacion(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	_, _ = sembrarReservaConOutbox(t, ctx, pool, usuario, Centavos(1000000))

	recibidos := map[string]int{}
	aviso := func(_ context.Context, usuarioID string, _ time.Time) {
		recibidos[usuarioID]++
	}
	riel := &rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoRechazado, Motivo: "rechazo de prueba"}}
	servicio := NewServicio(pool, nil, riel, nil, nil, ConAvisoTrasAsiento(aviso))
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}

	if recibidos[usuario] != 0 {
		t.Fatalf("avisos del titular compensado = %d, quiero 0", recibidos[usuario])
	}
}
