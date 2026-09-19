package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
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
	EnfriamientoCambioNumero time.Duration
	VentanaPendiente         time.Duration
	LimpiezaInterval         time.Duration
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
	}

	var errs []error
	if err := validarRedisURL(cfg.RedisURL); err != nil {
		errs = append(errs, err)
	}
	pepper, errPepper := pepperRequerido("CONTADORES_PEPPER", os.Getenv("CONTADORES_PEPPER"))
	if errPepper != nil {
		errs = append(errs, errPepper)
	}
	cfg.ContadoresPepper = pepper
	var errEnfriamiento error
	cfg.EnfriamientoCambioNumero, errEnfriamiento = duracionRequerida("AUTH_ENFRIAMIENTO_CAMBIO_NUMERO", os.Getenv("AUTH_ENFRIAMIENTO_CAMBIO_NUMERO"))
	if errEnfriamiento != nil {
		errs = append(errs, errEnfriamiento)
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
