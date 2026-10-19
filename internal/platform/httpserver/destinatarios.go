package httpserver

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// ConsultadorDestinatarios resuelve el nombre visible de un destino interno en la base de auth.
type ConsultadorDestinatarios interface {
	NombreDeDestinoPorCelular(ctx context.Context, celular, usuarioID string) (nombre string, existe, propio bool, err error)
}

// DestinatariosHandler limita consultas por usuario antes de leer el destino.
type DestinatariosHandler struct {
	consultador ConsultadorDestinatarios
	contadores  auth.Contadores
	claves      *auth.Claves
	limite      int64
	ventana     time.Duration
}

// NuevoDestinatariosHandler exige las dependencias necesarias para cerrar ante fallos.
func NuevoDestinatariosHandler(consultador ConsultadorDestinatarios, contadores auth.Contadores, claves *auth.Claves, limite int, ventana time.Duration) *DestinatariosHandler {
	if consultador == nil || contadores == nil || claves == nil || limite <= 0 || ventana <= 0 {
		panic("destinatarios: configuracion incompleta")
	}
	return &DestinatariosHandler{consultador: consultador, contadores: contadores, claves: claves, limite: int64(limite), ventana: ventana}
}

// ConsultarDestinatario devuelve solo el nombre visible de una cuenta interna.
func (h *DestinatariosHandler) ConsultarDestinatario(w http.ResponseWriter, r *http.Request) {
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok || usuarioID == "" {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return
	}
	celular := strings.TrimSpace(r.URL.Query().Get("celular"))
	if celular == "" {
		api.WriteError(w, http.StatusBadRequest, "BadRequest", "A required parameter is missing.", []api.FieldError{{Field: "celular", Rule: "required"}})
		return
	}
	clave := "destinatarios:usuario:" + h.claves.ClaveDeParte(usuarioID)
	conteo, err := h.contadores.Incrementar(r.Context(), clave, h.ventana)
	if err != nil {
		api.WriteError(w, http.StatusServiceUnavailable, "AuthUnavailable", "Recipient lookup is temporarily unavailable.", nil)
		return
	}
	if conteo > h.limite {
		api.WriteError(w, http.StatusTooManyRequests, "TooManyRequests", "Too many recipient lookups.", nil)
		return
	}
	nombre, existe, propio, err := h.consultador.NombreDeDestinoPorCelular(r.Context(), celular, usuarioID)
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	if !existe {
		api.WriteError(w, http.StatusNotFound, "RecipientNotFound", "The destination does not exist.", nil)
		return
	}
	if propio {
		api.WriteError(w, http.StatusUnprocessableEntity, "SelfTransferNotAllowed", "You cannot send money to yourself.", nil)
		return
	}
	api.WriteData(w, http.StatusOK, api.Destinatario{Nombre: nombre}, nil)
}
