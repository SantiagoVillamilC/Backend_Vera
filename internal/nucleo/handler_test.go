package nucleo

import (
	"errors"
	"testing"
)

// Un Handler sin Servicio es un error de programación: falla al construir, no en la petición.
func TestNewHandlerConServicioNuloFallaAlConstruir(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewHandler(nil) no falló")
		}
		err, ok := r.(error)
		if !ok || !errors.Is(err, ErrServicioNulo) {
			t.Fatalf("NewHandler(nil) falló con %v, quiero %v", r, ErrServicioNulo)
		}
		if got := err.Error(); got != "nucleo: NewHandler necesita un Servicio no nulo" {
			t.Fatalf("mensaje = %q, quiero el literal del invariante", got)
		}
		t.Logf("NewHandler(nil) falló como se esperaba: %v", err)
	}()

	NewHandler(nil)
}
