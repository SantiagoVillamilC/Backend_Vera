package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ServicioDeCambioDeNumero es lo que el handler autenticado necesita del cambio de número.
type ServicioDeCambioDeNumero interface {
	IniciarCambioDeNumero(ctx context.Context, sesionID uuid.UUID, nuevoCelular, contrasenaActual string, codigoRespaldo *string, origen string) (auth.Desafio, error)
	ConfirmarCambioDeNumero(ctx context.Context, sesionID, challengeID uuid.UUID, codigo, origen string) (auth.ResultadoCambio, error)
}

// HandlerDeCambioDeNumero atiende la solicitud y la confirmación del cambio de celular.
type HandlerDeCambioDeNumero struct{ servicio ServicioDeCambioDeNumero }

// NuevoCambioDeNumeroHandler construye el handler sobre el servicio de cambio.
func NuevoCambioDeNumeroHandler(servicio ServicioDeCambioDeNumero) *HandlerDeCambioDeNumero {
	return &HandlerDeCambioDeNumero{servicio: servicio}
}

// PhoneChangeRequest valida el cuerpo, inicia el cambio y responde el desafío con su propósito.
func (h *HandlerDeCambioDeNumero) PhoneChangeRequest(w http.ResponseWriter, r *http.Request) {
	var entrada api.PhoneChangeRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	sesionID, ok := sesionDelCambio(w, r)
	if !ok {
		return
	}
	origen, _ := clientIPKey(r)
	desafio, err := h.servicio.IniciarCambioDeNumero(r.Context(), sesionID, entrada.NuevoCelular, entrada.ContrasenaActual, entrada.CodigoRespaldo, origen)
	if err != nil {
		escribirErrorDelCambio(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, api.Desafio{
		ChallengeId:      openapi_types.UUID(desafio.ID),
		ExpiraEnSegundos: desafio.ExpiraEnSegundos,
		Proposito:        api.DesafioProposito(desafio.Proposito),
	}, nil)
}

// PhoneChangeConfirm valida el cuerpo, confirma el cambio y responde el mensaje del desenlace.
func (h *HandlerDeCambioDeNumero) PhoneChangeConfirm(w http.ResponseWriter, r *http.Request) {
	var entrada api.VerifyOtpRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	sesionID, ok := sesionDelCambio(w, r)
	if !ok {
		return
	}
	origen, _ := clientIPKey(r)
	resultado, err := h.servicio.ConfirmarCambioDeNumero(r.Context(), sesionID, uuid.UUID(entrada.ChallengeId), entrada.Codigo, origen)
	if err != nil {
		escribirErrorDelCambio(w, r, err)
		return
	}
	mensaje := "Numero actualizado."
	if !resultado.Confirmado {
		mensaje = "Enviamos un codigo al numero nuevo."
	}
	api.WriteData(w, http.StatusOK, api.OkData{Mensaje: &mensaje}, nil)
}

// sesionDelCambio resuelve la familia de sesión de la identidad autenticada.
func sesionDelCambio(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := identidad.IdentidadDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return uuid.Nil, false
	}
	sesionID, err := uuid.Parse(id.SesionID)
	if err != nil {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return uuid.Nil, false
	}
	return sesionID, true
}

// escribirErrorDelCambio traduce los 422 de campo del cambio y delega el resto en el mapeo de auth.
func escribirErrorDelCambio(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrCelularInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nuevo_celular", Rule: "e164"}})
		return
	case errors.Is(err, auth.ErrCelularNuevoIgual):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nuevo_celular", Rule: "distinto"}})
		return
	}
	mapeado := auth.MapearError(err)
	var apiErr *platformerrors.APIError
	if errors.As(mapeado, &apiErr) {
		api.WriteError(w, apiErr.Status, apiErr.Code, apiErr.Message, nil)
		return
	}
	api.WriteAPIError(w, r, mapeado)
}
