package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config reúne el entorno con el que arranca el simulador del riel.
type Config struct {
	Addr               string
	Port               string
	DBPath             string
	Origen             string
	Perillas           Perillas
	PermitirExposicion bool
}

// LoadConfig valida el entorno y reporta todos los errores del boot juntos.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		Addr:               envOr("SIMULADOR_RIEL_ADDR", "127.0.0.1"),
		Port:               envOr("SIMULADOR_RIEL_PORT", "8098"),
		DBPath:             envOr("SIMULADOR_RIEL_DB", "./simulador-riel.db"),
		Origen:             envOr("SIMULADOR_RIEL_ORIGEN", "simulador-riel"),
		PermitirExposicion: os.Getenv("SIMULADOR_RIEL_PERMITIR_EXPOSICION") == "true",
	}

	var errs []error
	if p, err := strconv.Atoi(cfg.Port); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("SIMULADOR_RIEL_PORT must be 1..65535, got %q", cfg.Port))
	}
	errs = append(errs, validarAddr(cfg.Addr, cfg.PermitirExposicion)...)
	if strings.TrimSpace(cfg.Origen) == "" {
		errs = append(errs, errors.New("SIMULADOR_RIEL_ORIGEN must not be empty"))
	}
	perillas, errPerillas := leerPerillasDelEntorno()
	errs = append(errs, errPerillas...)
	cfg.Perillas = perillas
	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// leerPerillasDelEntorno lee las cinco perillas y reporta todos sus errores juntos.
func leerPerillasDelEntorno() (Perillas, []error) {
	var errs []error
	p := Perillas{ModoCaida: ModoCaida(envOr("SIMULADOR_RIEL_MODO_CAIDA", string(ModoCaidaNinguna)))}
	var err error
	if p.PRechazo, err = probabilidadOr("SIMULADOR_RIEL_P_RECHAZO", "0"); err != nil {
		errs = append(errs, err)
	}
	if p.PRespuestaPerdida, err = probabilidadOr("SIMULADOR_RIEL_P_RESPUESTA_PERDIDA", "0"); err != nil {
		errs = append(errs, err)
	}
	if p.Latencia, err = duracionNoNegativaOr("SIMULADOR_RIEL_LATENCIA", "0s"); err != nil {
		errs = append(errs, err)
	}
	if p.Variacion, err = duracionNoNegativaOr("SIMULADOR_RIEL_LATENCIA_VARIACION", "0s"); err != nil {
		errs = append(errs, err)
	}
	if err := p.Valida(); err != nil {
		errs = append(errs, err)
	}
	return p, errs
}

// LogValue imprime qué variables resolvieron y nunca la ruta de la base.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("addr", c.Addr),
		slog.String("port", c.Port),
		slog.Bool("db_path_set", c.DBPath != ""),
		slog.String("origen", c.Origen),
		slog.Any("perillas", c.Perillas),
		slog.Bool("permitir_exposicion", c.PermitirExposicion),
	)
}

// validarAddr exige loopback salvo habilitación explícita, porque las órdenes no van autenticadas.
func validarAddr(addr string, permitirExposicion bool) []error {
	if permitirExposicion || esLoopback(addr) {
		return nil
	}
	return []error{fmt.Errorf("SIMULADOR_RIEL_ADDR must be a loopback address (127.0.0.1, ::1 or localhost); set SIMULADOR_RIEL_PERMITIR_EXPOSICION=true to expose it explicitly")}
}

// esLoopback acepta las direcciones de loopback IPv4 e IPv6 y el nombre localhost.
func esLoopback(addr string) bool {
	if strings.EqualFold(addr, "localhost") {
		return true
	}
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

// probabilidadOr exige una probabilidad numérica dentro de [0,1].
func probabilidadOr(clave, porDefecto string) (float64, error) {
	v := envOr(clave, porDefecto)
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 1 {
		return 0, fmt.Errorf("%s must be a number in [0,1], got %q", clave, v)
	}
	return f, nil
}

// duracionNoNegativaOr exige una duración no negativa, porque la latencia cero es válida.
func duracionNoNegativaOr(clave, porDefecto string) (time.Duration, error) {
	v := envOr(clave, porDefecto)
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%s must be a non-negative duration (e.g. 0s, 100ms), got %q", clave, v)
	}
	return d, nil
}

func envOr(clave, porDefecto string) string {
	if v := os.Getenv(clave); v != "" {
		return v
	}
	return porDefecto
}

var _ slog.LogValuer = Config{}
