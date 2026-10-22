package httpserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

func (s servidorV1) EnviarMensajeVozVera(w http.ResponseWriter, r *http.Request, params api.EnviarMensajeVozVeraParams) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if !s.exigirConsentimientoVera(w, r, nil) {
		return
	}
	var req api.EnviarAudioVeraRequest
	if !api.DecodeAndValidateClosed(w, r, &req) {
		return
	}
	var conv, clave uuid.UUID
	if req.ConversacionId != nil {
		conv = *req.ConversacionId
		if conv == uuid.Nil {
			api.WriteError(w, 422, "ValidationError", "conversacion_id debe ser un UUID válido no nulo.", nil)
			return
		}
	}
	if params.IdempotencyKey != nil {
		clave = *params.IdempotencyKey
		if clave == uuid.Nil {
			api.WriteError(w, 422, "ValidationError", "Idempotency-Key debe ser un UUID válido no nulo.", nil)
			return
		}
	}
	audio, err := copilot.ValidarAudio(req.Audio, string(req.Formato))
	req.Audio = ""
	if err != nil {
		errorVozVera(w, err)
		return
	}
	defer clear(audio.Datos)
	if !s.exigirUsoVera(w, r, copilot.CanalVoz) {
		return
	}
	if s.Vera.Voz == nil {
		errorTextoVera(w, copilot.ErrNoDisponibleIA)
		return
	}
	salida, err := s.Vera.Voz.EnviarVoz(ctx, conv, audio, clave)
	if salida.ConversacionID != uuid.Nil {
		w.Header().Set("X-Conversacion-Id", salida.ConversacionID.String())
	}
	if salida.MensajeUsuarioID != uuid.Nil {
		w.Header().Set("X-Mensaje-Usuario-Id", salida.MensajeUsuarioID.String())
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		errorVozVera(w, err)
		return
	}
	escribirRespuestaVera(w, salida)
}

func errorVozVera(w http.ResponseWriter, err error) {
	var audio *copilot.ErrorAudio
	switch {
	case errors.As(err, &audio):
		api.WriteError(w, 422, "ValidationError", "El audio no cumple el formato, tamaño o duración permitidos.", []api.FieldError{{Field: "audio", Rule: audio.Regla}})
	case errors.Is(err, copilot.ErrAudioNoEntendido):
		api.WriteError(w, 422, "AudioNotUnderstood", "No entendí el audio, repite o escribe.", nil)
	default:
		errorTextoVera(w, err)
	}
}
