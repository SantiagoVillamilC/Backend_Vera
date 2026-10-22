package httpserver

import (
	"errors"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

func (s servidorV1) exigirConsentimientoVera(w http.ResponseWriter, r *http.Request, accion *uuid.UUID) bool {
	var err error
	if accion == nil {
		err = s.Vera.Consentimiento.Exigir(r.Context())
	} else {
		err = s.Vera.Consentimiento.ExigirConfirmacion(r.Context(), *accion)
	}
	if err == nil {
		return true
	}
	if errors.Is(err, copilot.ErrConsentimientoAsistente) {
		api.WriteError(w, http.StatusForbidden, "ConsentRequired", "Falta el consentimiento vigente del asistente.", nil)
	} else {
		api.WriteError(w, http.StatusServiceUnavailable, "AssistantUnavailable", "No se pudo verificar el consentimiento del asistente.", nil)
	}
	return false
}
