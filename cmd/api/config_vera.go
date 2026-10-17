package main

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
)

// ConfigVera selecciona el proveedor sin exponer credenciales en logs.
type ConfigVera struct {
	Proveedor   string
	APIKey      string
	Modelo      string
	Timeout     time.Duration
	Temperatura float32
}

func cargarConfigVera() (ConfigVera, []error) {
	c := ConfigVera{Proveedor: envOr("VERA_PROVEEDOR", "simulado"), APIKey: strings.TrimSpace(os.Getenv("GEMINI_API_KEY")), Modelo: strings.TrimSpace(os.Getenv("VERA_MODELO"))}
	var errs []error
	if c.Proveedor != "simulado" && c.Proveedor != "gemini" {
		errs = append(errs, errors.New("VERA_PROVEEDOR debe ser gemini o simulado"))
	}
	if c.Proveedor == "gemini" {
		if c.APIKey == "" {
			errs = append(errs, errors.New("GEMINI_API_KEY es obligatoria para VERA_PROVEEDOR=gemini"))
		}
		if c.Modelo == "" {
			errs = append(errs, errors.New("VERA_MODELO es obligatorio para VERA_PROVEEDOR=gemini"))
		}
	}
	d, err := time.ParseDuration(envOr("VERA_TIMEOUT_LLAMADA", "10s"))
	if err != nil || d <= 0 || d > 30*time.Second {
		errs = append(errs, errors.New("VERA_TIMEOUT_LLAMADA debe ser mayor que 0 y hasta 30s"))
	} else {
		c.Timeout = d
	}
	t, err := strconv.ParseFloat(envOr("VERA_TEMPERATURA", "0.2"), 32)
	if err != nil || math.IsNaN(t) || t < 0 || t > 1 {
		errs = append(errs, errors.New("VERA_TEMPERATURA debe ser un número entre 0 y 1"))
	} else {
		c.Temperatura = float32(t)
	}
	return c, errs
}

// LogValue omite la clave y sólo indica su presencia.
func (c ConfigVera) LogValue() slog.Value {
	return slog.GroupValue(slog.String("proveedor", c.Proveedor), slog.String("modelo", c.Modelo), slog.Bool("gemini_api_key_set", c.APIKey != ""), slog.Duration("timeout", c.Timeout), slog.Float64("temperatura", float64(c.Temperatura)))
}

func nuevoProveedorIA(ctx context.Context, c ConfigVera) (copilot.ProveedorIA, error) {
	switch c.Proveedor {
	case "simulado":
		return provsim.Nuevo(), nil
	case "gemini":
		return gemini.Nuevo(ctx, gemini.Config{APIKey: c.APIKey, Modelo: c.Modelo, Timeout: c.Timeout, Temperatura: c.Temperatura}, slog.Default())
	default:
		return nil, copilot.ErrSolicitudIA
	}
}
