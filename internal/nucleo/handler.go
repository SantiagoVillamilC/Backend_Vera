package nucleo

import (
	"errors"
	"net/http"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// MonedaCOP es la moneda de la billetera y la que viaja en la respuesta.
const MonedaCOP = "COP"

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
