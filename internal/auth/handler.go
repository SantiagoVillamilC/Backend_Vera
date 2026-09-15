package auth

import (
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// Handler expone los endpoints públicos de autenticación.
type Handler struct{ service *Service }

// NewHandler construye el handler sobre el service de auth.
func NewHandler(s *Service) *Handler { return &Handler{service: s} }

// Login autentica al usuario con las credenciales del cuerpo.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	h.respond(w, r, func(req LoginRequest) (any, error) { return h.service.Login(r.Context(), req) })
}

// Register crea la cuenta a partir del cuerpo de la petición.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	h.respond(w, r, func(req LoginRequest) (any, error) { return h.service.Register(r.Context(), req) })
}

// VerifyMFA sigue pendiente de implementación hasta que el esquema tenga MFA.
func (h *Handler) VerifyMFA(w http.ResponseWriter, _ *http.Request) {
	api.WriteError(w, http.StatusNotImplemented, "NotImplemented", "TODO: verify MFA", nil)
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, fn func(LoginRequest) (any, error)) {
	var req LoginRequest
	if !api.DecodeAndValidate(w, r, &req) {
		return
	}
	result, err := fn(req)
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, result, nil)
}
