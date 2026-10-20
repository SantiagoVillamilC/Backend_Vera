package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
)

func TestComandoInformeDeterminista(t *testing.T) {
	var salida, diagnostico bytes.Buffer
	if codigo := ejecutar(t.Context(), nil, &salida, &diagnostico); codigo != 0 {
		t.Fatalf("código=%d diagnóstico=%s", codigo, &diagnostico)
	}
	var informe evaluacion.Informe
	if err := json.Unmarshal(salida.Bytes(), &informe); err != nil {
		t.Fatal(err)
	}
	if !informe.Aprobado || informe.Modo != "simulado" || informe.Total < 40 || informe.Total != informe.Correctos {
		t.Fatalf("informe=%+v", informe)
	}
	var repetida bytes.Buffer
	if codigo := ejecutar(t.Context(), nil, &repetida, &diagnostico); codigo != 0 || !bytes.Equal(salida.Bytes(), repetida.Bytes()) {
		t.Fatal("informe no reproducible")
	}
}

func TestComandoFallaAnteRegresionYEntradaInvalida(t *testing.T) {
	conjunto, err := evaluacion.Cargar()
	if err != nil {
		t.Fatal(err)
	}
	conjunto.Casos[0].Guion[1].Texto = "Disponible 999999.00 COP."
	datos, err := json.Marshal(conjunto)
	if err != nil {
		t.Fatal(err)
	}
	ruta := filepath.Join(t.TempDir(), "casos.json")
	if err := os.WriteFile(ruta, datos, 0600); err != nil {
		t.Fatal(err)
	}
	var salida, diagnostico bytes.Buffer
	if codigo := ejecutar(t.Context(), []string{"-casos", ruta}, &salida, &diagnostico); codigo != 1 {
		t.Fatalf("regresión aceptada: %d %s", codigo, &diagnostico)
	}
	if err := os.WriteFile(ruta, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-casos", ruta}, {"-casos", ruta + ".inexistente"}, {"-desconocido"}, {"argumento"}} {
		salida.Reset()
		diagnostico.Reset()
		if codigo := ejecutar(t.Context(), args, &salida, &diagnostico); codigo != 2 || salida.Len() != 0 {
			t.Fatalf("aceptó entrada inválida: %v código=%d", args, codigo)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if codigo := ejecutar(ctx, nil, &salida, &diagnostico); codigo != 2 {
		t.Fatalf("cancelación ignorada: %d", codigo)
	}
}

func TestModoRealNoEsImplicitoNiSeEjecutaEnCI(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("VERA_MODELO", "")
	for _, args := range [][]string{{"-modo", "otro"}, {"-modo", "real"}, {"-modo", "real", "-nivel-gratuito-confirmado"}, {"-continuar", "archivo.json"}} {
		var salida, diagnostico bytes.Buffer
		if codigo := ejecutar(t.Context(), args, &salida, &diagnostico); codigo != 2 || salida.Len() != 0 {
			t.Fatalf("aceptó configuración incompleta: %v", args)
		}
	}
	t.Setenv("CI", "true")
	var salida, diagnostico bytes.Buffer
	if codigo := ejecutar(t.Context(), []string{"-modo", "real", "-nivel-gratuito-confirmado"}, &salida, &diagnostico); codigo != 2 || !bytes.Contains(diagnostico.Bytes(), []byte("CI")) {
		t.Fatal("habilitó modo real en CI")
	}
}
