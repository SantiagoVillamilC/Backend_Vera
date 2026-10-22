package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

func (s servidorV1) SolicitarCodigoAccionVera(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if !cuerpoAccionVacio(w, r) {
		return
	}
	if s.Vera.Acciones == nil {
		errorAccionVera(w, copilot.ErrNoDisponibleIA)
		return
	}
	origen, _ := clientIPKey(r)
	d, err := s.Vera.Acciones.SolicitarCodigo(r.Context(), id, origen)
	if err != nil {
		errorAccionVera(w, err)
		return
	}
	api.WriteData(w, 200, api.Desafio{ChallengeId: d.ID, ExpiraEnSegundos: d.ExpiraEnSegundos, Proposito: api.DesafioProposito(d.Proposito)}, nil)
}
func (s servidorV1) ConfirmarAccionVera(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	var req api.ConfirmarAccionVeraRequest
	if !api.DecodeAndValidateClosed(w, r, &req) {
		return
	}
	if s.Vera.Acciones == nil {
		errorAccionVera(w, copilot.ErrNoDisponibleIA)
		return
	}
	sol := copilot.ConfirmacionAccion{}
	if req.DesafioId != nil {
		sol.DesafioID = *req.DesafioId
	}
	if req.Codigo != nil {
		sol.Codigo = *req.Codigo
	}
	if req.SinVerificacionLocal != nil {
		sol.SinVerificacionLocal = *req.SinVerificacionLocal
	}
	a, err := s.Vera.Acciones.Confirmar(r.Context(), id, sol)
	if err != nil {
		errorAccionVera(w, err)
		return
	}
	escribirAccionVera(w, a)
}
func (s servidorV1) CancelarAccionVera(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if !cuerpoAccionVacio(w, r) {
		return
	}
	if s.Vera.Acciones == nil {
		errorAccionVera(w, copilot.ErrNoDisponibleIA)
		return
	}
	a, err := s.Vera.Acciones.Cancelar(r.Context(), id)
	if err != nil {
		errorAccionVera(w, err)
		return
	}
	escribirAccionVera(w, a)
}
func cuerpoAccionVacio(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength == 0 {
		return true
	}
	var req api.OperacionAccionVeraRequest
	return api.DecodeAndValidateClosed(w, r, &req)
}
func escribirAccionVera(w http.ResponseWriter, a copilot.AccionResuelta) {
	zona := time.FixedZone("America/Bogota", -5*60*60)
	r := api.AccionResuelta{AccionPendiente: *tarjetaVera(&a.Accion, zona)}
	if t := a.Transferencia; t != nil {
		r.Transferencia = &api.Transferencia{Id: t.ID, Destino: t.LlaveDestino, Estado: api.TransferenciaEstado(t.Estado), Monto: strconv.FormatInt(t.Monto.Int64(), 10), Moneda: api.TransferenciaMoneda(t.Moneda), CreadaEn: t.CreadaEn.In(zona), ActualizadaEn: t.ActualizadaEn.In(zona)}
	}
	api.WriteData(w, 200, r, nil)
}

func errorAccionVera(w http.ResponseWriter, err error) {
	for _, c := range []struct {
		err           error
		status        int
		code, message string
	}{
		{copilot.ErrAccionNoEncontrada, 404, "NotFound", "Acción no encontrada."},
		{copilot.ErrAccionNoPendiente, 409, "ActionNotPending", "La acción ya no está pendiente."},
		{copilot.ErrAccionExpirada, 410, "ActionExpired", "La acción pendiente superó su vigencia."},
		{copilot.ErrCodigoAccionRequerido, 403, "SecondFactorRequired", "La operación exige un código de confirmación válido."},
		{copilot.ErrConsentimientoAsistente, 403, "ConsentRequired", "Falta el consentimiento vigente del asistente."},
		{auth.ErrCodigoInvalido, 400, "OtpInvalid", "El código no es válido."},
		{auth.ErrCodigoExpirado, 400, "OtpExpired", "El código expiró."},
		{auth.ErrDesafioInvalido, 400, "ChallengeInvalid", "El desafío no es válido para esta acción."},
		{auth.ErrDemasiadosIntentos, 429, "OtpTooManyAttempts", "El código agotó sus intentos."},
		{auth.ErrEnfriamientoReenvio, 429, "OtpResendCooldown", "Espera antes de solicitar otro código."},
		{auth.ErrDemasiadosReenvios, 429, "OtpTooManyResends", "El código agotó sus reenvíos."},
		{auth.ErrDemasiadosEnvios, 429, "TooManyRequests", "Se superó el límite de códigos."},
		{auth.ErrEntregaFallida, 503, "OtpDeliveryFailed", "No se pudo entregar el código."},
		{auth.ErrAuthNoDisponible, 503, "AuthUnavailable", "No se pudo verificar la seguridad de la operación."},
		{auth.ErrSinIdentidad, 401, "Unauthorized", "Falta una sesión válida."},
		{auth.ErrCelularSinVerificar, 403, "PhoneUnverified", "El celular no está verificado."},
		{auth.ErrConsentimientoRequerido, 403, "ConsentRequired", "Falta el consentimiento para recibir códigos por WhatsApp."},
		{auth.ErrCuentaSuspendida, 403, "AccountSuspended", "La cuenta no puede operar."},
		{nucleo.ErrDirectorioSinRespuesta, 503, "DirectoryUnavailable", "El directorio de llaves no está disponible."},
		{nucleo.ErrDirectorioNoDisponible, 503, "DirectoryUnavailable", "El directorio de llaves no está disponible."},
		{context.DeadlineExceeded, 504, "Timeout", "La operación agotó su tiempo de respuesta."},
	} {
		if errors.Is(err, c.err) {
			if c.code == "OtpResendCooldown" {
				w.Header().Set("Retry-After", "60")
			}
			api.WriteError(w, c.status, c.code, c.message, nil)
			return
		}
	}
	if codigo := copilot.CodigoRechazoNucleo(err); codigo != "" {
		status := 422
		if codigo == "RecipientNotFound" {
			status = 404
		}
		if codigo == "IdempotencyConflict" {
			status = 409
		}
		api.WriteError(w, status, codigo, "El núcleo rechazó la transferencia. Revisa la operación antes de preparar otra.", nil)
		return
	}
	api.WriteError(w, 503, "AssistantUnavailable", "No se pudo completar la confirmación. Consulta el estado antes de reintentar.", nil)
}
