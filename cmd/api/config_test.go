package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
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

// NF-A2-05: la espera creciente del outbox lee base, factor y tope del entorno con sus defaults.
func TestLoadConfigBackoffOutbox(t *testing.T) {
	const jwt = "jwt-secreto-de-al-menos-treinta-y-dos-bytes"

	tests := map[string]struct {
		base    string
		factor  string
		tope    string
		quiero  nucleo.PoliticaReintentoOutbox
		wantErr string
	}{
		"por defecto":     {quiero: nucleo.PoliticaReintentoOutboxPorDefecto()},
		"propios":         {base: "1s", factor: "3", tope: "5m", quiero: nucleo.PoliticaReintentoOutbox{Base: time.Second, Factor: 3, Tope: 5 * time.Minute}},
		"factor uno":      {factor: "1", quiero: nucleo.PoliticaReintentoOutbox{Base: 30 * time.Second, Factor: 1, Tope: 30 * time.Minute}},
		"base invalida":   {base: "ya", wantErr: "OUTBOX_BACKOFF_BASE"},
		"factor cero":     {factor: "0", wantErr: "OUTBOX_BACKOFF_FACTOR"},
		"factor negativo": {factor: "-1", wantErr: "OUTBOX_BACKOFF_FACTOR"},
		"tope invalido":   {tope: "0s", wantErr: "OUTBOX_BACKOFF_MAX"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
			t.Setenv("JWT_SECRET", jwt)
			t.Setenv("CUSTODIO_TOKEN", strings.Repeat("a", 32))
			t.Setenv("OUTBOX_BACKOFF_BASE", tc.base)
			t.Setenv("OUTBOX_BACKOFF_FACTOR", tc.factor)
			t.Setenv("OUTBOX_BACKOFF_MAX", tc.tope)

			cfg, err := LoadConfig()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("LoadConfig error = %v, quiero que contenga %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.OutboxBackoffBase != tc.quiero.Base || cfg.OutboxBackoffFactor != tc.quiero.Factor || cfg.OutboxBackoffMax != tc.quiero.Tope {
				t.Errorf("backoff = (%v, %v, %v), quiero (%v, %v, %v)",
					cfg.OutboxBackoffBase, cfg.OutboxBackoffFactor, cfg.OutboxBackoffMax,
					tc.quiero.Base, tc.quiero.Factor, tc.quiero.Tope)
			}
		})
	}
}

// NF-A6-01: la cifra de posición y el interruptor de A6 cargan del entorno y default apagado.
func TestLoadConfigA6(t *testing.T) {
	const jwt = "jwt-secreto-de-al-menos-treinta-y-dos-bytes"

	tests := map[string]struct {
		posicion string
		activo   string
		quiero   nucleo.Centavos
		activa   bool
		wantErr  string
	}{
		"por defecto":            {},
		"cifra y apagado":        {posicion: "100000000000", activo: "false", quiero: nucleo.Centavos(100000000000)},
		"interruptor encendido":  {posicion: "0", activo: "true", activa: true},
		"cifra malformada":       {posicion: "1000.50", wantErr: "A6_POSICION_LIQUIDACION_CENTAVOS"},
		"interruptor malformado": {activo: "apagado", wantErr: "A6_PREFONDEO_ACTIVO"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
			t.Setenv("JWT_SECRET", jwt)
			t.Setenv("CUSTODIO_TOKEN", strings.Repeat("a", 32))
			t.Setenv("A6_POSICION_LIQUIDACION_CENTAVOS", tc.posicion)
			t.Setenv("A6_PREFONDEO_ACTIVO", tc.activo)

			cfg, err := LoadConfig()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("LoadConfig error = %v, quiero que contenga %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.A6PosicionLiquidacion != tc.quiero {
				t.Errorf("A6PosicionLiquidacion = %d, quiero %d", cfg.A6PosicionLiquidacion, tc.quiero)
			}
			if cfg.A6PrefondeoActivo != tc.activa {
				t.Errorf("A6PrefondeoActivo = %v, quiero %v", cfg.A6PrefondeoActivo, tc.activa)
			}
		})
	}
}

// NF-A6-01: la cifra en centavos y en COP conviven y el default no representa presupuesto.
func TestLoadConfigA6RindeCOP(t *testing.T) {
	const jwt = "jwt-secreto-de-al-menos-treinta-y-dos-bytes"
	t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
	t.Setenv("JWT_SECRET", jwt)
	t.Setenv("CUSTODIO_TOKEN", strings.Repeat("a", 32))
	t.Setenv("A6_POSICION_LIQUIDACION_CENTAVOS", "100000000000")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.A6PosicionLiquidacion.PresentarUnidadesMayores(); got != "1000000000.00" {
		t.Errorf("PresentarUnidadesMayores() = %q, quiero %q", got, "1000000000.00")
	}
}

// NF-A6-01: la comprobación previa a la corrida falla con un error que nombra a A6.
func TestValidarCorridaA6(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		activo  bool
		wantErr bool
	}{
		"apagado":   {},
		"encendido": {activo: true, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := ValidarCorridaA6(&Config{A6PrefondeoActivo: tc.activo})
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidarCorridaA6(activo=%v) error = %v, wantErr %v", tc.activo, err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "A6") {
				t.Errorf("el error debe nombrar a A6: %v", err)
			}
		})
	}
}

// NF-A6-01: el arranque rechaza la corrida con el interruptor encendido antes de tocar Postgres.
func TestRunRechazaA6Encendido(t *testing.T) {
	t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
	t.Setenv("JWT_SECRET", strings.Repeat("j", 32))
	t.Setenv("CUSTODIO_TOKEN", strings.Repeat("c", 32))
	t.Setenv("A6_PREFONDEO_ACTIVO", "true")

	err := run()
	if err == nil || !strings.Contains(err.Error(), "A6") {
		t.Fatalf("run() error = %v, quiero un error que nombre a A6", err)
	}
}

// NF-A6-01: el registro de la corrida deja constancia de la cifra y del interruptor de A6.
func TestConfigLogValueRegistraA6(t *testing.T) {
	t.Parallel()

	cfg := Config{
		A6PosicionLiquidacion: nucleo.Centavos(100000000000),
		A6PrefondeoActivo:     true,
	}

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)

	var registro map[string]any
	if err := json.Unmarshal(buf.Bytes(), &registro); err != nil {
		t.Fatalf("registro inválido: %v (%q)", err, buf.String())
	}
	grupo, ok := registro["config"].(map[string]any)
	if !ok {
		t.Fatalf("config no se agrupó: %s", buf.String())
	}
	if grupo["a6_prefondeo_activo"] != true {
		t.Errorf("a6_prefondeo_activo = %v, quiero true", grupo["a6_prefondeo_activo"])
	}
	if grupo["a6_posicion_liquidacion_centavos"] != "100000000000" {
		t.Errorf("a6_posicion_liquidacion_centavos = %v, quiero 100000000000", grupo["a6_posicion_liquidacion_centavos"])
	}
	if grupo["a6_posicion_liquidacion_cop"] != "1000000000.00" {
		t.Errorf("a6_posicion_liquidacion_cop = %v, quiero 1000000000.00", grupo["a6_posicion_liquidacion_cop"])
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

// NF-A5-01: la URL del extracto del custodio es opcional y se valida como http(s) absoluta.
func TestLoadConfigExtractoDelCustodio(t *testing.T) {
	const jwt = "jwt-secreto-de-al-menos-treinta-y-dos-bytes"

	tests := map[string]struct {
		url     string
		wantErr bool
	}{
		"por defecto vacia": {},
		"http valida":       {url: "http://127.0.0.1:8099/extracto"},
		"relativa":          {url: "/extracto", wantErr: true},
		"esquema ajeno":     {url: "ftp://127.0.0.1/extracto", wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("APP_DATABASE_URL", "postgres://nucleo:dsn@localhost:5432/centinela")
			t.Setenv("JWT_SECRET", jwt)
			t.Setenv("CUSTODIO_TOKEN", strings.Repeat("a", 32))
			t.Setenv("CUSTODIO_EXTRACTO_URL", tc.url)

			cfg, err := LoadConfig()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "CUSTODIO_EXTRACTO_URL") {
					t.Fatalf("LoadConfig error = %v, quiero que contenga CUSTODIO_EXTRACTO_URL", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.CustodioExtractoURL != tc.url {
				t.Errorf("CustodioExtractoURL = %q, quiero %q", cfg.CustodioExtractoURL, tc.url)
			}
		})
	}
}
