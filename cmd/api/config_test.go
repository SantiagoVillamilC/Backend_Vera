package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfigLogValueNoImprimeSecretos(t *testing.T) {
	t.Parallel()

	cfg := Config{
		Port:           "8080",
		AppDatabaseURL: "postgres://nucleo:dsn-secreto@localhost:5432/centinela",
		JWTSecret:      "jwt-secreto-de-al-menos-treinta-y-dos-bytes",
		CustodioToken:  "custodio-secreto-de-al-menos-treinta-y-dos",
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
		"CustodioToken":  cfg.CustodioToken,
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
	if grupo["app_database_url_set"] != true || grupo["jwt_secret_set"] != true || grupo["custodio_token_set"] != true {
		t.Errorf("faltan los flags _set de las credenciales: %v", grupo)
	}
}

func TestLoadConfigExigeElTokenDelCustodio(t *testing.T) {
	const jwt = "jwt-secreto-de-al-menos-treinta-y-dos-bytes"

	tests := map[string]struct {
		token   string
		wantErr string
	}{
		"valido":              {token: strings.Repeat("a", 32)},
		"vacio":               {token: "", wantErr: "CUSTODIO_TOKEN is required"},
		"corto":               {token: strings.Repeat("a", 31), wantErr: "CUSTODIO_TOKEN must be at least 32 bytes"},
		"igual al jwt secret": {token: jwt, wantErr: "CUSTODIO_TOKEN must differ from JWT_SECRET"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
			t.Setenv("JWT_SECRET", jwt)
			t.Setenv("CUSTODIO_TOKEN", tc.token)

			cfg, err := LoadConfig()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("LoadConfig con token válido: %v", err)
				}
				if cfg.CustodioToken != tc.token {
					t.Errorf("CustodioToken = %q, quiero %q", cfg.CustodioToken, tc.token)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LoadConfig error = %v, quiero que contenga %q", err, tc.wantErr)
			}
		})
	}
}

// NF-A1-07.3: TTL_INTENCION por defecto en 60s y el apagado con vacío, 0 o 0s.
func TestLoadConfigTTLIntencion(t *testing.T) {
	const jwt = "jwt-secreto-de-al-menos-treinta-y-dos-bytes"

	tests := map[string]struct {
		valor   string
		omitir  bool
		quiero  time.Duration
		wantErr bool
	}{
		"por defecto":               {omitir: true, quiero: 60 * time.Second},
		"plazo propio":              {valor: "30s", quiero: 30 * time.Second},
		"apagado con cero":          {valor: "0", quiero: 0},
		"apagado con cero duracion": {valor: "0s", quiero: 0},
		"apagado vacio":             {valor: "", quiero: 0},
		"invalido":                  {valor: "pronto", wantErr: true},
		"negativo":                  {valor: "-1s", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
			t.Setenv("JWT_SECRET", jwt)
			t.Setenv("CUSTODIO_TOKEN", strings.Repeat("a", 32))
			if tc.omitir {
				t.Setenv("TTL_INTENCION", "")
				if err := os.Unsetenv("TTL_INTENCION"); err != nil {
					t.Fatalf("unset: %v", err)
				}
			} else {
				t.Setenv("TTL_INTENCION", tc.valor)
			}

			cfg, err := LoadConfig()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "TTL_INTENCION") {
					t.Fatalf("LoadConfig error = %v, quiero que contenga TTL_INTENCION", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.ExpirationTTL != tc.quiero {
				t.Errorf("ExpirationTTL = %v, quiero %v", cfg.ExpirationTTL, tc.quiero)
			}
		})
	}
}

// NF-A1-07.3: EXPIRACION_INTERVAL por defecto en 60s y siempre positiva, como las demás cadencias.
func TestLoadConfigExpiracionInterval(t *testing.T) {
	const jwt = "jwt-secreto-de-al-menos-treinta-y-dos-bytes"

	tests := map[string]struct {
		valor   string
		omitir  bool
		quiero  time.Duration
		wantErr bool
	}{
		"por defecto": {omitir: true, quiero: 60 * time.Second},
		"propia":      {valor: "15s", quiero: 15 * time.Second},
		"cero":        {valor: "0s", wantErr: true},
		"invalida":    {valor: "ya", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
			t.Setenv("JWT_SECRET", jwt)
			t.Setenv("CUSTODIO_TOKEN", strings.Repeat("a", 32))
			if tc.omitir {
				t.Setenv("EXPIRACION_INTERVAL", "")
				if err := os.Unsetenv("EXPIRACION_INTERVAL"); err != nil {
					t.Fatalf("unset: %v", err)
				}
			} else {
				t.Setenv("EXPIRACION_INTERVAL", tc.valor)
			}

			cfg, err := LoadConfig()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "EXPIRACION_INTERVAL") {
					t.Fatalf("LoadConfig error = %v, quiero que contenga EXPIRACION_INTERVAL", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.ExpirationInterval != tc.quiero {
				t.Errorf("ExpirationInterval = %v, quiero %v", cfg.ExpirationInterval, tc.quiero)
			}
		})
	}
}
