// Package cartesia permite evaluar TTS sin cambiar el proveedor de chat de Vera.
package cartesia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/audiopcm"
)

// ModeloVoz y VersionAPI fijan la versión evaluada, sin actualizaciones silenciosas.
const (
	ModeloVoz  = "sonic-3.6-2026-08-27"
	VersionAPI = "2026-08-14"
)

// Candidata describe voces públicas del catálogo consultado con la clave local.
type Candidata struct {
	Nombre string `json:"nombre"`
	ID     string `json:"id"`
	Locale string `json:"locale"`
}

// Candidatas devuelve una lista independiente; no crea ni clona voces.
func Candidatas() []Candidata {
	return []Candidata{
		{"Catalina", "162e0f37-8504-474c-bb33-c606c01890dc", "es-CO"},
		{"Mariana", "ae823354-f9be-4aef-8543-f569644136b4", "es-CO"},
		{"Ximena", "3597a26f-80ef-4bd5-8101-9699bc764917", "es-MX"},
	}
}

// Voz implementa la síntesis literal mediante HTTPS, sin registros ni reintentos.
type Voz struct {
	clave    string
	endpoint string
	cliente  *http.Client
}

// NuevaVoz no realiza llamadas ni expone la clave al navegador.
func NuevaVoz(clave string) (*Voz, error) {
	if strings.TrimSpace(clave) == "" {
		return nil, copilot.ErrCredencialesIA
	}
	return &Voz{clave: clave, endpoint: "https://api.cartesia.ai/tts/bytes", cliente: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Sintetizar acepta sólo nombres evaluados y solicita PCM16 mono de 24 kHz en WAV.
// El locale guía la lectura de cifras; el texto no se modifica ni se recalcula.
func (v *Voz) Sintetizar(ctx context.Context, texto, nombre string) ([]byte, error) {
	if !utf8.ValidString(texto) || strings.TrimSpace(texto) == "" || len(texto) > 8192 {
		return nil, copilot.ErrSolicitudIA
	}
	var candidata Candidata
	for _, c := range Candidatas() {
		if c.Nombre == nombre {
			candidata = c
			break
		}
	}
	if candidata.ID == "" {
		return nil, copilot.ErrSolicitudIA
	}
	b, err := json.Marshal(map[string]any{
		"model_id": ModeloVoz, "transcript": texto, "voice": candidata.ID, "locale": candidata.Locale,
		"output_format": map[string]any{"container": "wav", "encoding": "pcm_s16le", "sample_rate": 24000},
	})
	if err != nil {
		return nil, copilot.ErrSolicitudIA
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, copilot.ErrSolicitudIA
	}
	r.Header.Set("Authorization", "Bearer "+v.clave)
	r.Header.Set("Cartesia-Version", VersionAPI)
	r.Header.Set("Content-Type", "application/json")
	respuesta, err := v.cliente.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var e net.Error
		if errors.As(err, &e) && e.Timeout() {
			return nil, copilot.ErrTimeoutIA
		}
		return nil, copilot.ErrNoDisponibleIA
	}
	defer func() { _ = respuesta.Body.Close() }()
	switch respuesta.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, copilot.ErrCredencialesIA
	case http.StatusPaymentRequired, http.StatusTooManyRequests:
		espera := time.Minute
		if n, e := strconv.Atoi(respuesta.Header.Get("Retry-After")); e == nil && n > 0 && n <= 86400 {
			espera = time.Duration(n) * time.Second
		}
		return nil, &copilot.ErrorLimiteIA{Clase: copilot.LimiteDesconocido, ReintentarEn: espera}
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return nil, copilot.ErrSolicitudIA
	default:
		return nil, copilot.ErrNoDisponibleIA
	}
	audio, err := io.ReadAll(io.LimitReader(respuesta.Body, 4*1024*1024+4097))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, copilot.ErrRespuestaIA
	}
	normalizado, ok := audiopcm.Normalizar(audio)
	if !ok {
		return nil, copilot.ErrRespuestaIA
	}
	return normalizado, nil
}

var _ copilot.SintetizadorVoz = (*Voz)(nil)
