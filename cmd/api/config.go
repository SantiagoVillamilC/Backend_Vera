package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Config reúne los valores de entorno que el proceso necesita para arrancar.
type Config struct {
	Port                 string
	AppDatabaseURL       string
	JWTSecret            string
	CustodioToken        string
	GroqAPIKey           string
	OpenAIAPIKey         string
	OCRWorkerURL         string
	LogLevel             string
	LogFormat            string
	OutboxInterval       time.Duration
	ConciliationInterval time.Duration
	ConciliationWindow   time.Duration
	ExpirationInterval   time.Duration
	ExpirationTTL        time.Duration
}

// LoadConfig valida el entorno y reporta todos los errores juntos.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:           envOr("PORT", "8080"),
		AppDatabaseURL: os.Getenv("APP_DATABASE_URL"),
		JWTSecret:      os.Getenv("JWT_SECRET"),
		CustodioToken:  os.Getenv("CUSTODIO_TOKEN"),
		GroqAPIKey:     os.Getenv("GROQ_API_KEY"),
		OpenAIAPIKey:   os.Getenv("OPENAI_API_KEY"),
		OCRWorkerURL:   os.Getenv("OCR_WORKER_URL"),
		LogLevel:       envOr("LOG_LEVEL", "info"),
		LogFormat:      envOr("LOG_FORMAT", "json"),
	}

	var errs []error
	required := []struct{ name, value string }{
		{"APP_DATABASE_URL", cfg.AppDatabaseURL},
		{"JWT_SECRET", cfg.JWTSecret},
		{"CUSTODIO_TOKEN", cfg.CustodioToken},
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
	if cfg.CustodioToken != "" && len(cfg.CustodioToken) < 32 {
		errs = append(errs, fmt.Errorf("CUSTODIO_TOKEN must be at least 32 bytes, got %d", len(cfg.CustodioToken)))
	}
	if cfg.CustodioToken != "" && cfg.CustodioToken == cfg.JWTSecret {
		errs = append(errs, fmt.Errorf("CUSTODIO_TOKEN must differ from JWT_SECRET"))
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", cfg.LogLevel))
	}
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", cfg.LogFormat))
	}
	var errDuracion error
	cfg.OutboxInterval, errDuracion = durationOr("OUTBOX_INTERVAL", "5s")
	if errDuracion != nil {
		errs = append(errs, errDuracion)
	}
	cfg.ConciliationInterval, errDuracion = durationOr("CONCILIATION_INTERVAL", "60s")
	if errDuracion != nil {
		errs = append(errs, errDuracion)
	}
	cfg.ConciliationWindow, errDuracion = durationOr("CONCILIATION_WINDOW", "5m")
	if errDuracion != nil {
		errs = append(errs, errDuracion)
	}
	cfg.ExpirationTTL, errDuracion = ttlOr("TTL_INTENCION", "60s")
	if errDuracion != nil {
		errs = append(errs, errDuracion)
	}
	cfg.ExpirationInterval, errDuracion = durationOr("EXPIRACION_INTERVAL", "60s")
	if errDuracion != nil {
		errs = append(errs, errDuracion)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// LogValue imprime qué variables resolvieron y nunca sus valores.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", c.Port),
		slog.Bool("app_database_url_set", c.AppDatabaseURL != ""),
		slog.Bool("jwt_secret_set", c.JWTSecret != ""),
		slog.Bool("custodio_token_set", c.CustodioToken != ""),
		slog.Bool("groq_api_key_set", c.GroqAPIKey != ""),
		slog.Bool("openai_api_key_set", c.OpenAIAPIKey != ""),
		slog.Bool("ocr_worker_url_set", c.OCRWorkerURL != ""),
		slog.String("log_level", c.LogLevel),
		slog.String("log_format", c.LogFormat),
		slog.String("outbox_interval", c.OutboxInterval.String()),
		slog.String("conciliation_interval", c.ConciliationInterval.String()),
		slog.String("conciliation_window", c.ConciliationWindow.String()),
		slog.String("expiration_ttl", c.ExpirationTTL.String()),
		slog.String("expiration_interval", c.ExpirationInterval.String()),
	)
}

var _ slog.LogValuer = Config{}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// durationOr exige una duración positiva para que una cadencia mal puesta detenga el arranque.
func durationOr(key, fallback string) (time.Duration, error) {
	v := envOr(key, fallback)
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration (e.g. 5s, 1m), got %q", key, v)
	}
	return d, nil
}

// ttlOr deja apagar el proceso con cero o vacío y usa el default solo cuando la variable no existe.
func ttlOr(key, fallback string) (time.Duration, error) {
	v, presente := os.LookupEnv(key)
	if !presente {
		v = fallback
	}
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s must be zero or a positive duration (e.g. 60s, 0 to disable), got %q", key, v)
	}
	return d, nil
}
