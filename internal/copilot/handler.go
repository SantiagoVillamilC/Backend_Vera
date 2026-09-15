package copilot

import (
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// Handler expone el endpoint de chat del copilot.
type Handler struct{ service *Service }

// NewHandler construye el handler sobre el service de copilot.
func NewHandler(s *Service) *Handler { return &Handler{service: s} }

// Chat responde a la conversación que llega en el cuerpo de la petición.
func (h *Handler) Chat(w http.ResponseWriter, r *http.Request) {
	var req Request
	if !api.DecodeAndValidate(w, r, &req) {
		return
	}
	result, err := h.service.Respond(r.Context(), req)
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, result, nil)
}
