package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

type emisorDeAvisosFalso struct {
	llamadas  int
	instantes []time.Time
	emitidos  int
	err       error
	panico    bool
}

func (e *emisorDeAvisosFalso) EmitirAvisos(_ context.Context, ahora time.Time) (int, error) {
	e.llamadas++
	e.instantes = append(e.instantes, ahora)
	if e.panico {
		panic("emisor roto")
	}
	return e.emitidos, e.err
}

func relojFijo(instante time.Time) func() time.Time {
	return func() time.Time { return instante }
}

func TestAvisosAlArrancarEmiteUnaVezConElReloj(t *testing.T) {
	t.Parallel()

	instante := time.Date(2026, time.November, 12, 15, 20, 0, 0, time.UTC)
	emisor := &emisorDeAvisosFalso{emitidos: 7}

	emitidos := correrAvisosAlArrancar(t.Context(), true, emisor, relojFijo(instante))

	if emisor.llamadas != 1 {
		t.Fatalf("llamadas = %d, quiero 1", emisor.llamadas)
	}
	if len(emisor.instantes) != 1 || !emisor.instantes[0].Equal(instante) {
		t.Fatalf("instantes = %v, quiero [%v]", emisor.instantes, instante)
	}
	if emitidos != 7 {
		t.Errorf("emitidos = %d, quiero 7", emitidos)
	}
}

func TestAvisosAlArrancarConLaBanderaApagadaNoLlama(t *testing.T) {
	t.Parallel()

	emisor := &emisorDeAvisosFalso{emitidos: 7}

	emitidos := correrAvisosAlArrancar(t.Context(), false, emisor, relojFijo(time.Now()))

	if emisor.llamadas != 0 {
		t.Fatalf("llamadas = %d, quiero 0", emisor.llamadas)
	}
	if emitidos != 0 {
		t.Errorf("emitidos = %d, quiero 0", emitidos)
	}
}

func TestAvisosAlArrancarNoPropagaElErrorDelEmisor(t *testing.T) {
	t.Parallel()

	emisor := &emisorDeAvisosFalso{err: errors.New("base caída")}

	emitidos := correrAvisosAlArrancar(t.Context(), true, emisor, relojFijo(time.Now()))

	if emisor.llamadas != 1 {
		t.Fatalf("llamadas = %d, quiero 1", emisor.llamadas)
	}
	if emitidos != 0 {
		t.Errorf("emitidos = %d, quiero 0", emitidos)
	}
}

func TestAvisosAlArrancarContieneElPanicoDelEmisor(t *testing.T) {
	t.Parallel()

	emisor := &emisorDeAvisosFalso{panico: true}

	emitidos := correrAvisosAlArrancar(t.Context(), true, emisor, relojFijo(time.Now()))

	if emisor.llamadas != 1 {
		t.Fatalf("llamadas = %d, quiero 1", emisor.llamadas)
	}
	if emitidos != 0 {
		t.Errorf("emitidos = %d, quiero 0", emitidos)
	}
}
