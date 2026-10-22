package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestDemoMuestraFlujoCompletoSinProveedorReal(t *testing.T) {
	var salida, diagnostico bytes.Buffer
	crear := func(context.Context, string) (copilot.ProveedorIA, error) {
		t.Fatal("demo llamó al proveedor real")
		return nil, nil
	}
	codigo := ejecutar(t.Context(), []string{"-demo"}, strings.NewReader(""), &salida, &diagnostico, crear)
	if codigo != 0 {
		t.Fatalf("demo=%d: %s", codigo, diagnostico.String())
	}
	for _, texto := range []string{"DEMO SIMULADA", "FUNCIÓN ELEGIDA", "consultar_saldo", "BACKEND DE PRUEBA", "\"centavos\": \"12000000\"", "respuesta final", "120000.00 COP", "2/2"} {
		if !strings.Contains(salida.String(), texto) {
			t.Fatalf("falta %q en %s", texto, salida.String())
		}
	}
}

func TestRealRequiereConfiguracionAntesDeCrearProveedor(t *testing.T) {
	t.Setenv("VERA_MODELO", "prueba")
	var salida bytes.Buffer
	codigo := ejecutar(t.Context(), nil, strings.NewReader(""), &salida, &salida, func(context.Context, string) (copilot.ProveedorIA, error) {
		t.Fatal("creó cliente sin confirmar")
		return nil, nil
	})
	if codigo != 2 {
		t.Fatal(codigo)
	}
}

func TestComandosInteractivosYErroresSinSecretos(t *testing.T) {
	t.Setenv("VERA_MODELO", "prueba")
	t.Setenv("CI", "")
	var salida bytes.Buffer
	p := proveedorError{}
	codigo := ejecutar(t.Context(), []string{"-nivel-gratuito-confirmado", "-intervalo", "0"}, strings.NewReader("/ayuda\n/cuota\n/nuevo\nHola\n/salir\n"), &salida, &salida, func(context.Context, string) (copilot.ProveedorIA, error) { return p, nil })
	if codigo != 0 || strings.Contains(salida.String(), "SECRETO-DEL-SDK") || !strings.Contains(salida.String(), "0/20") || !strings.Contains(salida.String(), "Conversación nueva") || !strings.Contains(salida.String(), "[ ERROR ]") {
		t.Fatalf("consola=%d: %s", codigo, salida.String())
	}
	if got := visible("\x1b[31mtexto\nseguro\x00"); strings.ContainsAny(got, "\x1b\x00") || !strings.Contains(got, "\n") {
		t.Fatal(got)
	}
}

type proveedorError struct{}

func (proveedorError) Generar(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	return copilot.RespuestaIA{}, errors.New("SECRETO-DEL-SDK")
}
