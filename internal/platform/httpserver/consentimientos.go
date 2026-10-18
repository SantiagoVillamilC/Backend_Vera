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

// ServicioDeConsentimientos es lo que el handler necesita del modulo de consentimientos.
type ServicioDeConsentimientos interface {
	Listar(ctx context.Context, parteID uuid.UUID) ([]auth.Consentimiento, error)
	Registrar(ctx context.Context, solicitud auth.SolicitudDeConsentimientoNuevo) (auth.Consentimiento, error)
}

// HandlerDeConsentimientos atiende la consulta y el registro autenticados de consentimientos.
type HandlerDeConsentimientos struct{ servicio ServicioDeConsentimientos }

// NuevoConsentimientosHandler construye el handler autenticado de consentimientos.
func NuevoConsentimientosHandler(servicio ServicioDeConsentimientos) *HandlerDeConsentimientos {
	return &HandlerDeConsentimientos{servicio: servicio}
}

// ListConsents lista el historial completo de la persona, mas reciente primero, con el texto exacto.
func (h *HandlerDeConsentimientos) ListConsents(w http.ResponseWriter, r *http.Request, params api.ListConsentsParams) {
	parteID, ok := personaDeConsentimiento(w, r)
	if !ok {
		return
	}
	if params.Tipo != nil {
		if string(*params.Tipo) != auth.TipoConsentimientoAsistente {
			api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Tipo de consentimiento inválido.", nil)
			return
		}
		lector, ok := h.servicio.(interface {
			EstadoAsistente(context.Context, uuid.UUID) (auth.EstadoConsentimientoAsistente, error)
		})
		if !ok {
			api.WriteError(w, http.StatusServiceUnavailable, "AuthUnavailable", "Consentimiento no disponible.", nil)
			return
		}
		estado, err := lector.EstadoAsistente(r.Context(), parteID)
		if err != nil {
			escribirErrorDeConsentimiento(w, r, err)
			return
		}
		api.WriteData(w, http.StatusOK, api.EstadoConsentimientoAsistente{Tipo: api.EstadoConsentimientoAsistenteTipo(auth.TipoConsentimientoAsistente), VersionTexto: estado.VersionTexto, Texto: estado.Texto, Autorizado: estado.Autorizado}, nil)
		return
	}
	consentimientos, err := h.servicio.Listar(r.Context(), parteID)
	if err != nil {
		escribirErrorDeConsentimiento(w, r, err)
		return
	}
	respuesta := make([]api.Consent, 0, len(consentimientos))
	for _, consentimiento := range consentimientos {
		respuesta = append(respuesta, respuestaDeConsentimiento(consentimiento))
	}
	api.WriteData(w, http.StatusOK, respuesta, nil)
}

// CreateConsent valida la coherencia de la accion, registra la fila nueva y responde 201.
func (h *HandlerDeConsentimientos) CreateConsent(w http.ResponseWriter, r *http.Request) {
	parteID, ok := personaDeConsentimiento(w, r)
	if !ok {
		return
	}
	var entrada api.ConsentRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	if !accionCoherente(w, entrada) {
		return
	}
	origen, _ := clientIPKey(r)
	metodo := ""
	if entrada.Metodo != nil {
		metodo = *entrada.Metodo
	}
	_, err := h.servicio.Registrar(r.Context(), auth.SolicitudDeConsentimientoNuevo{
		ParteID:      parteID,
		Tipo:         string(entrada.Tipo),
		VersionTexto: entrada.VersionTexto,
		Aceptado:     *entrada.Aceptado,
		Metodo:       metodo,
		Origen:       origen,
		UserAgent:    r.UserAgent(),
	})
	if err != nil {
		escribirErrorDeConsentimiento(w, r, err)
		return
	}
	mensaje := "Consent recorded."
	api.WriteData(w, http.StatusCreated, api.OkData{Mensaje: &mensaje}, nil)
}

// accionCoherente exige la accion coherente con aceptado (otorgar/true, revocar/false) y aplica el default otorgar del contrato.
func accionCoherente(w http.ResponseWriter, entrada api.ConsentRequest) bool {
	accion := auth.AccionConsentimientoOtorgar
	if entrada.Accion != nil {
		accion = string(*entrada.Accion)
	}
	coherente := (accion == auth.AccionConsentimientoOtorgar && *entrada.Aceptado) ||
		(accion == auth.AccionConsentimientoRevocar && !*entrada.Aceptado)
	if !coherente {
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "aceptado", Rule: "coherente_con_accion"}})
		return false
	}
	return true
}

// personaDeConsentimiento resuelve la persona duena de la sesion por su parte, nunca por el usuario del contexto.
func personaDeConsentimiento(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, ok := identidad.DeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return uuid.Nil, false
	}
	parteID, err := uuid.Parse(id.ParteID)
	if err != nil {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return uuid.Nil, false
	}
	return parteID, true
}

// respuestaDeConsentimiento traduce la fila de dominio al contrato con el metodo opcional.
func respuestaDeConsentimiento(consentimiento auth.Consentimiento) api.Consent {
	respuesta := api.Consent{
		Tipo:         consentimiento.Tipo,
		VersionTexto: consentimiento.VersionTexto,
		Aceptado:     consentimiento.Aceptado,
		Texto:        consentimiento.Texto,
		Origen:       consentimiento.Origen,
		Accion:       api.ConsentAccion(consentimiento.Accion),
		Fecha:        consentimiento.Fecha,
	}
	if consentimiento.Metodo != "" {
		respuesta.Metodo = &consentimiento.Metodo
	}
	return respuesta
}

// escribirErrorDeConsentimiento nombra la version inexistente, el opt-in unico y delega el resto en auth.
func escribirErrorDeConsentimiento(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrVersionDeConsentimientoInexistente):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "version_texto", Rule: "vigente"}})
	default:
		escribirErrorDeAuth(w, r, err)
	}
}
