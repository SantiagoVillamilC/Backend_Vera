package main

import (
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/compuesto"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/destinolocal"
)

// entornoDestinoLimpio vacía las variables que intervienen en la validación de destinos.
func entornoDestinoLimpio(t *testing.T) {
	t.Helper()
	for _, key := range []string{"VERA_PROVEEDOR", "VERA_MODELO", "VERA_OLLAMA_URL", "VERA_STT_URL", "VERA_STT_TOKEN", "VERA_STT_TIMEOUT", destinolocal.VariableHostPermitido} {
		t.Setenv(key, "")
	}
}

func hayErrorDe(errs []error, variable string) bool {
	for _, err := range errs {
		if strings.Contains(err.Error(), variable) {
			return true
		}
	}
	return false
}

func TestConfigVeraSTTIPPrivadaSoloConLaVariable(t *testing.T) {
	entornoDestinoLimpio(t)
	t.Setenv("VERA_STT_URL", "http://172.18.0.1:8777")
	t.Setenv("VERA_STT_TOKEN", tokenSTTDePrueba)
	if _, errs := cargarConfigVera(); !hayErrorDe(errs, "VERA_STT_URL") {
		t.Fatalf("sin la variable debe fallar VERA_STT_URL: %v", errs)
	}
	t.Setenv(destinolocal.VariableHostPermitido, "172.18.0.1")
	cfg, errs := cargarConfigVera()
	if len(errs) != 0 {
		t.Fatalf("con la variable debe aceptarse: %v", errs)
	}
	p, err := nuevoProveedorIA(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*compuesto.Proveedor); !ok {
		t.Fatalf("proveedor %T, quiero compuesto", p)
	}
}

func TestConfigVeraSTTRechazaOtrasIPsAunConLaVariable(t *testing.T) {
	for _, valor := range []string{"http://172.18.0.2:8777", "http://172.18.0.1", "http://172.18.0.1:8777/x", "http://8.8.8.8:8777", "http://host.docker.internal:8777", "https://172.18.0.1:8777", "http://[::1]:8777"} {
		t.Run(valor, func(t *testing.T) {
			entornoDestinoLimpio(t)
			t.Setenv(destinolocal.VariableHostPermitido, "172.18.0.1")
			t.Setenv("VERA_STT_URL", valor)
			t.Setenv("VERA_STT_TOKEN", tokenSTTDePrueba)
			if _, errs := cargarConfigVera(); !hayErrorDe(errs, "VERA_STT_URL") {
				t.Fatalf("debe fallar VERA_STT_URL: %v", errs)
			}
		})
	}
}

func TestConfigVeraOllamaDestinos(t *testing.T) {
	casos := []struct {
		nombre, url, permitidos string
		valido                  bool
	}{
		{"porDefecto", "", "", true},
		{"loopbackConPuerto", "http://127.0.0.1:11435", "", true},
		{"loopbackBarraFinal", "http://127.0.0.1:11434/", "", true},
		{"privadaSinVariable", "http://172.18.0.1:11434", "", false},
		{"privadaConVariable", "http://172.18.0.1:11434", "172.18.0.1", true},
		{"privadaConVariableYBarra", "http://172.18.0.1:11434/", "172.18.0.1", true},
		{"otraPrivada", "http://172.18.0.2:11434", "172.18.0.1", false},
		{"hostname", "http://ollama:11434", "172.18.0.1", false},
		{"https", "https://127.0.0.1:11434", "", false},
		{"publica", "http://8.8.8.8:11434", "", false},
		{"ruta", "http://127.0.0.1:11434/api", "", false},
		{"dobleBarra", "http://127.0.0.1:11434//", "", false},
		{"userinfo", "http://usuario:clave@127.0.0.1:11434", "", false},
		{"sinEsquema", "127.0.0.1:11434", "", false},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			entornoDestinoLimpio(t)
			t.Setenv("VERA_OLLAMA_URL", c.url)
			t.Setenv(destinolocal.VariableHostPermitido, c.permitidos)
			_, errs := cargarConfigVera()
			if c.valido && len(errs) != 0 {
				t.Fatalf("debe ser válida: %v", errs)
			}
			if !c.valido && !hayErrorDe(errs, "VERA_OLLAMA_URL") {
				t.Fatalf("debe fallar VERA_OLLAMA_URL: %v", errs)
			}
		})
	}
}

func TestConfigVeraListaPermitidaInvalidaFallaAlArrancar(t *testing.T) {
	for _, valor := range []string{"localhost", "8.8.8.8", "127.0.0.1", "0.0.0.0", "::1", "172.18.0.0/16", "172.18.0.1:80", "172.18.0.1,,10.0.0.1"} {
		t.Run(valor, func(t *testing.T) {
			entornoDestinoLimpio(t)
			t.Setenv(destinolocal.VariableHostPermitido, valor)
			if _, errs := cargarConfigVera(); !hayErrorDe(errs, destinolocal.VariableHostPermitido) {
				t.Fatalf("debe fallar %s: %v", destinolocal.VariableHostPermitido, errs)
			}
		})
	}
}

func TestConfigVeraSinVariableNoCambiaElComportamiento(t *testing.T) {
	entornoDestinoLimpio(t)
	t.Setenv("VERA_STT_URL", "http://127.0.0.1:8777")
	t.Setenv("VERA_STT_TOKEN", tokenSTTDePrueba)
	cfg, errs := cargarConfigVera()
	if len(errs) != 0 || cfg.OllamaURL != "http://127.0.0.1:11434" || cfg.STTURL != "http://127.0.0.1:8777" {
		t.Fatalf("defaults: cfg=%+v errs=%v", cfg, errs)
	}
}

func TestConfigVeraErroresDeDestinoNoFiltranSecretos(t *testing.T) {
	const secreto = "clave-secreta-del-destino"
	for _, variable := range []string{"VERA_STT_URL", "VERA_OLLAMA_URL"} {
		t.Run(variable, func(t *testing.T) {
			entornoDestinoLimpio(t)
			t.Setenv("VERA_STT_TOKEN", tokenSTTDePrueba)
			t.Setenv("VERA_STT_URL", "http://127.0.0.1:8777")
			t.Setenv(variable, "http://usuario:"+secreto+"@127.0.0.1:8777")
			_, errs := cargarConfigVera()
			if !hayErrorDe(errs, variable) {
				t.Fatalf("debe fallar %s: %v", variable, errs)
			}
			for _, err := range errs {
				if strings.Contains(err.Error(), secreto) {
					t.Fatalf("el error filtra el secreto: %v", err)
				}
			}
		})
	}
	entornoDestinoLimpio(t)
	t.Setenv(destinolocal.VariableHostPermitido, secreto)
	_, errs := cargarConfigVera()
	for _, err := range errs {
		if strings.Contains(err.Error(), secreto) {
			t.Fatalf("el error de la lista filtra el valor: %v", err)
		}
	}
}
