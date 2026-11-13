// Package sttlocal consume Whisper por loopback sin persistir audio ni transcripciones.
package sttlocal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// Cliente acota cada transcripción a un plazo y no sigue redirecciones.
type Cliente struct {
	endpoint, token string
	http            *http.Client
}

// Nuevo requiere una dirección HTTP de loopback, token y plazo de hasta dos minutos.
func Nuevo(endpoint, token string, plazo time.Duration) (*Cliente, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(token) < 32 || plazo <= 0 || plazo > 2*time.Minute {
		return nil, copilot.ErrSolicitudIA
	}
	return &Cliente{endpoint, token, &http.Client{Timeout: plazo, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Transcribir devuelve texto vacío para silencio; no crea conversaciones.
func (c *Cliente) Transcribir(ctx context.Context, a copilot.AudioIA) (string, error) {
	tipo, _, err := mime.ParseMediaType(a.MIME)
	if err != nil || len(a.Datos) == 0 || len(a.Datos) > 8*1024*1024 {
		return "", copilot.ErrSolicitudIA
	}
	switch tipo {
	case "audio/wav", "audio/x-wav", "audio/m4a", "audio/mp4", "audio/mpeg":
	default:
		return "", copilot.ErrSolicitudIA
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/transcribir", bytes.NewReader(a.Datos))
	if err != nil {
		return "", copilot.ErrSolicitudIA
	}
	r.Header.Set("Content-Type", tipo)
	r.Header.Set("X-Vera-Token", c.token)
	resp, err := c.http.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return "", copilot.ErrTimeoutIA
		}
		return "", copilot.ErrNoDisponibleIA
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case 200:
	case 401:
		return "", copilot.ErrCredencialesIA
	case 409, 429:
		return "", copilot.ErrLimiteIA
	case 413, 415, 422:
		return "", copilot.ErrSolicitudIA
	default:
		return "", copilot.ErrNoDisponibleIA
	}
	tipo, _, err = mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || tipo != "application/json" {
		return "", copilot.ErrRespuestaIA
	}
	var salida struct {
		Texto  *string `json:"transcripcion"`
		Modelo string  `json:"modelo"`
		Idioma string  `json:"idioma"`
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil || len(b) > 64*1024 || json.Unmarshal(b, &salida) != nil || salida.Texto == nil || salida.Modelo != "small" || salida.Idioma != "es" {
		return "", copilot.ErrRespuestaIA
	}
	return strings.TrimSpace(*salida.Texto), nil
}
