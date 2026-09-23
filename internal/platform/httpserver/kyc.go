package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/kyc"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// ServicioDeKYC es lo que el handler necesita del módulo de KYC.
type ServicioDeKYC interface {
	Estado(ctx context.Context, parteID string) (kyc.Estado, error)
	Verificar(ctx context.Context, parteID, nivel, proveedor string) error
}

// HandlerDeKYC atiende la consulta y la verificación autenticadas del KYC.
type HandlerDeKYC struct{ servicio ServicioDeKYC }

// NuevoKYC construye el handler autenticado de KYC.
func NuevoKYC(servicio ServicioDeKYC) *HandlerDeKYC { return &HandlerDeKYC{servicio: servicio} }

// KycEstado responde el producto y el estado de la parte del contexto activo.
func (h *HandlerDeKYC) KycEstado(w http.ResponseWriter, r *http.Request) {
	parteID, ok := parteDeContextoKYC(w, r)
	if !ok {
		return
	}
	estado, err := h.servicio.Estado(r.Context(), parteID)
	if err != nil {
		escribirErrorDeKYC(w, r, err)
		return
	}
	limites := map[string]interface{}{}
	api.WriteData(w, http.StatusOK, api.KycEstado{
		Producto:  api.KycEstadoProducto(estado.Producto),
		EstadoKyc: estado.EstadoKYC,
		Limites:   &limites,
	}, nil)
}

// KycVerificacion valida el nivel, lo aplica y responde el ok del envelope.
func (h *HandlerDeKYC) KycVerificacion(w http.ResponseWriter, r *http.Request) {
	parteID, ok := parteDeContextoKYC(w, r)
	if !ok {
		return
	}
	var entrada api.KycVerificacionRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	proveedor := ""
	if entrada.Proveedor != nil {
		proveedor = *entrada.Proveedor
	}
	if err := h.servicio.Verificar(r.Context(), parteID, string(entrada.Nivel), proveedor); err != nil {
		escribirErrorDeKYC(w, r, err)
		return
	}
	mensaje := "KYC verification processed."
	api.WriteData(w, http.StatusOK, api.OkData{Mensaje: &mensaje}, nil)
}

// parteDeContextoKYC exige la identidad autenticada y devuelve la parte que opera.
func parteDeContextoKYC(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, ok := identidad.IdentidadDeContexto(r.Context())
	if !ok || id.UsuarioID == "" {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return "", false
	}
	return id.UsuarioID, true
}

// escribirErrorDeKYC nombra el nivel inválido y delega el resto en el catálogo de auth.
func escribirErrorDeKYC(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, kyc.ErrNivelNoAplicaEmpresa):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nivel", Rule: "no_aplica_empresa"}})
	case errors.Is(err, kyc.ErrNivelNoSoportado):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nivel", Rule: "no_aplica"}})
	default:
		escribirErrorDeAuth(w, r, err)
	}
}
