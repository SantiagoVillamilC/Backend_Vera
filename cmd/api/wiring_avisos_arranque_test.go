package main

import (
	"context"
	"testing"
	"time"
)

type emisorQueAvisaLaLlamada struct {
	llamadas chan struct{}
}

func (e *emisorQueAvisaLaLlamada) EmitirAvisos(_ context.Context, _ time.Time) (int, error) {
	e.llamadas <- struct{}{}
	return 1, nil
}

func TestLanzarAvisosAlArrancarCorreUnaVez(t *testing.T) {
	t.Parallel()

	emisor := &emisorQueAvisaLaLlamada{llamadas: make(chan struct{}, 2)}
	lanzarAvisosAlArrancar(t.Context(), true, emisor)

	select {
	case <-emisor.llamadas:
	case <-time.After(2 * time.Second):
		t.Fatal("la corrida al arrancar no llamó al emisor")
	}

	select {
	case <-emisor.llamadas:
		t.Fatal("la corrida al arrancar llamó al emisor más de una vez")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestLanzarAvisosAlArrancarConLaBanderaApagadaNoCorre(t *testing.T) {
	t.Parallel()

	emisor := &emisorQueAvisaLaLlamada{llamadas: make(chan struct{}, 2)}
	lanzarAvisosAlArrancar(t.Context(), false, emisor)

	select {
	case <-emisor.llamadas:
		t.Fatal("con la bandera apagada no debe lanzar la corrida")
	case <-time.After(200 * time.Millisecond):
	}
}
