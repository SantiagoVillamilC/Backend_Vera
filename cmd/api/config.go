package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
)

// Config reúne los valores de entorno que el proceso necesita para arrancar.
type Config struct {
	Port                   string
	AppDatabaseURL         string
	JWTSecret              string
	CustodioToken          string
	CustodioExtractoURL    string
	RielSimuladorURL       string
	DirectorioSimuladorURL string
	GroqAPIKey             string
	OpenAIAPIKey           string
	OCRWorkerURL           string
	LogLevel               string
	LogFormat              string
	OutboxInterval         time.Duration
	OutboxBackoffBase      time.Duration
	OutboxBackoffFactor    float64
	OutboxBackoffMax       time.Duration
	ConciliationInterval   time.Duration
	ConciliationWindow     time.Duration
	ExpirationInterval     time.Duration
	ExpirationTTL          time.Duration
	A6PosicionLiquidacion  nucleo.Centavos
	A6PrefondeoActivo      bool
	Auth                   ConfigAuth
}

// LoadConfig lee el entorno, valida la forma de cada variable y reporta todos los errores
// juntos para no obligar a un despliegue por cada variable mal puesta.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:           envOr("PORT", "8080"),
		AppDatabaseURL: os.Getenv("APP_DATABASE_URL"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		GroqAPIKey:     os.Getenv("GROQ_API_KEY"),
		OpenAIAPIKey:   os.Getenv("OPENAI_API_KEY"),
		OCRWorkerURL:   os.Getenv("OCR_WORKER_URL"),
		LogLevel:       envOr("LOG_LEVEL", "info"),
		LogFormat:      envOr("LOG_FORMAT", "json"),
	}

	var errs []error
	var errsAuth []error
	cfg.Auth, errsAuth = cargarConfigAuth()
	errs = append(errs, errsAuth...)
	required := []struct{ name, value string }{
		{"APP_DATABASE_URL", cfg.AppDatabaseURL},
		{"JWT_SECRET", cfg.JWTSecret},
	}
	for _, r := range required {
		if r.value == "" {
			errs = append(errs, fmt.Errorf("%s is required", r.name))
		}
	}
	if p, err := strconv.Atoi(cfg.Port); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be 1..65535, got %q", cfg.Port))
	}
	if cfg.JWTSecret != "" && len(cfg.JWTSecret) < 32 {
		errs = append(errs, fmt.Errorf("JWT_SECRET must be at least 32 bytes for HS256, got %d", len(cfg.JWTSecret)))
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", cfg.LogLevel))
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", cfg.LogFormat))
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// LogValue hace que cualquier slog call imprima qué variables resolvieron y nunca sus
// valores, para que un log accidental no filtre el DSN ni el JWT_SECRET.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", c.Port),
		slog.Bool("app_database_url_set", c.AppDatabaseURL != ""),
		slog.Bool("jwt_secret_set", c.JWTSecret != ""),
		slog.Bool("custodio_token_set", c.CustodioToken != ""),
		slog.Bool("custodio_extracto_url_set", c.CustodioExtractoURL != ""),
		slog.Bool("redis_url_set", c.Auth.RedisURL != ""),
		slog.Bool("contadores_pepper_set", len(c.Auth.ContadoresPepper) > 0),
		slog.String("otp_canal", c.Auth.OTPCanal),
		slog.String("auth_enfriamiento_cambio_numero", c.Auth.EnfriamientoCambioNumero.String()),
		slog.String("auth_ventana_pendiente", c.Auth.VentanaPendiente.String()),
		slog.String("auth_limpieza_interval", c.Auth.LimpiezaInterval.String()),
		slog.Bool("whatsapp_token_set", c.Auth.WhatsAppToken != ""),
		slog.Bool("riel_simulador_url_set", c.RielSimuladorURL != ""),
		slog.Bool("directorio_simulador_url_set", c.DirectorioSimuladorURL != ""),
		slog.Bool("groq_api_key_set", c.GroqAPIKey != ""),
		slog.Bool("openai_api_key_set", c.OpenAIAPIKey != ""),
		slog.Bool("ocr_worker_url_set", c.OCRWorkerURL != ""),
		slog.String("log_level", c.LogLevel),
		slog.String("log_format", c.LogFormat),
	)
}

var _ slog.LogValuer = Config{}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
