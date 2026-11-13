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

type emisorDelUsuarioFalso struct {
	llamadas  int
	usuarios  []string
	instantes []time.Time
	emitidos  int
	err       error
	panico    bool
}

func (e *emisorDelUsuarioFalso) EmitirAvisosDelUsuario(_ context.Context, usuarioID string, ahora time.Time) (int, error) {
	e.llamadas++
	e.usuarios = append(e.usuarios, usuarioID)
	e.instantes = append(e.instantes, ahora)
	if e.panico {
		panic("emisor roto")
	}
	return e.emitidos, e.err
}

func TestFrenoDeEmisionesDaUnTurnoPorMinutoYUsuario(t *testing.T) {
	t.Parallel()

	inicio := time.Date(2026, time.November, 12, 15, 0, 0, 0, time.UTC)
	freno := nuevoFrenoDeEmisiones(true, &emisorDelUsuarioFalso{}, relojFijo(inicio))

	if !freno.tomarTurno("u-1", inicio) {
		t.Fatal("el primer turno de u-1 debe pasar")
	}
	if freno.tomarTurno("u-1", inicio.Add(30*time.Second)) {
		t.Fatal("el segundo turno dentro del mismo minuto debe frenarse")
	}
	if !freno.tomarTurno("u-1", inicio.Add(time.Minute+time.Second)) {
		t.Fatal("pasado el minuto, u-1 debe volver a pasar")
	}
	if !freno.tomarTurno("u-2", inicio.Add(30*time.Second)) {
		t.Fatal("otro usuario tiene su propio turno")
	}
}

func TestFrenoDeEmisionesApagadoNoEmite(t *testing.T) {
	t.Parallel()

	emisor := &emisorDelUsuarioFalso{emitidos: 3}
	freno := nuevoFrenoDeEmisiones(false, emisor, relojFijo(time.Now()))

	freno.disparar(t.Context(), "u-1", time.Now())

	if emisor.llamadas != 0 {
		t.Fatalf("llamadas = %d, quiero 0", emisor.llamadas)
	}
}

func TestFrenoDeEmisionesNoPropagaElErrorDelEmisor(t *testing.T) {
	t.Parallel()

	emisor := &emisorDelUsuarioFalso{err: errors.New("base caída")}
	freno := nuevoFrenoDeEmisiones(true, emisor, relojFijo(time.Now()))

	emitidos := freno.emitirYRegistrar(t.Context(), "u-1", time.Now())

	if emisor.llamadas != 1 {
		t.Fatalf("llamadas = %d, quiero 1", emisor.llamadas)
	}
	if emitidos != 0 {
		t.Errorf("emitidos = %d, quiero 0", emitidos)
	}
}

func TestFrenoDeEmisionesContieneElPanicoDelEmisor(t *testing.T) {
	t.Parallel()

	emisor := &emisorDelUsuarioFalso{panico: true}
	freno := nuevoFrenoDeEmisiones(true, emisor, relojFijo(time.Now()))

	emitidos := freno.emitirYRegistrar(t.Context(), "u-1", time.Now())

	if emisor.llamadas != 1 {
		t.Fatalf("llamadas = %d, quiero 1", emisor.llamadas)
	}
	if emitidos != 0 {
		t.Errorf("emitidos = %d, quiero 0", emitidos)
	}
}
