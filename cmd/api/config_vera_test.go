package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
)

func TestConfigVera(t *testing.T) {
	for _, caso := range []struct {
		nombre     string
		vars       map[string]string
		errorCampo string
	}{
		{"simulado", nil, ""},
		{"gemini", map[string]string{"VERA_PROVEEDOR": "gemini", "GEMINI_API_KEY": "clave", "VERA_MODELO": "flash-prueba"}, ""},
		{"proveedor", map[string]string{"VERA_PROVEEDOR": "otro"}, "VERA_PROVEEDOR"},
		{"sinClave", map[string]string{"VERA_PROVEEDOR": "gemini", "VERA_MODELO": "flash-prueba"}, "GEMINI_API_KEY"},
		{"sinModelo", map[string]string{"VERA_PROVEEDOR": "gemini", "GEMINI_API_KEY": "clave"}, "VERA_MODELO"},
		{"timeout", map[string]string{"VERA_TIMEOUT_LLAMADA": "0s"}, "VERA_TIMEOUT_LLAMADA"},
		{"timeoutLargo", map[string]string{"VERA_TIMEOUT_LLAMADA": "31s"}, "VERA_TIMEOUT_LLAMADA"},
		{"timeoutInvalido", map[string]string{"VERA_TIMEOUT_LLAMADA": "abc"}, "VERA_TIMEOUT_LLAMADA"},
		{"temperatura", map[string]string{"VERA_TEMPERATURA": "2"}, "VERA_TEMPERATURA"},
		{"nan", map[string]string{"VERA_TEMPERATURA": "NaN"}, "VERA_TEMPERATURA"},
		{"inf", map[string]string{"VERA_TEMPERATURA": "Inf"}, "VERA_TEMPERATURA"},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			for _, key := range []string{"VERA_PROVEEDOR", "GEMINI_API_KEY", "VERA_MODELO", "VERA_TIMEOUT_LLAMADA", "VERA_TEMPERATURA"} {
				t.Setenv(key, "")
			}
			for k, v := range caso.vars {
				t.Setenv(k, v)
			}
			cfg, errs := cargarConfigVera()
			if caso.errorCampo != "" {
				var encontrado bool
				for _, err := range errs {
					encontrado = encontrado || strings.Contains(err.Error(), caso.errorCampo)
				}
				if !encontrado {
					t.Fatalf("falta error %s: %v", caso.errorCampo, errs)
				}
				return
			}
			if len(errs) > 0 {
				t.Fatal(errs)
			}
			if cfg.Timeout != 10*time.Second || cfg.Temperatura != 0.2 {
				t.Fatalf("defaults: %+v", cfg)
			}
			p, err := nuevoProveedorIA(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			switch cfg.Proveedor {
			case "simulado":
				if _, ok := p.(*provsim.Proveedor); !ok {
					t.Fatalf("proveedor %T", p)
				}
			case "gemini":
				if _, ok := p.(*gemini.Proveedor); !ok {
					t.Fatalf("proveedor %T", p)
				}
			}
		})
	}
}

func TestLoadConfigExigeClaveGemini(t *testing.T) {
	t.Setenv("VERA_PROVEEDOR", "gemini")
	t.Setenv("GEMINI_API_KEY", "")
	_, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("LoadConfig no validó Gemini: %v", err)
	}
}

func TestConfigVeraNoFiltraClave(t *testing.T) {
	var b bytes.Buffer
	cfg := Config{Vera: ConfigVera{Proveedor: "gemini", APIKey: "secreto-que-no-se-imprime", Modelo: "flash-prueba"}}
	slog.New(slog.NewJSONHandler(&b, nil)).Info("config", "config", cfg)
	if strings.Contains(b.String(), cfg.Vera.APIKey) || !strings.Contains(b.String(), `"gemini_api_key_set":true`) {
		t.Fatalf("log inseguro: %s", b.String())
	}
}
