package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// ejecutorDeFilasFalso responde cada Exec con las filas afectadas configuradas y registra las sentencias.
type ejecutorDeFilasFalso struct {
	filas      int64
	sentencias []string
}

func (e *ejecutorDeFilasFalso) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	e.sentencias = append(e.sentencias, sql)
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", e.filas)), nil
}

func (e *ejecutorDeFilasFalso) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("Query no esperada")
}

func (e *ejecutorDeFilasFalso) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func TestActivarCredencialConCeroFilasEsConflictoSinEscribirHistorial(t *testing.T) {
	t.Parallel()

	ejecutor := &ejecutorDeFilasFalso{filas: 0}
	registrador := &registradorFalso{}
	gestor := NuevoGestorDeEstados(registrador)

	err := gestor.ActivarCredencial(t.Context(), repositorio.New(ejecutor), uuid.New(), "celular_verificado")
	if !errors.Is(err, ErrConflictoDeEstado) {
		t.Fatalf("error = %v, quiero %v", err, ErrConflictoDeEstado)
	}
	if len(ejecutor.sentencias) != 1 {
		t.Errorf("sentencias = %d, quiero solo el compare-and-set: %v", len(ejecutor.sentencias), ejecutor.sentencias)
	}
	if len(registrador.eventos) != 0 {
		t.Errorf("un conflicto publicó eventos: %+v", registrador.eventos)
	}
}

func TestActivarCredencialConUnaFilaSellaElCelularEscribeHistorialYPublicaVerificacion(t *testing.T) {
	t.Parallel()

	ejecutor := &ejecutorDeFilasFalso{filas: 1}
	registrador := &registradorFalso{}
	gestor := NuevoGestorDeEstados(registrador)
	id := uuid.New()

	if err := gestor.ActivarCredencial(t.Context(), repositorio.New(ejecutor), id, "celular_verificado"); err != nil {
		t.Fatalf("ActivarCredencial: %v", err)
	}
	if len(ejecutor.sentencias) != 2 {
		t.Fatalf("sentencias = %d, quiero compare-and-set e historial: %v", len(ejecutor.sentencias), ejecutor.sentencias)
	}
	if !strings.Contains(ejecutor.sentencias[0], "celular_verificado_en = now()") || !strings.Contains(ejecutor.sentencias[0], "estado = 'pendiente_verificacion'") {
		t.Errorf("el compare-and-set no sella el celular sobre pendiente_verificacion: %s", ejecutor.sentencias[0])
	}
	if !strings.Contains(ejecutor.sentencias[1], "auth.transiciones_credencial") {
		t.Errorf("la segunda sentencia no es el historial: %s", ejecutor.sentencias[1])
	}
	if len(registrador.eventos) != 1 || registrador.eventos[0].Tipo != TipoEventoVerificacion || registrador.eventos[0].CredencialID != id || registrador.eventos[0].Contexto != "celular_verificado" {
		t.Errorf("eventos = %+v, quiero un evento verificacion de %s con el motivo", registrador.eventos, id)
	}
}

func TestTransicionarCredencialGenericaNoSellaElCelular(t *testing.T) {
	t.Parallel()

	ejecutor := &ejecutorDeFilasFalso{filas: 1}
	gestor := NuevoGestorDeEstados(RegistradorNulo{})

	if err := gestor.TransicionarCredencial(t.Context(), repositorio.New(ejecutor), uuid.New(), EstadoPendienteVerificacion, EstadoCerrado, MotivoPendienteVencida); err != nil {
		t.Fatalf("TransicionarCredencial: %v", err)
	}
	if strings.Contains(ejecutor.sentencias[0], "celular_verificado_en") {
		t.Errorf("la consulta genérica toca celular_verificado_en: %s", ejecutor.sentencias[0])
	}
}
