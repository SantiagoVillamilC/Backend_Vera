package auth

import (
	"errors"
	"testing"
)

func TestNuevoResolutorDestinoExigeEnfriamientoPositivo(t *testing.T) {
	defer func() {
		recuperado := recover()
		if recuperado == nil {
			t.Fatal("NuevoResolutorDestino(nil, 0) no entró en pánico")
		}
		err, ok := recuperado.(error)
		if !ok || !errors.Is(err, errEnfriamientoNoPositivo) {
			t.Fatalf("NuevoResolutorDestino(nil, 0) falló con %v, quiero %v", recuperado, errEnfriamientoNoPositivo)
		}
	}()
	NuevoResolutorDestino(nil, 0)
}
