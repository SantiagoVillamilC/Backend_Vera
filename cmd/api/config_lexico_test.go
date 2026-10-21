package main

import (
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
)

// TestLoadConfigLexicoPorDefectoApagado fija que el escalón 5 no se enciende solo.
func TestLoadConfigLexicoPorDefectoApagado(t *testing.T) {
	baseDeConfig(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig sin LEXICO: %v", err)
	}
	if cfg.Lexico != inteligencia.ModoLexicoApagado {
		t.Fatalf("léxico = %q, want %q", cfg.Lexico, inteligencia.ModoLexicoApagado)
	}
}

// TestLoadConfigLexicoV2 acepta el único artefacto embebido.
func TestLoadConfigLexicoV2(t *testing.T) {
	baseDeConfig(t)
	t.Setenv("LEXICO", "v2")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig con LEXICO=v2: %v", err)
	}
	if cfg.Lexico != inteligencia.ModoLexicoV2 {
		t.Fatalf("léxico = %q, want %q", cfg.Lexico, inteligencia.ModoLexicoV2)
	}
}

// TestLoadConfigRechazaUnLexicoDesconocido deja fuera un v3 que no está embebido.
func TestLoadConfigRechazaUnLexicoDesconocido(t *testing.T) {
	baseDeConfig(t)
	t.Setenv("LEXICO", "v3")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "LEXICO") {
		t.Fatalf("se esperaba error de LEXICO, recibí %v", err)
	}
}

// TestConfigLogValueRegistraElLexico comprueba que el arranque deja rastro del modo.
func TestConfigLogValueRegistraElLexico(t *testing.T) {
	cfg := Config{Lexico: inteligencia.ModoLexicoV2, LogLevel: "info", LogFormat: "json"}
	for _, atributo := range cfg.LogValue().Group() {
		if atributo.Key == "lexico" {
			return
		}
	}
	t.Fatal("el log del arranque no registra el léxico")
}
