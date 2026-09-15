package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestConfigLogValueNoImprimeSecretos(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Port:           "8080",
		AppDatabaseURL: "postgres://nucleo:dsn-secreto@localhost:5432/centinela",
		JWTSecret:      "jwt-secreto-de-al-menos-treinta-y-dos-bytes",
		GroqAPIKey:     "groq-secreta",
		OpenAIAPIKey:   "openai-secreta",
		OCRWorkerURL:   "http://usuario:clave-secreta@ocr",
		LogLevel:       "info",
		LogFormat:      "json",
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)

	salida := buf.String()
	for nombre, secreto := range map[string]string{
		"AppDatabaseURL": cfg.AppDatabaseURL,
		"JWTSecret":      cfg.JWTSecret,
		"GroqAPIKey":     cfg.GroqAPIKey,
		"OpenAIAPIKey":   cfg.OpenAIAPIKey,
		"OCRWorkerURL":   cfg.OCRWorkerURL,
	} {
		if strings.Contains(salida, secreto) {
			t.Errorf("%s se filtró en el log: %s", nombre, salida)
		}
	}

	var registro map[string]any
	if err := json.Unmarshal(buf.Bytes(), &registro); err != nil {
		t.Fatalf("registro inválido: %v (%q)", err, salida)
	}
	grupo, ok := registro["config"].(map[string]any)
	if !ok {
		t.Fatalf("config no se agrupó: %s", salida)
	}
	if grupo["app_database_url_set"] != true || grupo["jwt_secret_set"] != true {
		t.Errorf("faltan los flags _set de las credenciales: %v", grupo)
	}
}
