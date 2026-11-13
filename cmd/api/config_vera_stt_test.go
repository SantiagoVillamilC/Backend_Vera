package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/compuesto"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
)

const tokenSTTDePrueba = "token-de-prueba-con-32-caracteres"

func entornoSTTLimpio(t *testing.T) {
	t.Helper()
	for _, key := range []string{"VERA_PROVEEDOR", "VERA_STT_URL", "VERA_STT_TOKEN", "VERA_STT_TIMEOUT"} {
		t.Setenv(key, "")
	}
}

func TestConfigVeraSTTValido(t *testing.T) {
	entornoSTTLimpio(t)
	t.Setenv("VERA_STT_URL", "http://127.0.0.1:8777")
	t.Setenv("VERA_STT_TOKEN", tokenSTTDePrueba)
	cfg, errs := cargarConfigVera()
	if len(errs) != 0 {
		t.Fatalf("config válida: %v", errs)
	}
	if cfg.STTURL != "http://127.0.0.1:8777" || cfg.STTToken != tokenSTTDePrueba || cfg.STTPlazo != 30*time.Second {
		t.Fatalf("config resuelta: %+v", cfg)
	}
	p, err := nuevoProveedorIA(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*compuesto.Proveedor); !ok {
		t.Fatalf("proveedor %T, quiero compuesto", p)
	}
}

func TestConfigVeraSTTTokenCorto(t *testing.T) {
	entornoSTTLimpio(t)
	t.Setenv("VERA_STT_URL", "http://127.0.0.1:8777")
	t.Setenv("VERA_STT_TOKEN", strings.Repeat("t", 31))
	_, errs := cargarConfigVera()
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "VERA_STT_TOKEN") {
		t.Fatalf("errores = %v, quiero uno de VERA_STT_TOKEN", errs)
	}
}

func TestConfigVeraSTTURLConRuta(t *testing.T) {
	for _, valor := range []string{"http://127.0.0.1:8777/audio", "http://127.0.0.1:8777/"} {
		t.Run(valor, func(t *testing.T) {
			entornoSTTLimpio(t)
			t.Setenv("VERA_STT_URL", valor)
			t.Setenv("VERA_STT_TOKEN", tokenSTTDePrueba)
			_, errs := cargarConfigVera()
			if len(errs) == 0 || !strings.Contains(errs[0].Error(), "VERA_STT_URL") {
				t.Fatalf("errores = %v, quiero uno de VERA_STT_URL", errs)
			}
		})
	}
}

func TestConfigVeraSTTURLNoLoopback(t *testing.T) {
	for _, valor := range []string{"http://192.168.1.10:8777", "http://localhost:8777", "https://127.0.0.1:8777", "127.0.0.1:8777"} {
		t.Run(valor, func(t *testing.T) {
			entornoSTTLimpio(t)
			t.Setenv("VERA_STT_URL", valor)
			t.Setenv("VERA_STT_TOKEN", tokenSTTDePrueba)
			_, errs := cargarConfigVera()
			if len(errs) == 0 || !strings.Contains(errs[0].Error(), "VERA_STT_URL") {
				t.Fatalf("errores = %v, quiero uno de VERA_STT_URL", errs)
			}
		})
	}
}

func TestConfigVeraSTTAusente(t *testing.T) {
	entornoSTTLimpio(t)
	cfg, errs := cargarConfigVera()
	if len(errs) != 0 {
		t.Fatalf("sin STT no debe haber errores: %v", errs)
	}
	if cfg.STTURL != "" || cfg.STTToken != "" || cfg.STTPlazo != 0 {
		t.Fatalf("config resuelta: %+v", cfg)
	}
	p, err := nuevoProveedorIA(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*provsim.Proveedor); !ok {
		t.Fatalf("proveedor %T, quiero el simulado sin envolver", p)
	}
}

func TestConfigVeraSTTIncompleto(t *testing.T) {
	entornoSTTLimpio(t)
	t.Setenv("VERA_STT_URL", "http://127.0.0.1:8777")
	_, errs := cargarConfigVera()
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "VERA_STT_URL") {
		t.Fatalf("errores = %v, quiero uno de VERA_STT_URL", errs)
	}
}

func TestConfigVeraSTTPlazo(t *testing.T) {
	entornoSTTLimpio(t)
	t.Setenv("VERA_STT_URL", "http://127.0.0.1:8777")
	t.Setenv("VERA_STT_TOKEN", tokenSTTDePrueba)
	t.Setenv("VERA_STT_TIMEOUT", "2m")
	cfg, errs := cargarConfigVera()
	if len(errs) != 0 || cfg.STTPlazo != 2*time.Minute {
		t.Fatalf("plazo de 2m: config=%+v errores=%v", cfg, errs)
	}
	for _, valor := range []string{"0s", "-1s", "2m1s", "abc"} {
		t.Run(valor, func(t *testing.T) {
			t.Setenv("VERA_STT_TIMEOUT", valor)
			_, errs := cargarConfigVera()
			if len(errs) == 0 || !strings.Contains(errs[0].Error(), "VERA_STT_TIMEOUT") {
				t.Fatalf("errores = %v, quiero uno de VERA_STT_TIMEOUT", errs)
			}
		})
	}
}

func TestConfigVeraSTTNoFiltraToken(t *testing.T) {
	var b bytes.Buffer
	cfg := Config{Vera: ConfigVera{Proveedor: "local", Modelo: "gemma4-e4b-local", STTURL: "http://127.0.0.1:8777", STTToken: "secreto-stt-que-no-se-imprime"}}
	slog.New(slog.NewJSONHandler(&b, nil)).Info("config", "config", cfg)
	if strings.Contains(b.String(), cfg.Vera.STTToken) || !strings.Contains(b.String(), `"stt_token_set":true`) {
		t.Fatalf("log inseguro: %s", b.String())
	}
}
