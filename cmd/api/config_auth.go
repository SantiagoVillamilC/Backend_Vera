package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// ConfigAuth reúne la configuración del módulo de autenticación: Redis, el canal del OTP, el enfriamiento y la limpieza de pendientes.
type ConfigAuth struct {
	RedisURL                 string
	OTPCanal                 string
	OTPSimuladorCodigoFijo   string
	WhatsAppToken            string
	WhatsAppPhoneID          string
	WhatsAppTemplate         string
	ContadoresPepper         []byte
	OTPPepper                []byte
	JWTIssuer                string
	JWTAudience              string
	ResetTokenTTL            time.Duration
	EnfriamientoCambioNumero time.Duration
	DestinatariosLimite      int
	DestinatariosVentana     time.Duration
	ContactoSoporte          string
	VentanaPendiente         time.Duration
	LimpiezaInterval         time.Duration
	OutboxInterval           time.Duration
	OutboxBackoffBase        time.Duration
	OutboxBackoffFactor      float64
	OutboxBackoffMax         time.Duration
	Argon2Concurrencia       int
	KYCProveedor             string
	KYCResultado             string
}

// cargarConfigAuth lee el bloque de auth y devuelve sus errores para el errors.Join del llamador.
func cargarConfigAuth() (ConfigAuth, []error) {
	cfg := ConfigAuth{
		RedisURL:               os.Getenv("REDIS_URL"),
		OTPCanal:               os.Getenv("OTP_CANAL"),
		OTPSimuladorCodigoFijo: os.Getenv("OTP_SIMULADOR_CODIGO_FIJO"),
		WhatsAppToken:          os.Getenv("WHATSAPP_TOKEN"),
		WhatsAppPhoneID:        os.Getenv("WHATSAPP_PHONE_ID"),
		WhatsAppTemplate:       os.Getenv("WHATSAPP_TEMPLATE"),
		JWTIssuer:              os.Getenv("JWT_ISSUER"),
		JWTAudience:            os.Getenv("JWT_AUDIENCE"),
		ContactoSoporte:        os.Getenv("AUTH_CONTACTO_SOPORTE"),
	}

	var errs []error
	if err := validarRedisURL(cfg.RedisURL); err != nil {
		errs = append(errs, err)
	}
	if cfg.JWTIssuer == "" {
		errs = append(errs, errors.New("JWT_ISSUER is required"))
	}
	if cfg.JWTAudience == "" {
		errs = append(errs, errors.New("JWT_AUDIENCE is required"))
	}
	pepper, errPepper := pepperRequerido("CONTADORES_PEPPER", os.Getenv("CONTADORES_PEPPER"))
	if errPepper != nil {
		errs = append(errs, errPepper)
	}
	cfg.ContadoresPepper = pepper
	pepperOTP, errPepperOTP := pepperRequerido("OTP_PEPPER", os.Getenv("OTP_PEPPER"))
	if errPepperOTP != nil {
		errs = append(errs, errPepperOTP)
	}
	cfg.OTPPepper = pepperOTP
	if len(cfg.ContadoresPepper) > 0 && bytes.Equal(cfg.ContadoresPepper, cfg.OTPPepper) {
		errs = append(errs, errors.New("OTP_PEPPER must differ from CONTADORES_PEPPER"))
	}
	var errResetToken error
	cfg.ResetTokenTTL, errResetToken = duracionRequerida("AUTH_RESET_TOKEN_TTL", os.Getenv("AUTH_RESET_TOKEN_TTL"))
	if errResetToken != nil {
		errs = append(errs, errResetToken)
	}
	var errEnfriamiento error
	cfg.EnfriamientoCambioNumero, errEnfriamiento = duracionRequerida("AUTH_ENFRIAMIENTO_CAMBIO_NUMERO", os.Getenv("AUTH_ENFRIAMIENTO_CAMBIO_NUMERO"))
	if errEnfriamiento != nil {
		errs = append(errs, errEnfriamiento)
	}
	var errLimite error
	cfg.DestinatariosLimite, errLimite = enteroPositivoRequerido("DESTINATARIOS_LIMITE", envOr("DESTINATARIOS_LIMITE", "20"))
	if errLimite != nil {
		errs = append(errs, errLimite)
	}
	cfg.DestinatariosVentana, errLimite = duracionRequerida("DESTINATARIOS_VENTANA", envOr("DESTINATARIOS_VENTANA", "1h"))
	if errLimite != nil {
		errs = append(errs, errLimite)
	}
	if cfg.ContactoSoporte == "" {
		errs = append(errs, errors.New("AUTH_CONTACTO_SOPORTE is required"))
	}
	var errVentana error
	cfg.VentanaPendiente, errVentana = duracionRequerida("AUTH_VENTANA_PENDIENTE", os.Getenv("AUTH_VENTANA_PENDIENTE"))
	if errVentana != nil {
		errs = append(errs, errVentana)
	}
	var errLimpieza error
	cfg.LimpiezaInterval, errLimpieza = duracionRequerida("AUTH_LIMPIEZA_INTERVAL", os.Getenv("AUTH_LIMPIEZA_INTERVAL"))
	if errLimpieza != nil {
		errs = append(errs, errLimpieza)
	}
	var errOutbox error
	cfg.OutboxInterval, errOutbox = duracionRequerida("AUTH_OUTBOX_INTERVAL", os.Getenv("AUTH_OUTBOX_INTERVAL"))
	if errOutbox != nil {
		errs = append(errs, errOutbox)
	}
	cfg.OutboxBackoffBase, errOutbox = duracionRequerida("AUTH_OUTBOX_BACKOFF_BASE", os.Getenv("AUTH_OUTBOX_BACKOFF_BASE"))
	if errOutbox != nil {
		errs = append(errs, errOutbox)
	}
	cfg.OutboxBackoffFactor, errOutbox = factorRequerido("AUTH_OUTBOX_BACKOFF_FACTOR", os.Getenv("AUTH_OUTBOX_BACKOFF_FACTOR"))
	if errOutbox != nil {
		errs = append(errs, errOutbox)
	}
	cfg.OutboxBackoffMax, errOutbox = duracionRequerida("AUTH_OUTBOX_BACKOFF_MAX", os.Getenv("AUTH_OUTBOX_BACKOFF_MAX"))
	if errOutbox != nil {
		errs = append(errs, errOutbox)
	}
	var errArgon2 error
	cfg.Argon2Concurrencia, errArgon2 = enteroPositivoRequerido("AUTH_ARGON2_CONCURRENCIA", os.Getenv("AUTH_ARGON2_CONCURRENCIA"))
	if errArgon2 != nil {
		errs = append(errs, errArgon2)
	}
	switch cfg.OTPCanal {
	case "simulador", "demo":
	case "cloudapi":
		for _, r := range []struct{ name, value string }{
			{"WHATSAPP_TOKEN", cfg.WhatsAppToken},
			{"WHATSAPP_PHONE_ID", cfg.WhatsAppPhoneID},
			{"WHATSAPP_TEMPLATE", cfg.WhatsAppTemplate},
		} {
			if r.value == "" {
				errs = append(errs, fmt.Errorf("%s is required when OTP_CANAL=cloudapi", r.name))
			}
		}
	case "":
		errs = append(errs, errors.New("OTP_CANAL is required"))
	default:
		errs = append(errs, fmt.Errorf("OTP_CANAL must be simulador, cloudapi or demo, got %q", cfg.OTPCanal))
	}
	cfg.KYCProveedor = os.Getenv("KYC_PROVEEDOR")
	switch cfg.KYCProveedor {
	case "":
		errs = append(errs, errors.New("KYC_PROVEEDOR is required"))
	case "simulador":
		cfg.KYCResultado = os.Getenv("KYC_SIMULADOR_RESULTADO")
		if cfg.KYCResultado == "" {
			errs = append(errs, errors.New("KYC_SIMULADOR_RESULTADO is required when KYC_PROVEEDOR=simulador"))
		}
	}
	return cfg, errs
}

// validarRedisURL exige una URL redis:// o rediss:// con host e imprime solo el host, nunca la URL.
func validarRedisURL(raw string) error {
	if raw == "" {
		return errors.New("REDIS_URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("REDIS_URL must be a parseable redis:// or rediss:// URL")
	}
	if (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
		return fmt.Errorf("REDIS_URL must be a redis:// or rediss:// URL with a host, got %q", u.Host)
	}
	if _, err := goredis.ParseURL(raw); err != nil {
		return fmt.Errorf("REDIS_URL must be a valid Redis URL, got host %q", u.Host)
	}
	return nil
}

// pepperRequerido decodifica un pepper en hex y exige al menos 32 bytes sin imprimir su valor.
func pepperRequerido(key, valor string) ([]byte, error) {
	if valor == "" {
		return nil, fmt.Errorf("%s is required", key)
	}
	b, err := hex.DecodeString(valor)
	if err != nil {
		return nil, fmt.Errorf("%s must be hex-encoded", key)
	}
	if len(b) < 32 {
		return nil, fmt.Errorf("%s must decode to at least 32 bytes, got %d", key, len(b))
	}
	return b, nil
}

// duracionRequerida exige una duración positiva sin default en código.
func duracionRequerida(key, valor string) (time.Duration, error) {
	if valor == "" {
		return 0, fmt.Errorf("%s is required", key)
	}
	d, err := time.ParseDuration(valor)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration (e.g. 24h), got %q", key, valor)
	}
	return d, nil
}

// enteroPositivoRequerido exige un entero mayor que cero sin default en código.
func enteroPositivoRequerido(key, valor string) (int, error) {
	if valor == "" {
		return 0, fmt.Errorf("%s is required", key)
	}
	n, err := strconv.Atoi(valor)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a positive integer, got %q", key, valor)
	}
	return n, nil
}

// factorRequerido exige un multiplicador finito y mayor que cero sin default en código.
func factorRequerido(key, valor string) (float64, error) {
	if valor == "" {
		return 0, fmt.Errorf("%s is required", key)
	}
	f, err := strconv.ParseFloat(valor, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return 0, fmt.Errorf("%s must be a finite number greater than zero, got %q", key, valor)
	}
	return f, nil
}
