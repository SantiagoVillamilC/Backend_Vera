package main

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/ollama"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
)

func TestConfigVera(t *testing.T) {
	for _, caso := range []struct {
		nombre     string
		vars       map[string]string
		errorCampo string
		timeout    time.Duration
		modelo     string
		url        string
	}{
		{"simulado", nil, "", 10 * time.Second, "", "http://127.0.0.1:11434"},
		{"gemini", map[string]string{"VERA_PROVEEDOR": "gemini", "GEMINI_API_KEY": "clave", "VERA_MODELO": "flash-prueba"}, "", 10 * time.Second, "flash-prueba", "http://127.0.0.1:11434"},
		{"proveedor", map[string]string{"VERA_PROVEEDOR": "otro"}, "VERA_PROVEEDOR", 0, "", ""},
		{"sinClave", map[string]string{"VERA_PROVEEDOR": "gemini", "VERA_MODELO": "flash-prueba"}, "GEMINI_API_KEY", 0, "", ""},
		{"sinModelo", map[string]string{"VERA_PROVEEDOR": "gemini", "GEMINI_API_KEY": "clave"}, "VERA_MODELO", 0, "", ""},
		{"timeout", map[string]string{"VERA_TIMEOUT_LLAMADA": "0s"}, "VERA_TIMEOUT_LLAMADA", 0, "", ""},
		{"timeoutLargo", map[string]string{"VERA_TIMEOUT_LLAMADA": "31s"}, "VERA_TIMEOUT_LLAMADA", 0, "", ""},
		{"timeoutInvalido", map[string]string{"VERA_TIMEOUT_LLAMADA": "abc"}, "VERA_TIMEOUT_LLAMADA", 0, "", ""},
		{"temperatura", map[string]string{"VERA_TEMPERATURA": "2"}, "VERA_TEMPERATURA", 0, "", ""},
		{"nan", map[string]string{"VERA_TEMPERATURA": "NaN"}, "VERA_TEMPERATURA", 0, "", ""},
		{"inf", map[string]string{"VERA_TEMPERATURA": "Inf"}, "VERA_TEMPERATURA", 0, "", ""},
		{"local", map[string]string{"VERA_PROVEEDOR": "local"}, "", 20 * time.Second, "gemma4-e4b-local", "http://127.0.0.1:11434"},
		{"localUrl", map[string]string{"VERA_PROVEEDOR": "local", "VERA_OLLAMA_URL": "http://127.0.0.1:11435"}, "", 20 * time.Second, "gemma4-e4b-local", "http://127.0.0.1:11435"},
		{"localModelo", map[string]string{"VERA_PROVEEDOR": "local", "VERA_MODELO": "gemma4-e2b-local"}, "", 20 * time.Second, "gemma4-e2b-local", "http://127.0.0.1:11434"},
		{"localUrlSinEsquema", map[string]string{"VERA_PROVEEDOR": "local", "VERA_OLLAMA_URL": "127.0.0.1:11434"}, "VERA_OLLAMA_URL", 0, "", ""},
		{"localUrlConRuta", map[string]string{"VERA_PROVEEDOR": "local", "VERA_OLLAMA_URL": "http://127.0.0.1:11434/api"}, "VERA_OLLAMA_URL", 0, "", ""},
		{"localTimeoutLargo", map[string]string{"VERA_PROVEEDOR": "local", "VERA_TIMEOUT_LLAMADA": "6m"}, "VERA_TIMEOUT_LLAMADA", 0, "", ""},
		{"localSinModelo", map[string]string{"VERA_PROVEEDOR": "local", "VERA_MODELO": "   "}, "VERA_MODELO", 0, "", ""},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			for _, key := range []string{"VERA_PROVEEDOR", "GEMINI_API_KEY", "VERA_MODELO", "VERA_OLLAMA_URL", "VERA_TIMEOUT_LLAMADA", "VERA_TEMPERATURA", "VERA_PLAZO_ORQUESTADOR", "VERA_TIMEOUT_LLAMADA", "VERA_STT_URL", "VERA_STT_TOKEN", "VERA_STT_TIMEOUT"} {
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
			if cfg.Timeout != caso.timeout || cfg.Temperatura != 0.2 || cfg.Modelo != caso.modelo || cfg.OllamaURL != caso.url {
				t.Fatalf("configuración resuelta: %+v", cfg)
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
			case "local":
				if _, ok := p.(*ollama.Proveedor); !ok {
					t.Fatalf("proveedor %T", p)
				}
			}
		})
	}
}

func TestProveedorLocalNoSondeaAlArrancar(t *testing.T) {
	// Un puerto que nadie escucha: si Nuevo abriera conexión, fallaría.
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + l.Addr().String()
	_ = l.Close()
	t.Setenv("VERA_PROVEEDOR", "local")
	t.Setenv("VERA_OLLAMA_URL", url)
	t.Setenv("VERA_MODELO", "gemma4-e4b-local")
	cfg, errs := cargarConfigVera()
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if _, err := nuevoProveedorIA(t.Context(), cfg); err != nil {
		t.Fatalf("el proveedor local no debe sondear al arrancar: %v", err)
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

func TestConfigVeraPlazoOrquestador(t *testing.T) {
	baseDeConfig(t)
	t.Setenv("VERA_PROVEEDOR", "local")
	t.Setenv("VERA_PLAZO_ORQUESTADOR", "2m")
	cfg, errs := cargarConfigVera()
	if len(errs) != 0 {
		t.Fatalf("cargarConfigVera con plazo válido: %v", errs)
	}
	if cfg.PlazoOrquestador != 2*time.Minute {
		t.Fatalf("plazo = %v, quiero 2m", cfg.PlazoOrquestador)
	}

	t.Setenv("VERA_PLAZO_ORQUESTADOR", "0s")
	if _, errs := cargarConfigVera(); len(errs) == 0 || !strings.Contains(errs[0].Error(), "VERA_PLAZO_ORQUESTADOR") {
		t.Fatalf("errs = %v, quiero un error de VERA_PLAZO_ORQUESTADOR", errs)
	}
}
