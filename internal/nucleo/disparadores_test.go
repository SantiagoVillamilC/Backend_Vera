package nucleo

import (
	"testing"
	"time"
)

func TestRellenosDeDisparadoresNoFallan(t *testing.T) {
	t.Parallel()

	servicio := &Servicio{}
	if err := servicio.DespacharOutbox(t.Context()); err != nil {
		t.Errorf("DespacharOutbox() = %v, want nil", err)
	}
	if corridas, err := servicio.Conciliar(t.Context(), 5*time.Minute); err != nil || corridas != 0 {
		t.Errorf("Conciliar() = (%d, %v), want (0, nil)", corridas, err)
	}
}
