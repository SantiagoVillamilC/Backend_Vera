// Package gemini adapta el SDK oficial al puerto del asistente.
package gemini

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"google.golang.org/genai"
)

// Config no se imprime: la API key sólo se entrega al SDK.
type Config struct {
	APIKey      string
	Modelo      string
	Timeout     time.Duration
	Temperatura float32
	// SinReintentos permite que el evaluador limite cada solicitud HTTP de cuota.
	SinReintentos bool
}

// Proveedor realiza hasta tres intentos por turno, sin ejecutar herramientas.
type Proveedor struct {
	cliente *genai.Client
	cfg     Config
	logger  *slog.Logger
	esperar func(context.Context, time.Duration) error
}

// Nuevo construye el cliente sin llamar a la red.
func Nuevo(ctx context.Context, cfg Config, logger *slog.Logger) (*Proveedor, error) {
	return nuevo(ctx, cfg, logger, nil, "")
}

func nuevo(ctx context.Context, cfg Config, logger *slog.Logger, httpClient *http.Client, baseURL string) (*Proveedor, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, copilot.ErrCredencialesIA
	}
	if strings.TrimSpace(cfg.Modelo) == "" || cfg.Timeout <= 0 || cfg.Temperatura < 0 || cfg.Temperatura > 1 || math.IsNaN(float64(cfg.Temperatura)) {
		return nil, copilot.ErrSolicitudIA
	}
	if logger == nil {
		logger = slog.Default()
	}
	cliente, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: cfg.APIKey, Backend: genai.BackendGeminiAPI, HTTPClient: httpClient,
		HTTPOptions: genai.HTTPOptions{BaseURL: baseURL, APIVersion: "v1beta", RetryOptions: &genai.HTTPRetryOptions{Attempts: genai.Ptr(int32(1))}},
	})
	if err != nil {
		return nil, copilot.ErrNoDisponibleIA
	}
	return &Proveedor{cliente: cliente, cfg: cfg, logger: logger, esperar: esperar}, nil
}

// Generar respeta el plazo de cada intento y cualquier plazo menor del llamador.
// Los reintentos internos del SDK y las continuaciones automáticas están apagados.
func (p *Proveedor) Generar(ctx context.Context, solicitud copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	if err := ctx.Err(); err != nil {
		return copilot.RespuestaIA{}, err
	}
	contenidos, config, err := traducirSolicitud(solicitud, p.cfg.Temperatura)
	if err != nil {
		return copilot.RespuestaIA{}, err
	}
	intentos := 3
	if p.cfg.SinReintentos {
		intentos = 1
	}
	for intento := range intentos {
		inicio := time.Now()
		llamadaCtx, cancelar := context.WithTimeout(ctx, p.cfg.Timeout)
		raw, llamadaErr := p.cliente.Models.GenerateContent(llamadaCtx, p.cfg.Modelo, contenidos, config)
		errContexto := llamadaCtx.Err()
		cancelar()
		var respuesta copilot.RespuestaIA
		var reintentar bool
		switch {
		case ctx.Err() != nil:
			err = ctx.Err()
		case errors.Is(errContexto, context.DeadlineExceeded):
			err, reintentar = copilot.ErrTimeoutIA, true
		case llamadaErr != nil:
			reintentar, err = clasificar(llamadaErr)
		default:
			respuesta, err = traducirRespuesta(raw)
		}
		resultado := "ok"
		if err != nil {
			resultado = err.Error()
		}
		p.logger.InfoContext(ctx, "llamada proveedor IA", "modelo", p.cfg.Modelo,
			"duracion", time.Since(inicio), "tokens_entrada", respuesta.TokensEntrada,
			"tokens_salida", respuesta.TokensSalida, "resultado", resultado)
		if !reintentar {
			return respuesta, err
		}
		if intento == intentos-1 {
			return respuesta, errors.Join(copilot.ErrNoDisponibleIA, err)
		}
		// Fluctuación de 0..50% sobre 200ms y 400ms; no es aleatoriedad criptográfica.
		base := 200 * time.Millisecond * time.Duration(1<<intento)
		if err := p.esperar(ctx, base+time.Duration(rand.Int64N(int64(base/2)))); err != nil { //nolint:gosec // jitter, no datos de seguridad
			return copilot.RespuestaIA{}, err
		}
	}
	return copilot.RespuestaIA{}, copilot.ErrNoDisponibleIA
}

func clasificar(err error) (bool, error) {
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.Code == http.StatusUnauthorized || apiErr.Code == http.StatusForbidden:
			return false, copilot.ErrCredencialesIA
		case apiErr.Code == http.StatusBadRequest:
			for _, detalle := range apiErr.Details {
				if detalle["reason"] == "API_KEY_INVALID" {
					return false, copilot.ErrCredencialesIA
				}
			}
			return false, copilot.ErrSolicitudIA
		case apiErr.Code == http.StatusTooManyRequests:
			return true, copilot.ErrLimiteIA
		case apiErr.Code == http.StatusRequestTimeout:
			return true, copilot.ErrTimeoutIA
		case apiErr.Code >= 500 && apiErr.Code <= 599:
			return true, copilot.ErrNoDisponibleIA
		default:
			return false, copilot.ErrSolicitudIA
		}
	}
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return true, copilot.ErrTimeoutIA
	}
	return false, copilot.ErrNoDisponibleIA
}

func esperar(ctx context.Context, duracion time.Duration) error {
	timer := time.NewTimer(duracion)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var _ copilot.ProveedorIA = (*Proveedor)(nil)
