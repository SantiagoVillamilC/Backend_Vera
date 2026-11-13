package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/compuesto"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/ollama"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/sttlocal"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// ConfigVera selecciona el proveedor sin exponer credenciales en logs.
type ConfigVera struct {
	Proveedor        string
	APIKey           string
	Modelo           string
	OllamaURL        string
	STTURL           string
	STTToken         string
	STTPlazo         time.Duration
	Timeout          time.Duration
	PlazoOrquestador time.Duration
	Temperatura      float32
	Limites          copilot.LimitesUso
	Preparacion      copilot.ConfigPreparacion
	RetencionDias    int
}

func cargarConfigVera() (ConfigVera, []error) {
	c := ConfigVera{Proveedor: envOr("VERA_PROVEEDOR", "simulado"), APIKey: strings.TrimSpace(os.Getenv("GEMINI_API_KEY")), OllamaURL: strings.TrimSpace(envOr("VERA_OLLAMA_URL", "http://127.0.0.1:11434"))}
	modeloPorDefecto := ""
	if c.Proveedor == "local" {
		modeloPorDefecto = "gemma4-e4b-local"
	}
	c.Modelo = strings.TrimSpace(envOr("VERA_MODELO", modeloPorDefecto))
	var errs []error
	if c.Proveedor != "simulado" && c.Proveedor != "gemini" && c.Proveedor != "local" {
		errs = append(errs, errors.New("VERA_PROVEEDOR debe ser simulado, gemini o local"))
	}
	if err := urlOrigenOpcional("VERA_OLLAMA_URL", c.OllamaURL); err != nil {
		errs = append(errs, err)
	}
	c.STTURL = strings.TrimSpace(os.Getenv("VERA_STT_URL"))
	c.STTToken = strings.TrimSpace(os.Getenv("VERA_STT_TOKEN"))
	if (c.STTURL == "") != (c.STTToken == "") {
		errs = append(errs, errors.New("VERA_STT_URL y VERA_STT_TOKEN deben definirse juntas"))
	} else if c.STTURL != "" {
		if err := urlSTTLocal(c.STTURL); err != nil {
			errs = append(errs, err)
		}
		if len(c.STTToken) < 32 {
			errs = append(errs, errors.New("VERA_STT_TOKEN debe tener al menos 32 caracteres"))
		}
		plazoSTT, errSTT := time.ParseDuration(envOr("VERA_STT_TIMEOUT", "30s"))
		if errSTT != nil || plazoSTT <= 0 || plazoSTT > 2*time.Minute {
			errs = append(errs, errors.New("VERA_STT_TIMEOUT debe ser una duración mayor que 0 y hasta 2m"))
		} else {
			c.STTPlazo = plazoSTT
		}
	}
	if c.Proveedor == "gemini" {
		if c.APIKey == "" {
			errs = append(errs, errors.New("GEMINI_API_KEY es obligatoria para VERA_PROVEEDOR=gemini"))
		}
		if c.Modelo == "" {
			errs = append(errs, errors.New("VERA_MODELO es obligatorio para VERA_PROVEEDOR=gemini"))
		}
	}
	if c.Proveedor == "local" && c.Modelo == "" {
		errs = append(errs, errors.New("VERA_MODELO es obligatorio para VERA_PROVEEDOR=local"))
	}
	plazoPorDefecto, plazoMaximo := "10s", 30*time.Second
	if c.Proveedor == "local" {
		// La inferencia en una GPU de 4 GB es más lenta que una API remota, pero el plazo por
		// intento tiene que dejar dos intentos dentro del corte del orquestador (45 s).
		plazoPorDefecto, plazoMaximo = "20s", 5*time.Minute
	}
	d, err := time.ParseDuration(envOr("VERA_TIMEOUT_LLAMADA", plazoPorDefecto))
	if err != nil || d <= 0 || d > plazoMaximo {
		errs = append(errs, fmt.Errorf("VERA_TIMEOUT_LLAMADA debe ser mayor que 0 y hasta %s", plazoMaximo))
	} else {
		c.Timeout = d
	}
	plazoOrquestador, errPlazo := time.ParseDuration(envOr("VERA_PLAZO_ORQUESTADOR", "45s"))
	if errPlazo != nil || plazoOrquestador < time.Second || plazoOrquestador > 5*time.Minute {
		errs = append(errs, errors.New("VERA_PLAZO_ORQUESTADOR debe ser una duración entre 1s y 5m"))
	} else {
		c.PlazoOrquestador = plazoOrquestador
	}
	t, err := strconv.ParseFloat(envOr("VERA_TEMPERATURA", "0.2"), 32)
	if err != nil || math.IsNaN(t) || t < 0 || t > 1 {
		errs = append(errs, errors.New("VERA_TEMPERATURA debe ser un número entre 0 y 1"))
	} else {
		c.Temperatura = float32(t)
	}
	c.Limites, err = cargarLimitesVera()
	if err != nil {
		errs = append(errs, err)
	}
	c.Preparacion.UmbralCodigo, err = nucleo.ParsearPesosEnteros(envOr("VERA_UMBRAL_CODIGO", "500000"))
	if err != nil {
		errs = append(errs, errors.New("VERA_UMBRAL_CODIGO debe ser un entero positivo de pesos sin separadores"))
	}
	c.Preparacion.LlavesHabilitadas, err = strconv.ParseBool(envOr("VERA_LLAVES_HABILITADAS", "false"))
	if err != nil {
		errs = append(errs, errors.New("VERA_LLAVES_HABILITADAS debe ser un booleano"))
	}
	c.RetencionDias, err = strconv.Atoi(envOr("VERA_RETENCION_DIAS", "30"))
	if err != nil || c.RetencionDias < 1 || c.RetencionDias > 36500 {
		errs = append(errs, errors.New("VERA_RETENCION_DIAS debe ser un entero de 1 a 36500"))
	}
	return c, errs
}

func cargarLimitesVera() (copilot.LimitesUso, error) {
	var l copilot.LimitesUso
	var errs []error
	for _, campo := range []struct {
		nombre, defecto string
		destino         *int64
	}{
		{"VERA_LIMITE_TEXTO_HORA", "30", &l.TextoHora},
		{"VERA_LIMITE_TEXTO_DIA", "150", &l.TextoDia},
		{"VERA_LIMITE_VOZ_HORA", "10", &l.VozHora},
		{"VERA_TOKENS_DIARIOS", "100000", &l.TokensDiarios},
		{"VERA_TOKENS_RESERVA_LLAMADA", "4096", &l.ReservaTokens},
	} {
		valor, err := strconv.ParseInt(envOr(campo.nombre, campo.defecto), 10, 64)
		if err != nil || valor <= 0 || valor > 1_000_000_000_000 {
			errs = append(errs, errors.New(campo.nombre+" debe ser un entero positivo hasta 1000000000000"))
		} else {
			*campo.destino = valor
		}
	}
	if l.ReservaTokens > l.TokensDiarios {
		errs = append(errs, errors.New("VERA_TOKENS_RESERVA_LLAMADA no puede superar VERA_TOKENS_DIARIOS"))
	}
	return l, errors.Join(errs...)
}

// urlSTTLocal exige un origen http de loopback sin ruta ni credenciales incrustadas.
func urlSTTLocal(valor string) error {
	u, err := url.Parse(valor)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("VERA_STT_URL debe ser un origen http en 127.0.0.1 sin ruta, recibido %q", valor)
	}
	return nil
}

// LogValue omite la clave y sólo indica su presencia.
func (c ConfigVera) LogValue() slog.Value {
	return slog.GroupValue(slog.String("proveedor", c.Proveedor), slog.String("modelo", c.Modelo), slog.String("ollama_url", c.OllamaURL), slog.Bool("gemini_api_key_set", c.APIKey != ""), slog.String("stt_url", c.STTURL), slog.Bool("stt_token_set", c.STTToken != ""), slog.Duration("stt_timeout", c.STTPlazo), slog.Duration("timeout", c.Timeout), slog.Float64("temperatura", float64(c.Temperatura)))
}

func nuevoProveedorIA(ctx context.Context, c ConfigVera) (copilot.ProveedorIA, error) {
	base, err := proveedorBase(ctx, c)
	if err != nil {
		return nil, err
	}
	if c.STTURL == "" && c.STTToken == "" {
		return base, nil
	}
	transcriptor, err := sttlocal.Nuevo(c.STTURL, c.STTToken, c.STTPlazo)
	if err != nil {
		return nil, fmt.Errorf("transcriptor local: %w", err)
	}
	return compuesto.Nuevo(base, transcriptor)
}

func proveedorBase(ctx context.Context, c ConfigVera) (copilot.ProveedorIA, error) {
	switch c.Proveedor {
	case "simulado":
		return provsim.Nuevo(), nil
	case "gemini":
		return gemini.Nuevo(ctx, gemini.Config{APIKey: c.APIKey, Modelo: c.Modelo, Timeout: c.Timeout, Temperatura: c.Temperatura}, slog.Default())
	case "local":
		// Sin sondeo al arrancar: Ollama puede no estar arriba todavía.
		return ollama.Nuevo(ollama.Config{URL: c.OllamaURL, Modelo: c.Modelo, Timeout: c.Timeout, Temperatura: c.Temperatura}, slog.Default())
	default:
		return nil, copilot.ErrSolicitudIA
	}
}
