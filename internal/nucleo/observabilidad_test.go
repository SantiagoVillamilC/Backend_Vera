package nucleo

import (
	"context"
	"testing"
)

type capturaFalsa struct {
	err      error
	contexto ContextoError
}

// rastreadorFalso es el adaptador de prueba del puerto: guarda cada captura para inspeccionarla.
type rastreadorFalso struct {
	capturas []capturaFalsa
}

func (f *rastreadorFalso) Capturar(_ context.Context, err error, contexto ContextoError) {
	f.capturas = append(f.capturas, capturaFalsa{err: err, contexto: contexto})
}

func TestNewServicioInyectaElRastreador(t *testing.T) {
	t.Parallel()

	falso := &rastreadorFalso{}
	servicio := NewServicio(nil, KYCNoDisponible{}, RielNoDisponible{}, CustodioNoDisponible{}, falso)
	if servicio.rastreador != RastreadorErrores(falso) {
		t.Errorf("rastreador inyectado = %v, quiero el falso", servicio.rastreador)
	}
}
