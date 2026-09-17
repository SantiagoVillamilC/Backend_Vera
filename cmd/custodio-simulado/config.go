package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config reúne el entorno con el que arranca el despachador del custodio simulado.
type Config struct {
	Addr                  string
	Port                  string
	DBPath                string
	NucleoConfirmacionURL string
	CustodioToken         string
	SimuladorCustodiaURL  string
	PermitirExposicion    bool
}

// LoadConfig valida el entorno y reporta todos los errores del boot juntos.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Addr:                  envOr("CUSTODIO_ADDR", "127.0.0.1"),
		Port:                  envOr("CUSTODIO_PORT", "8090"),
		DBPath:                os.Getenv("CUSTODIO_DB"),
		NucleoConfirmacionURL: os.Getenv("NUCLEO_CONFIRMACION_URL"),
		CustodioToken:         os.Getenv("CUSTODIO_TOKEN"),
		SimuladorCustodiaURL:  os.Getenv("SIMULADOR_CUSTODIA_URL"),
		PermitirExposicion:    os.Getenv("CUSTODIO_PERMITIR_EXPOSICION") == "true",
	}

	var errs []error
	obligatorias := []struct{ nombre, valor string }{
		{"CUSTODIO_DB", cfg.DBPath},
		{"NUCLEO_CONFIRMACION_URL", cfg.NucleoConfirmacionURL},
		{"CUSTODIO_TOKEN", cfg.CustodioToken},
		{"SIMULADOR_CUSTODIA_URL", cfg.SimuladorCustodiaURL},
	}
	for _, v := range obligatorias {
		if v.valor == "" {
			errs = append(errs, fmt.Errorf("%s is required", v.nombre))
		}
	}
	if p, err := strconv.Atoi(cfg.Port); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("CUSTODIO_PORT must be 1..65535, got %q", cfg.Port))
	}
	errs = append(errs, validarAddr(cfg.Addr, cfg.PermitirExposicion)...)
	if cfg.CustodioToken != "" && len(cfg.CustodioToken) < 32 {
		errs = append(errs, fmt.Errorf("CUSTODIO_TOKEN must be at least 32 bytes, got %d", len(cfg.CustodioToken)))
	}
	errs = append(errs, urlObligatoria("NUCLEO_CONFIRMACION_URL", cfg.NucleoConfirmacionURL)...)
	errs = append(errs, urlObligatoria("SIMULADOR_CUSTODIA_URL", cfg.SimuladorCustodiaURL)...)
	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// LogValue imprime qué variables resolvieron y nunca el token, la ruta de la base ni las credenciales de la URL.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", c.Addr),
		slog.String("port", c.Port),
		slog.Bool("db_path_set", c.DBPath != ""),
		slog.String("nucleo_confirmacion_url", redactarURL(c.NucleoConfirmacionURL)),
		slog.String("simulador_custodia_url", redactarURL(c.SimuladorCustodiaURL)),
		slog.Bool("custodio_token_set", c.CustodioToken != ""),
		slog.Bool("permitir_exposicion", c.PermitirExposicion),
	)
}

// validarAddr exige loopback salvo habilitación explícita, porque el PSE confirma depósitos sin autenticar.
func validarAddr(addr string, permitirExposicion bool) []error {
	if permitirExposicion || esLoopback(addr) {
		return nil
	}
	return []error{fmt.Errorf("CUSTODIO_ADDR must be a loopback address (127.0.0.1, ::1 or localhost) because the PSE simulator has no authentication; set CUSTODIO_PERMITIR_EXPOSICION=true to expose it explicitly")}
}

// esLoopback acepta las direcciones de loopback IPv4 e IPv6 y el nombre localhost.
func esLoopback(addr string) bool {
	if strings.EqualFold(addr, "localhost") {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

var _ slog.LogValuer = Config{}

// redactarURL deja a lo sumo esquema://host, sin userinfo ni query.
func redactarURL(valor string) string {
	u, err := url.Parse(valor)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// urlObligatoria exige una URL http o https absoluta cuando el valor está presente.
func urlObligatoria(nombre, valor string) []error {
	if valor == "" {
		return nil
	}
	u, err := url.Parse(valor)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return []error{fmt.Errorf("%s must be an absolute http(s) URL", nombre)}
	}
	return nil
}

func envOr(clave, porDefecto string) string {
	if v := os.Getenv(clave); v != "" {
		return v
	}
	return porDefecto
}
