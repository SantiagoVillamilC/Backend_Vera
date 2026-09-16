package nucleo

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// MonedaCOP es la moneda de la billetera y la que viaja en la respuesta.
const MonedaCOP = "COP"

// Límites del historial: página por defecto 50 y tope 100, como fija la unidad NF-A1-05.
const (
	limiteHistorialPorDefecto = 50
	limiteHistorialMaximo     = 100
)

// ErrServicioNulo reporta la construcción de un Handler sin el Servicio que necesita.
var ErrServicioNulo = errors.New("nucleo: NewHandler necesita un Servicio no nulo")

// RespuestaSaldo es el monto a favor en centavos, la moneda y el instante del cálculo.
type RespuestaSaldo struct {
	Monto       Centavos  `json:"monto"`
	Moneda      string    `json:"moneda"`
	CalculadoEn time.Time `json:"calculado_en"`
}

// Handler expone el saldo del usuario autenticado.
type Handler struct{ servicio *Servicio }

// NewHandler construye el handler sobre el servicio del núcleo y falla si el servicio es nulo.
func NewHandler(s *Servicio) *Handler {
	if s == nil {
		panic(ErrServicioNulo)
	}
	return &Handler{servicio: s}
}

// parametrosDeDestino son los nombres con los que un cliente intentaría apuntar a otra cuenta.
var parametrosDeDestino = []string{"usuario_id", "user_id", "cuenta_id"}

// Saldo responde el disponible del usuario autenticado, con el instante en que se calculó.
func (h *Handler) Saldo(w http.ResponseWriter, r *http.Request) {
	for _, p := range parametrosDeDestino {
		if r.URL.Query().Has(p) {
			api.WriteError(w, http.StatusBadRequest, "TargetNotAllowed",
				"This endpoint only returns the balance of the authenticated user.", nil)
			return
		}
	}
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return
	}

	monto, err := h.servicio.Saldo(r.Context(), NombreCuentaDisponible(usuarioID))
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, RespuestaSaldo{
		Monto:       monto,
		Moneda:      MonedaCOP,
		CalculadoEn: time.Now().UTC(),
	}, nil)
}

// Movimientos responde una página del historial del usuario autenticado, del más nuevo al más viejo.
func (h *Handler) Movimientos(w http.ResponseWriter, r *http.Request) {
	for _, p := range parametrosDeDestino {
		if r.URL.Query().Has(p) {
			api.WriteError(w, http.StatusBadRequest, "TargetNotAllowed",
				"This endpoint only returns the history of the authenticated user.", nil)
			return
		}
	}
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return
	}
	cursor, limite, mensaje := paginacionDeMovimientos(r)
	if mensaje != "" {
		api.WriteError(w, http.StatusBadRequest, "ValidationError", mensaje, nil)
		return
	}

	pagina, err := h.servicio.Historial(r.Context(), usuarioID, cursor, limite)
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, pagina.Movimientos, &api.Meta{
		Limit:      limite,
		NextCursor: pagina.Siguiente,
		HasMore:    pagina.HayMas,
	})
}

// paginacionDeMovimientos lee el cursor y el límite de la URL; un mensaje no vacío es un rechazo.
func paginacionDeMovimientos(r *http.Request) (*int64, int, string) {
	limite := limiteHistorialPorDefecto
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, 0, "limit must be a positive integer"
		}
		if n > limiteHistorialMaximo {
			n = limiteHistorialMaximo
		}
		limite = n
	}

	var cursor *int64
	if v := r.URL.Query().Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return nil, 0, "cursor must be the anchor entry id of the last movement"
		}
		cursor = &n
	}
	return cursor, limite, ""
}
