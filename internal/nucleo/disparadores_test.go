package nucleo

import (
	"testing"
	"time"
)

func TestRellenoDeConciliacionNoFalla(t *testing.T) {
	t.Parallel()

	servicio := &Servicio{}
	if corridas, err := servicio.Conciliar(t.Context(), 5*time.Minute); err != nil || corridas != 0 {
		t.Errorf("Conciliar() = (%d, %v), want (0, nil)", corridas, err)
	}
}
