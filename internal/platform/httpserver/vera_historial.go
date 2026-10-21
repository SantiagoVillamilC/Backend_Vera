package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

func consultaHistorial(w http.ResponseWriter, limite, cursor *string) (copilot.ConsultaHistorial, bool) {
	p := copilot.ConsultaHistorial{Limite: 50, Cursor: cursor}
	if limite != nil {
		n, err := strconv.Atoi(*limite)
		if err != nil || n < 1 || n > 100 {
			errorHistorialVera(w, copilot.ErrPaginaHistorial)
			return p, false
		}
		p.Limite = n
	}
	return p, true
}

func (s servidorV1) historialDisponible(w http.ResponseWriter) bool {
	if s.Vera.Historial == nil {
		errorHistorialVera(w, copilot.ErrNoDisponibleIA)
		return false
	}
	// Acceder y borrar el historial sigue disponible tras revocar el consentimiento de IA.
	return true
}

func (s servidorV1) ListarConversacionesVera(w http.ResponseWriter, r *http.Request, params api.ListarConversacionesVeraParams) {
	p, ok := consultaHistorial(w, params.Limit, params.Cursor)
	if !ok || !s.historialDisponible(w) {
		return
	}
	pagina, err := s.Vera.Historial.ListarConversaciones(r.Context(), p)
	if err != nil {
		errorHistorialVera(w, err)
		return
	}
	zona := time.FixedZone("America/Bogota", -5*60*60)
	datos := make([]api.ConversacionVera, 0, len(pagina.Elementos))
	for _, c := range pagina.Elementos {
		datos = append(datos, api.ConversacionVera{Id: c.ID, Titulo: c.Titulo, CreadaEn: c.CreadaEn.In(zona), UltimaActividad: c.UltimaActividad.In(zona)})
	}
	api.WriteDataHistorialVera(w, 200, datos, &api.MetaHistorialVera{Limit: pagina.Limite, HasMore: pagina.Siguiente != nil, NextCursor: pagina.Siguiente})
}

func (s servidorV1) ListarMensajesConversacionVera(w http.ResponseWriter, r *http.Request, id uuid.UUID, params api.ListarMensajesConversacionVeraParams) {
	p, ok := consultaHistorial(w, params.Limit, params.Cursor)
	if !ok || !s.historialDisponible(w) {
		return
	}
	pagina, err := s.Vera.Historial.ListarMensajes(r.Context(), id, p)
	if err != nil {
		errorHistorialVera(w, err)
		return
	}
	zona := time.FixedZone("America/Bogota", -5*60*60)
	datos := make([]api.MensajeVera, 0, len(pagina.Elementos))
	for _, m := range pagina.Elementos {
		datos = append(datos, api.MensajeVera{Id: m.ID, Rol: api.MensajeVeraRol(m.Rol), Canal: api.MensajeVeraCanal(m.Canal), Texto: m.Texto, CreadoEn: m.CreadoEn.In(zona), AccionPendiente: tarjetaVeraOpcional(m.Accion, zona)})
	}
	api.WriteDataHistorialVera(w, 200, datos, &api.MetaHistorialVera{Limit: pagina.Limite, HasMore: pagina.Siguiente != nil, NextCursor: pagina.Siguiente})
}

func (s servidorV1) BorrarConversacionVera(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if !s.historialDisponible(w) {
		return
	}
	if err := s.Vera.Historial.BorrarConversacion(r.Context(), id); err != nil {
		errorHistorialVera(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s servidorV1) BorrarConversacionesVera(w http.ResponseWriter, r *http.Request) {
	if !s.historialDisponible(w) {
		return
	}
	if err := s.Vera.Historial.BorrarConversaciones(r.Context()); err != nil {
		errorHistorialVera(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func errorHistorialVera(w http.ResponseWriter, err error) {
	if errors.Is(err, copilot.ErrPaginaHistorial) {
		api.WriteError(w, 400, "BadRequest", "El límite debe estar entre 1 y 100 y el cursor debe pertenecer a esta lista.", nil)
		return
	}
	errorTextoVera(w, err)
}
