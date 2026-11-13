package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	for _, args := range [][]string{{"-proveedor", "otro"}, {"-proveedor", "gemini"}, {"-proveedor", "gemini", "-nivel-gratuito-confirmado"}, {"-continuar", "archivo.json"}, {"-proveedor", "local"}, {"-proveedor", "local", "-familias", "consulta,inexistente"}, {"-ids", "saldo_coloquial"}, {"-proveedor", "local", "-nivel-gratuito-confirmado"}} {
		var salida, diagnostico bytes.Buffer
		if codigo := ejecutar(t.Context(), args, &salida, &diagnostico); codigo != 2 || salida.Len() != 0 {
			t.Fatalf("aceptó configuración incompleta: %v", args)
		}
	}
	t.Setenv("VERA_MODELO", "modelo-de-prueba")
	var salida, diagnostico bytes.Buffer
	if codigo := ejecutar(t.Context(), []string{"-proveedor", "local", "-ids", "no_existe"}, &salida, &diagnostico); codigo != 2 || !bytes.Contains(diagnostico.Bytes(), []byte("no existe")) {
		t.Fatalf("aceptó un id inexistente: %d %s", codigo, &diagnostico)
	}
	t.Setenv("CI", "true")
	salida.Reset()
	diagnostico.Reset()
	if codigo := ejecutar(t.Context(), []string{"-proveedor", "gemini", "-nivel-gratuito-confirmado"}, &salida, &diagnostico); codigo != 2 || !bytes.Contains(diagnostico.Bytes(), []byte("CI")) {
		t.Fatal("habilitó modo real en CI")
	}
}

func TestModoLocalValidaEntradaSinExigirNivelGratuito(t *testing.T) {
	if err := proveedorLocalValido("http://127.0.0.1:11434", 45*time.Second, 0); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		url     string
		timeout time.Duration
		temp    float64
	}{{"", time.Second, 0}, {"http://127.0.0.1:11434", 0, 0}, {"http://127.0.0.1:11434", 6 * time.Minute, 0}, {"http://127.0.0.1:11434", time.Second, 2}} {
		if err := proveedorLocalValido(tc.url, tc.timeout, tc.temp); err == nil {
			t.Fatalf("aceptó configuración local inválida: %+v", tc)
		}
	}
	if got := lista(" consulta , , inyeccion "); len(got) != 2 || got[0] != "consulta" || got[1] != "inyeccion" {
		t.Fatalf("lista=%v", got)
	}
}
