package httpserver

import (
	"context"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ControlDeSesiones lista y revoca las sesiones y resuelve el contexto activo de la parte.
type ControlDeSesiones interface {
	ListarDispositivos(ctx context.Context, parteID uuid.UUID) ([]auth.Sesion, error)
	CerrarSesionPorRefresh(ctx context.Context, parteID uuid.UUID, refreshToken string) error
	CerrarTodasLasSesiones(ctx context.Context, parteID uuid.UUID) error
	RevocarDispositivo(ctx context.Context, parteID, familiaID uuid.UUID) error
	ListarContextos(ctx context.Context, parteID uuid.UUID) ([]auth.ContextoDeParte, error)
	ActivarContexto(ctx context.Context, parteID, familiaID uuid.UUID, contextoID string) (auth.ContextoActivo, error)
}

// HandlerDeDispositivos atiende el control de sesiones y contextos del contrato.
type HandlerDeDispositivos struct{ servicio ControlDeSesiones }

// NuevoDispositivosHandler construye el handler sobre el servicio de sesiones.
func NuevoDispositivosHandler(servicio ControlDeSesiones) *HandlerDeDispositivos {
	return &HandlerDeDispositivos{servicio: servicio}
}

// Logout revoca la familia del refresh del cuerpo y responde 204.
func (h *HandlerDeDispositivos) Logout(w http.ResponseWriter, r *http.Request) {
	var entrada api.RefreshRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	parteID, _, ok := idsDeDispositivo(w, r)
	if !ok {
		return
	}
	if err := h.servicio.CerrarSesionPorRefresh(r.Context(), parteID, entrada.RefreshToken); err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LogoutAll revoca todas las sesiones de la parte y responde 204.
func (h *HandlerDeDispositivos) LogoutAll(w http.ResponseWriter, r *http.Request) {
	parteID, _, ok := idsDeDispositivo(w, r)
	if !ok {
		return
	}
	if err := h.servicio.CerrarTodasLasSesiones(r.Context(), parteID); err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSessions responde las sesiones activas de la parte.
func (h *HandlerDeDispositivos) ListSessions(w http.ResponseWriter, r *http.Request) {
	parteID, _, ok := idsDeDispositivo(w, r)
	if !ok {
		return
	}
	sesiones, err := h.servicio.ListarDispositivos(r.Context(), parteID)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	dispositivos := make([]api.Dispositivo, 0, len(sesiones))
	for _, sesion := range sesiones {
		dispositivos = append(dispositivos, api.Dispositivo{
			Id:          openapi_types.UUID(sesion.FamiliaID),
			Dispositivo: sesion.Dispositivo,
			CreadaEn:    sesion.CreadaEn,
			UltimoUsoEn: sesion.UltimoUsoEn,
			Contexto:    sesion.ContextoActivo,
		})
	}
	api.WriteData(w, http.StatusOK, dispositivos, nil)
}

// DeleteSession revoca la sesión indicada si es de la parte.
func (h *HandlerDeDispositivos) DeleteSession(w http.ResponseWriter, r *http.Request, sessionId openapi_types.UUID) {
	parteID, _, ok := idsDeDispositivo(w, r)
	if !ok {
		return
	}
	if err := h.servicio.RevocarDispositivo(r.Context(), parteID, uuid.UUID(sessionId)); err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListContextos responde el contexto personal y las empresas con rol vigente de la parte.
func (h *HandlerDeDispositivos) ListContextos(w http.ResponseWriter, r *http.Request) {
	parteID, _, ok := idsDeDispositivo(w, r)
	if !ok {
		return
	}
	contextos, err := h.servicio.ListarContextos(r.Context(), parteID)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	respuesta := make([]api.Contexto, 0, len(contextos))
	for _, contexto := range contextos {
		respuesta = append(respuesta, contextoDelContrato(contexto))
	}
	api.WriteData(w, http.StatusOK, respuesta, nil)
}

// SetContexto cambia el contexto de la sesión y responde un access nuevo.
func (h *HandlerDeDispositivos) SetContexto(w http.ResponseWriter, r *http.Request) {
	var entrada api.ContextoRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	parteID, familiaID, ok := idsDeDispositivo(w, r)
	if !ok {
		return
	}
	activo, err := h.servicio.ActivarContexto(r.Context(), parteID, familiaID, entrada.ContextoId)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, api.ContextoActivo{
		AccessToken: activo.AccessToken,
		Contexto:    contextoDelContrato(activo.Contexto),
	}, nil)
}

// idsDeDispositivo traduce la identidad de la petición a los dos uuid que usan estos handlers.
func idsDeDispositivo(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	id, ok := identidad.IdentidadDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return uuid.Nil, uuid.Nil, false
	}
	parteID, errParte := uuid.Parse(id.ParteID)
	familiaID, errSesion := uuid.Parse(id.SesionID)
	if errParte != nil || errSesion != nil {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return uuid.Nil, uuid.Nil, false
	}
	return parteID, familiaID, true
}

// contextoDelContrato traduce el contexto de dominio al contrato, con el rol nulo en el personal.
func contextoDelContrato(c auth.ContextoDeParte) api.Contexto {
	contexto := api.Contexto{
		ContextoId: c.ID,
		Tipo:       api.ContextoTipo(c.Tipo),
		Nombre:     c.Nombre,
	}
	if c.NIT != "" {
		contexto.Nit = &c.NIT
	}
	if c.Rol != "" {
		contexto.Rol = &c.Rol
	}
	return contexto
}
