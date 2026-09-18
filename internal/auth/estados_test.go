package auth

import (
	"errors"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
)

func TestPuedeTransicionar(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nombre   string
		anterior EstadoCredencial
		nuevo    EstadoCredencial
		want     bool
	}{
		{"pendiente a activo", EstadoPendienteVerificacion, EstadoActivo, true},
		{"pendiente a cerrado", EstadoPendienteVerificacion, EstadoCerrado, true},
		{"activo a suspendido", EstadoActivo, EstadoSuspendido, true},
		{"pendiente a suspendido", EstadoPendienteVerificacion, EstadoSuspendido, false},
		{"activo a pendiente", EstadoActivo, EstadoPendienteVerificacion, false},
		{"cerrado a activo", EstadoCerrado, EstadoActivo, false},
		{"suspendido a activo", EstadoSuspendido, EstadoActivo, false},
		{"activo a cerrado", EstadoActivo, EstadoCerrado, false},
		{"sin_login a activo", EstadoSinLogin, EstadoActivo, false},
		{"activo a sin_login", EstadoActivo, EstadoSinLogin, false},
		{"activo a activo", EstadoActivo, EstadoActivo, false},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			t.Parallel()
			if got := PuedeTransicionar(caso.anterior, caso.nuevo); got != caso.want {
				t.Errorf("PuedeTransicionar(%s, %s) = %v, want %v", caso.anterior, caso.nuevo, got, caso.want)
			}
		})
	}
}

func TestTransicionarCredencialRechazaElParNoPermitidoSinTocarLaBase(t *testing.T) {
	t.Parallel()

	var sentencias []string
	q := repositorio.New(&fakeTransaccionAuth{eventos: &sentencias})
	gestor := NuevoGestorDeEstados(RegistradorNulo{})

	err := gestor.TransicionarCredencial(t.Context(), q, uuid.New(), EstadoPendienteVerificacion, EstadoSuspendido, "no_permitida")
	if !errors.Is(err, ErrTransicionNoPermitida) {
		t.Fatalf("error = %v, quiero %v", err, ErrTransicionNoPermitida)
	}
	if len(sentencias) != 0 {
		t.Errorf("una transición no permitida tocó la base: %v", sentencias)
	}
}
