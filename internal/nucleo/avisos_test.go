package nucleo

import (
	"context"
	"testing"
	"time"
)

// TestAvisoTrasAsientoLlegaConElUsuarioYElReloj verifica que el callback recibe al titular y la hora del servicio.
func TestAvisoTrasAsientoLlegaConElUsuarioYElReloj(t *testing.T) {
	t.Parallel()

	instante := time.Date(2026, time.November, 12, 15, 20, 0, 0, time.UTC)
	var usuarioRecibido string
	var horaRecibida time.Time
	llamadas := 0
	servicio := &Servicio{
		ahora: func() time.Time { return instante },
		avisoTrasAsiento: func(_ context.Context, usuarioID string, hora time.Time) {
			llamadas++
			usuarioRecibido = usuarioID
			horaRecibida = hora
		},
	}

	servicio.avisarTrasAsiento(t.Context(), "u-1")

	if llamadas != 1 {
		t.Fatalf("llamadas = %d, quiero 1", llamadas)
	}
	if usuarioRecibido != "u-1" {
		t.Errorf("usuario = %q, quiero u-1", usuarioRecibido)
	}
	if !horaRecibida.Equal(instante) {
		t.Errorf("hora = %v, quiero %v", horaRecibida, instante)
	}
}

// TestAvisoTrasAsientoSinCallbackNoHaceNada cubre el servicio armado sin la opción.
func TestAvisoTrasAsientoSinCallbackNoHaceNada(t *testing.T) {
	t.Parallel()

	servicio := &Servicio{}

	servicio.avisarTrasAsiento(t.Context(), "u-1")
}

// TestAvisoTrasAsientoNoDejaPasarElPanico comprueba que una falla del callback no rompe el cierre.
func TestAvisoTrasAsientoNoDejaPasarElPanico(t *testing.T) {
	t.Parallel()

	rastreador := &rastreadorFalso{}
	servicio := &Servicio{
		rastreador: rastreador,
		avisoTrasAsiento: func(context.Context, string, time.Time) {
			panic("emisor roto")
		},
	}

	servicio.avisarTrasAsiento(t.Context(), "u-1")

	if len(rastreador.capturas) != 1 {
		t.Fatalf("capturas = %d, quiero 1", len(rastreador.capturas))
	}
	if erro := rastreador.capturas[0].err; erro == nil {
		t.Error("la captura debe traer el pánico del callback")
	}
}
