package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// compensadorFalso registra el id y el contexto recibidos y devuelve el error configurado.
type compensadorFalso struct {
	err          error
	llamadas     []uuid.UUID
	ctxCancelado bool
	conPlazo     bool
}

func (c *compensadorFalso) CompensarAlta(ctx context.Context, credencialID uuid.UUID) error {
	c.llamadas = append(c.llamadas, credencialID)
	c.ctxCancelado = ctx.Err() != nil
	_, c.conPlazo = ctx.Deadline()
	return c.err
}

func TestCompensarAltaCierraLaCredencialAunqueElRequestEsteCancelado(t *testing.T) {
	t.Parallel()

	compensador := &compensadorFalso{}
	registrador := &RegistradorDePersonas{compensador: compensador}
	ctx, cancelar := context.WithCancel(t.Context())
	cancelar()
	id := uuid.New()

	registrador.compensarAlta(ctx, id)

	if len(compensador.llamadas) != 1 || compensador.llamadas[0] != id {
		t.Fatalf("llamadas = %v, quiero una con %s", compensador.llamadas, id)
	}
	if compensador.ctxCancelado {
		t.Error("la compensacion recibio el contexto cancelado del request")
	}
	if !compensador.conPlazo {
		t.Error("la compensacion no tiene plazo propio")
	}
}

func TestCompensarAltaConsignaElFalloSinPropagarlo(t *testing.T) {
	// Reemplaza el logger global, por eso no corre en paralelo.
	var salida bytes.Buffer
	previo := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&salida, nil)))
	t.Cleanup(func() { slog.SetDefault(previo) })

	compensador := &compensadorFalso{err: errors.New("base caida")}
	registrador := &RegistradorDePersonas{compensador: compensador}
	id := uuid.New()

	registrador.compensarAlta(t.Context(), id)

	log := salida.String()
	for _, quiero := range []string{"level=ERROR", id.String(), "base caida"} {
		if !strings.Contains(log, quiero) {
			t.Errorf("el log %q no contiene %q", log, quiero)
		}
	}
}
