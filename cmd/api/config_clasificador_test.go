package main

import (
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
)

func TestLoadConfigClasificadorPorDefectoApagado(t *testing.T) {
	baseDeConfig(t)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig sin CLASIFICADOR: %v", err)
	}
	if cfg.Clasificador != inteligencia.ModoClasificadorApagado {
		t.Fatalf("clasificador = %q, want %q", cfg.Clasificador, inteligencia.ModoClasificadorApagado)
	}
	modelo, err := modeloDelClasificador(cfg)
	if err != nil {
		t.Fatalf("modeloDelClasificador apagado: %v", err)
	}
	if modelo != nil {
		t.Fatalf("el modo apagado cargó un modelo")
	}
}

func TestLoadConfigClasificadorTFIDF(t *testing.T) {
	baseDeConfig(t)
	t.Setenv("CLASIFICADOR", "tfidf")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig con CLASIFICADOR=tfidf: %v", err)
	}
	if cfg.Clasificador != inteligencia.ModoClasificadorTFIDF {
		t.Fatalf("clasificador = %q", cfg.Clasificador)
	}
	modelo, err := modeloDelClasificador(cfg)
	if err != nil {
		t.Fatalf("modeloDelClasificador: %v", err)
	}
	if modelo == nil || modelo.Huella != inteligencia.HuellaArtefactoTFIDF {
		t.Fatalf("modelo = %+v", modelo)
	}
}

func TestLoadConfigRechazaUnModoDesconocido(t *testing.T) {
	for _, valor := range []string{"tfdif", "TF-IDF", "llm"} {
		t.Run(valor, func(t *testing.T) {
			baseDeConfig(t)
			t.Setenv("CLASIFICADOR", valor)
			if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "CLASIFICADOR") {
				t.Fatalf("err = %v, want un error de CLASIFICADOR", err)
			}
		})
	}
}

func TestConfigLogValueRegistraElClasificador(t *testing.T) {
	cfg := Config{Clasificador: inteligencia.ModoClasificadorTFIDF, LogLevel: "info", LogFormat: "json"}
	var encontrado bool
	for _, atributo := range cfg.LogValue().Group() {
		if atributo.Key == "clasificador" {
			encontrado = true
		}
	}
	if !encontrado {
		t.Fatalf("el log del arranque no registra el clasificador")
	}
}
