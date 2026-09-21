package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

// CreadorDeEmpresasParaParte crea la parte juridica de una persona ya existente.
type CreadorDeEmpresasParaParte interface {
	CrearEmpresaParaParte(ctx context.Context, parteID uuid.UUID, razonSocial, nit string) (uuid.UUID, error)
}

// HandlerDeEmpresas atiende la creacion autenticada de empresa.
type HandlerDeEmpresas struct{ creador CreadorDeEmpresasParaParte }

// NuevoEmpresasHandler construye el handler sobre el servicio de registro.
func NuevoEmpresasHandler(creador CreadorDeEmpresasParaParte) *HandlerDeEmpresas {
	return &HandlerDeEmpresas{creador: creador}
}

// CreateCompany crea la empresa de la persona autenticada y responde 201 con sus tres datos.
func (h *HandlerDeEmpresas) CreateCompany(w http.ResponseWriter, r *http.Request) {
	var entrada api.CrearEmpresaRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	parteID, ok := partePersonaDeContexto(w, r)
	if !ok {
		return
	}
	empresaID, err := h.creador.CrearEmpresaParaParte(r.Context(), parteID, entrada.RazonSocial, entrada.Nit)
	if err != nil {
		escribirErrorDeEmpresa(w, r, err)
		return
	}
	api.WriteData(w, http.StatusCreated, api.EmpresaCreada{
		EmpresaId:   empresaID,
		Nit:         entrada.Nit,
		RazonSocial: entrada.RazonSocial,
	}, nil)
}

// partePersonaDeContexto exige identidad con contexto personal y devuelve el uuid de la persona.
func partePersonaDeContexto(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := identidad.DeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return uuid.Nil, false
	}
	if id.Contexto != auth.ContextoUsuario {
		api.WriteError(w, http.StatusBadRequest, "ContextInvalid", "The requested context is not available.", nil)
		return uuid.Nil, false
	}
	parteID, err := uuid.Parse(id.ParteID)
	if err != nil {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return uuid.Nil, false
	}
	return parteID, true
}

// escribirErrorDeEmpresa nombra nit y razon_social y delega el resto en el catalogo de auth.
func escribirErrorDeEmpresa(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrNitInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nit", Rule: "nit"}})
	case errors.Is(err, auth.ErrRazonSocialInvalida):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "razon_social", Rule: "max"}})
	default:
		escribirErrorDeAuth(w, r, err)
	}
}
