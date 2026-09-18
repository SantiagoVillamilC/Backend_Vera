package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config reúne el entorno con el que arranca el simulador de custodia.
type Config struct {
	Addr               string
	Port               string
	DBPath             string
	Origen             string
	PermitirExposicion bool
}

// LoadConfig valida el entorno y reporta todos los errores del boot juntos.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Addr:               envOr("SIMULADOR_CUSTODIA_ADDR", "127.0.0.1"),
		Port:               envOr("SIMULADOR_CUSTODIA_PORT", "8099"),
		DBPath:             envOr("SIMULADOR_CUSTODIA_DB", "./simulador-custodia.db"),
		Origen:             envOr("SIMULADOR_CUSTODIA_ORIGEN", "simulador-custodia"),
		PermitirExposicion: os.Getenv("SIMULADOR_CUSTODIA_PERMITIR_EXPOSICION") == "true",
	}

	var errs []error
	if p, err := strconv.Atoi(cfg.Port); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("SIMULADOR_CUSTODIA_PORT must be 1..65535, got %q", cfg.Port))
	}
	errs = append(errs, validarAddr(cfg.Addr, cfg.PermitirExposicion)...)
	if strings.TrimSpace(cfg.Origen) == "" {
		errs = append(errs, errors.New("SIMULADOR_CUSTODIA_ORIGEN must not be empty"))
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// LogValue imprime qué variables resolvieron y nunca la ruta de la base.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", c.Addr),
		slog.String("port", c.Port),
		slog.Bool("db_path_set", c.DBPath != ""),
		slog.String("origen", c.Origen),
		slog.Bool("permitir_exposicion", c.PermitirExposicion),
	)
}

// validarAddr exige loopback salvo habilitación explícita, porque las notificaciones no van autenticadas.
func validarAddr(addr string, permitirExposicion bool) []error {
	if permitirExposicion || esLoopback(addr) {
		return nil
	}
	return []error{fmt.Errorf("SIMULADOR_CUSTODIA_ADDR must be a loopback address (127.0.0.1, ::1 or localhost); set SIMULADOR_CUSTODIA_PERMITIR_EXPOSICION=true to expose it explicitly")}
}

// esLoopback acepta las direcciones de loopback IPv4 e IPv6 y el nombre localhost.
func esLoopback(addr string) bool {
	if strings.EqualFold(addr, "localhost") {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

func envOr(clave, porDefecto string) string {
	if v := os.Getenv(clave); v != "" {
		return v
	}
	return porDefecto
}

var _ slog.LogValuer = Config{}
