package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

func (s servidorV1) EnviarMensajeVera(w http.ResponseWriter, r *http.Request, params api.EnviarMensajeVeraParams) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if !s.exigirConsentimientoVera(w, r, nil) {
		return
	}
	var req api.EnviarMensajeVeraRequest
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
	if !s.exigirUsoVera(w, r, copilot.CanalTexto) {
		return
	}
	if s.Vera.Texto == nil {
		errorTextoVera(w, copilot.ErrNoDisponibleIA)
		return
	}
	salida, err := s.Vera.Texto.EnviarTexto(ctx, conv, req.Texto, clave)
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
		errorTextoVera(w, err)
		return
	}
	escribirRespuestaVera(w, salida)
}

func escribirRespuestaVera(w http.ResponseWriter, salida copilot.RespuestaTexto) {
	zona := time.FixedZone("America/Bogota", -5*60*60)
	respuesta := api.RespuestaAsistente{ConversacionId: salida.ConversacionID, MensajeId: salida.MensajeID, Texto: salida.Texto, CreadoEn: salida.CreadoEn.In(zona)}
	if salida.Transcripcion != "" {
		respuesta.Transcripcion = &salida.Transcripcion
	}
	if a := salida.Accion; a != nil {
		nombre := a.Nombre
		if a.Tipo == "transferencia_llave" && a.Entidad != "" {
			nombre = a.Entidad
		}
		dest := &api.DestinatarioAccion{Nombre: nombre}
		if a.Celular != "" {
			dest.CelularParcial = &a.Celular
		}
		card := &api.AccionPendiente{Id: a.ID, Estado: api.AccionPendienteEstado(a.Estado), Tipo: api.AccionPendienteTipo(a.Tipo), Moneda: api.AccionPendienteMoneda("COP"), Monto: strconv.FormatInt(int64(a.Monto), 10), RequiereCodigo: a.RequiereCodigo, ExpiraEn: a.ExpiraEn.In(zona), Destinatario: dest}
		if a.Llave != "" {
			card.Llave = &a.Llave
		}
		if a.TipoLlave != "" {
			tipo := api.AccionPendienteTipoLlave(a.TipoLlave)
			card.TipoLlave = &tipo
		}
		respuesta.AccionPendiente = card
	}
	api.WriteData(w, http.StatusOK, respuesta, nil)
}

// Nunca se envía ni se registra la causa privada del proveedor o de PostgreSQL.
func errorTextoVera(w http.ResponseWriter, err error) {
	status, code, message := 503, "AssistantUnavailable", "El asistente no está disponible. Puedes reintentar el mensaje."
	var limite *copilot.ErrorLimiteUso
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled), errors.Is(err, copilot.ErrTimeoutIA):
		status, code, message = 504, "Timeout", "El asistente agotó el tiempo de respuesta. Puedes reintentar el mensaje."
	case errors.Is(err, copilot.ErrConversacionTextoNoEncontrada):
		status, code, message = 404, "NotFound", "Conversación no encontrada."
	case errors.Is(err, copilot.ErrTextoEnProceso), errors.Is(err, copilot.ErrCicloNoDisponible):
		status, code, message = 409, "ConversationBusy", "La conversación tiene un mensaje en procesamiento."
		w.Header().Set("Retry-After", "1")
	case errors.Is(err, copilot.ErrClaveTextoEnConflicto):
		status, code, message = 409, "IdempotencyConflict", "La clave de reintento ya corresponde a otra solicitud."
	case errors.Is(err, copilot.ErrConsentimientoAsistente):
		status, code, message = 403, "ConsentRequired", "Falta el consentimiento vigente del asistente."
	case errors.As(err, &limite):
		status, code, message = 429, "TooManyRequests", "Límite de uso del asistente alcanzado."
		segundos := max(int64((limite.ReintentarEn+time.Second-1)/time.Second), 1)
		w.Header().Set("Retry-After", strconv.FormatInt(segundos, 10))
	}
	api.WriteError(w, status, code, message, nil)
}
