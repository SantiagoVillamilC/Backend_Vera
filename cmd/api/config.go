package main

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// Config reúne los valores de entorno que el proceso necesita para arrancar.
type Config struct {
	Port                  string
	AppDatabaseURL        string
	JWTSecret             string
	CustodioToken         string
	CustodioExtractoURL   string
	GroqAPIKey            string
	OpenAIAPIKey          string
	OCRWorkerURL          string
	LogLevel              string
	LogFormat             string
	OutboxInterval        time.Duration
	OutboxBackoffBase     time.Duration
	OutboxBackoffFactor   float64
	OutboxBackoffMax      time.Duration
	ConciliationInterval  time.Duration
	ConciliationWindow    time.Duration
	ExpirationInterval    time.Duration
	ExpirationTTL         time.Duration
	A6PosicionLiquidacion nucleo.Centavos
	A6PrefondeoActivo     bool
}

// LoadConfig valida el entorno y reporta todos los errores juntos.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:                envOr("PORT", "8080"),
		AppDatabaseURL:      os.Getenv("APP_DATABASE_URL"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		CustodioToken:       os.Getenv("CUSTODIO_TOKEN"),
		CustodioExtractoURL: os.Getenv("CUSTODIO_EXTRACTO_URL"),
		GroqAPIKey:          os.Getenv("GROQ_API_KEY"),
		OpenAIAPIKey:        os.Getenv("OPENAI_API_KEY"),
		OCRWorkerURL:        os.Getenv("OCR_WORKER_URL"),
		LogLevel:            envOr("LOG_LEVEL", "info"),
		LogFormat:           envOr("LOG_FORMAT", "json"),
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
	if err := urlOpcional("CUSTODIO_EXTRACTO_URL", cfg.CustodioExtractoURL); err != nil {
		errs = append(errs, err)
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
	cfg.OutboxBackoffBase, errDuracion = durationOr("OUTBOX_BACKOFF_BASE", "30s")
	if errDuracion != nil {
		errs = append(errs, errDuracion)
	}
	var errFactor error
	cfg.OutboxBackoffFactor, errFactor = factorOr("OUTBOX_BACKOFF_FACTOR", "2")
	if errFactor != nil {
		errs = append(errs, errFactor)
	}
	cfg.OutboxBackoffMax, errDuracion = durationOr("OUTBOX_BACKOFF_MAX", "30m")
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
	var errA6 error
	cfg.A6PosicionLiquidacion, errA6 = centavosOr("A6_POSICION_LIQUIDACION_CENTAVOS", "0")
	if errA6 != nil {
		errs = append(errs, errA6)
	}
	cfg.A6PrefondeoActivo, errA6 = boolOr("A6_PREFONDEO_ACTIVO", "false")
	if errA6 != nil {
		errs = append(errs, errA6)
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// ValidarCorridaA6 exige el interruptor apagado: encendido contamina la medición con otra causa.
func ValidarCorridaA6(cfg *Config) error {
	if cfg.A6PrefondeoActivo {
		return errors.New("A6: el prefondeo se asume infinito durante la corrida, apaga A6_PREFONDEO_ACTIVO")
	}
	return nil
}

// LogValue imprime qué variables resolvieron y nunca sus valores.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("port", c.Port),
		slog.Bool("app_database_url_set", c.AppDatabaseURL != ""),
		slog.Bool("jwt_secret_set", c.JWTSecret != ""),
		slog.Bool("custodio_token_set", c.CustodioToken != ""),
		slog.Bool("custodio_extracto_url_set", c.CustodioExtractoURL != ""),
		slog.Bool("groq_api_key_set", c.GroqAPIKey != ""),
		slog.Bool("openai_api_key_set", c.OpenAIAPIKey != ""),
		slog.Bool("ocr_worker_url_set", c.OCRWorkerURL != ""),
		slog.String("log_level", c.LogLevel),
		slog.String("log_format", c.LogFormat),
		slog.String("outbox_interval", c.OutboxInterval.String()),
		slog.String("outbox_backoff_base", c.OutboxBackoffBase.String()),
		slog.String("outbox_backoff_factor", strconv.FormatFloat(c.OutboxBackoffFactor, 'g', -1, 64)),
		slog.String("outbox_backoff_max", c.OutboxBackoffMax.String()),
		slog.String("conciliation_interval", c.ConciliationInterval.String()),
		slog.String("conciliation_window", c.ConciliationWindow.String()),
		slog.String("expiration_ttl", c.ExpirationTTL.String()),
		slog.String("expiration_interval", c.ExpirationInterval.String()),
		slog.Bool("a6_prefondeo_activo", c.A6PrefondeoActivo),
		slog.String("a6_posicion_liquidacion_centavos", strconv.FormatInt(c.A6PosicionLiquidacion.Int64(), 10)),
		slog.String("a6_posicion_liquidacion_cop", c.A6PosicionLiquidacion.PresentarUnidadesMayores()),
	)
}

var _ slog.LogValuer = Config{}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// urlOpcional valida una URL http(s) absoluta solo cuando la variable trae un valor.
func urlOpcional(key, valor string) error {
	if valor == "" {
		return nil
	}
	u, err := url.Parse(valor)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be an absolute http(s) URL, got %q", key, valor)
	}
	return nil
}

// centavosOr parsea una cifra de dinero en unidades menores y nombra la variable si no es entera.
func centavosOr(key, fallback string) (nucleo.Centavos, error) {
	v := envOr(key, fallback)
	c, err := nucleo.ParsearCentavos(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer number of minor units (e.g. 0, 100000), got %q", key, v)
	}
	return c, nil
}

// boolOr parsea un interruptor y nombra la variable si no es true ni false.
func boolOr(key, fallback string) (bool, error) {
	v := envOr(key, fallback)
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false, got %q", key, v)
	}
	return b, nil
}

// factorOr exige un multiplicador finito y mayor que cero para la espera creciente.
func factorOr(key, fallback string) (float64, error) {
	v := envOr(key, fallback)
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return 0, fmt.Errorf("%s must be a finite number greater than zero, got %q", key, v)
	}
	return f, nil
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
