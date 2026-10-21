package laboratorio

import (
	"context"
	"errors"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
)

// Miden generación completa en el servidor, no audición ni calidad percibida.
type metricasVoz struct {
	Solicitudes int `json:"solicitudes"`
	Gemini      int `json:"gemini"`
	Local       int `json:"local"`
	Cache       int `json:"cache"`
	Fallos      int `json:"fallos"`
}

func (s *servidor) registrarVoz(cache bool) {
	if cache {
		s.voces.Cache++
		return
	}
	if s.fuenteCache == "kokoro" {
		s.voces.Local++
	} else {
		s.voces.Gemini++
	}
}

// La espera de Gemini nunca impide usar el sintetizador local. Sólo se cambia
// de motor antes del primer fragmento; no se mezclan dos voces a media frase.
func (s *servidor) generarVoz(ctx context.Context, voz string, recibir func([]byte) error) ([]byte, string, string, error) {
	var err error
	var audio []byte
	emitido := false
	if voz != "Dora" && s.voz != nil {
		ctxGoogle := ctx
		cancelar := func() {}
		if s.vozLocal != nil {
			ctxGoogle, cancelar = context.WithTimeout(ctx, 12*time.Second)
		}
		if flujo, ok := s.voz.(copilot.SintetizadorVozFlujo); ok && recibir != nil {
			audio, err = s.consola.SintetizarFlujo(ctxGoogle, flujo, s.ultimoTexto, voz, func(pcm []byte) error {
				emitido = emitido || len(pcm) > 0
				return recibir(pcm)
			})
		} else {
			audio, err = s.consola.Sintetizar(ctxGoogle, s.voz, s.ultimoTexto, voz)
		}
		cancelar()
		if err == nil {
			return audio, "gemini", "", nil
		}
	}
	if ctx.Err() != nil {
		return nil, "", "", ctx.Err()
	}
	if s.vozLocal == nil || emitido || (err != nil && !recuperableConVozLocal(err)) {
		return nil, "", "", err
	}
	motivo := ""
	if err != nil {
		motivo = "GeminiUnavailable"
		if errors.Is(err, copilot.ErrLimiteIA) {
			motivo = "GeminiQuota"
		}
		if errors.Is(err, evaluacion.ErrCuotaEvaluacion) {
			motivo = "SessionLimit"
		}
		if errors.Is(err, copilot.ErrTimeoutIA) || errors.Is(err, context.DeadlineExceeded) {
			motivo = "GeminiTimeout"
		}
	}
	// No se reserva presupuesto de Google: esta inferencia ocurre en CPU local.
	if flujo, ok := s.vozLocal.(copilot.SintetizadorVozFlujo); ok && recibir != nil {
		audio, err = flujo.SintetizarFlujo(ctx, s.ultimoTexto, "Dora", recibir)
	} else {
		audio, err = s.vozLocal.Sintetizar(ctx, s.ultimoTexto, "Dora")
	}
	return audio, "kokoro", motivo, err
}

func recuperableConVozLocal(err error) bool {
	return errors.Is(err, copilot.ErrLimiteIA) || errors.Is(err, copilot.ErrNoDisponibleIA) ||
		errors.Is(err, copilot.ErrTimeoutIA) || errors.Is(err, copilot.ErrRespuestaIA) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, copilot.ErrCredencialesIA) ||
		errors.Is(err, evaluacion.ErrCuotaEvaluacion)
}
