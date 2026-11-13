// Package ollama adapta el servidor local de Ollama al puerto del asistente.
package ollama

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// Parámetros fijos del modelo local: contexto de trabajo, salida máxima y el
// plazo de residencia en memoria. Con 8192 tokens de contexto el modelo cabe en
// una GPU de 4 GB y conserva instrucciones e historial reciente.
const (
	numCtx          = 8192
	numPredict      = 2048
	keepAlive       = "30m"
	intentos        = 3
	limiteRespuesta = 1 << 20
)

// Config no incluye credenciales: Ollama atiende por loopback sin token.
// URL es el origen del servidor, sin ruta ni credenciales embebidas.
type Config struct {
	URL         string
	Modelo      string
	Timeout     time.Duration
	Temperatura float32
}

// Proveedor realiza hasta tres intentos por turno, sin ejecutar herramientas.
type Proveedor struct {
	cfg     Config
	origen  string
	cliente *http.Client
	logger  *slog.Logger
	esperar func(context.Context, time.Duration) error
}

// Nuevo valida la configuración sin llamar a la red.
func Nuevo(cfg Config, logger *slog.Logger) (*Proveedor, error) {
	origen, err := origenHTTP(cfg.URL)
	if err != nil || strings.TrimSpace(cfg.Modelo) == "" || cfg.Timeout <= 0 ||
		math.IsNaN(float64(cfg.Temperatura)) || cfg.Temperatura < 0 || cfg.Temperatura > 1 {
		return nil, copilot.ErrSolicitudIA
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Proveedor{
		cfg:     cfg,
		origen:  origen,
		cliente: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		logger:  logger,
		esperar: esperar,
	}, nil
}

// origenHTTP acepta sólo un origen http(s) constante: una redirección o una ruta
// inesperada podría reenviar la conversación a otro destino.
func origenHTTP(crudo string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(crudo))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", copilot.ErrSolicitudIA
	}
	return u.Scheme + "://" + u.Host, nil
}

// Generar respeta el plazo de cada intento y cualquier plazo menor del llamador.
func (p *Proveedor) Generar(ctx context.Context, solicitud copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	if err := ctx.Err(); err != nil {
		return copilot.RespuestaIA{}, err
	}
	cuerpo, err := traducirSolicitud(solicitud, p.cfg)
	if err != nil {
		return copilot.RespuestaIA{}, err
	}
	for intento := range intentos {
		inicio := time.Now()
		llamadaCtx, cancelar := context.WithTimeout(ctx, p.cfg.Timeout)
		respuesta, reintentar, llamadaErr := p.intentar(llamadaCtx, cuerpo)
		cancelar()
		if ctx.Err() != nil {
			respuesta, reintentar, llamadaErr = copilot.RespuestaIA{}, false, ctx.Err()
		}
		resultado := "ok"
		if llamadaErr != nil {
			resultado = llamadaErr.Error()
		}
		p.logger.InfoContext(ctx, "llamada proveedor IA", "proveedor", "local", "modelo", p.cfg.Modelo,
			"duracion", time.Since(inicio), "tokens_entrada", respuesta.TokensEntrada,
			"tokens_salida", respuesta.TokensSalida, "resultado", resultado)
		if !reintentar {
			return respuesta, llamadaErr
		}
		if intento == intentos-1 {
			return respuesta, errors.Join(copilot.ErrNoDisponibleIA, llamadaErr)
		}
		// Fluctuación de 0 a 50% sobre 200ms y 400ms, no es aleatoriedad criptográfica.
		base := 200 * time.Millisecond * time.Duration(1<<intento)
		if err := p.esperar(ctx, base+time.Duration(rand.Int64N(int64(base/2)))); err != nil { //nolint:gosec // jitter, no datos de seguridad
			return copilot.RespuestaIA{}, err
		}
	}
	return copilot.RespuestaIA{}, copilot.ErrNoDisponibleIA
}

// intentar envía el cuerpo ya traducido y clasifica el fallo en una categoría
// pública. Nunca devuelve el cuerpo del servidor ni su mensaje de error.
func (p *Proveedor) intentar(ctx context.Context, cuerpo []byte) (copilot.RespuestaIA, bool, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, p.origen+"/api/chat", bytes.NewReader(cuerpo))
	if err != nil {
		return copilot.RespuestaIA{}, false, copilot.ErrSolicitudIA
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := p.cliente.Do(r)
	if err != nil {
		return copilot.RespuestaIA{}, true, clasificarTransporte(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		reintentar, clasificado := clasificarEstado(resp.StatusCode)
		return copilot.RespuestaIA{}, reintentar, clasificado
	}
	if tipo, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || tipo != "application/json" {
		return copilot.RespuestaIA{}, false, copilot.ErrRespuestaIA
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limiteRespuesta+1))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return copilot.RespuestaIA{}, true, copilot.ErrTimeoutIA
		}
		return copilot.RespuestaIA{}, true, copilot.ErrNoDisponibleIA
	}
	if len(b) > limiteRespuesta {
		return copilot.RespuestaIA{}, false, copilot.ErrRespuestaIA
	}
	return traducirRespuesta(b)
}

func clasificarTransporte(err error) error {
	var netErr net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		return copilot.ErrTimeoutIA
	}
	return copilot.ErrNoDisponibleIA
}

// clasificarEstado traduce el código HTTP. Sólo timeout y 5xx se reintentan:
// un 4xx es un error de la solicitud y repetirlo no lo corrige.
func clasificarEstado(codigo int) (bool, error) {
	switch {
	case codigo == http.StatusUnauthorized || codigo == http.StatusForbidden:
		return false, copilot.ErrCredencialesIA
	case codigo == http.StatusTooManyRequests:
		return false, copilot.ErrLimiteIA
	case codigo == http.StatusRequestTimeout:
		return true, copilot.ErrTimeoutIA
	case codigo >= 500 && codigo <= 599:
		return true, copilot.ErrNoDisponibleIA
	case codigo >= 400 && codigo <= 499:
		return false, copilot.ErrSolicitudIA
	default:
		return false, copilot.ErrNoDisponibleIA
	}
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
