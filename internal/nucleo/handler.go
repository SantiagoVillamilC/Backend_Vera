package nucleo

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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

// crearDepositoRequest es el cuerpo de POST /depositos: monto en centavos, moneda COP y clave del cliente.
type crearDepositoRequest struct {
	Monto  Centavos `json:"monto" validate:"required,gt=0"`
	Moneda string   `json:"moneda" validate:"omitempty,eq=COP"`
	// Clave del cliente acotada a 128 caracteres: cabe en el índice btree de (usuario_id, idempotency_key).
	IdempotencyKey string `json:"idempotency_key" validate:"required,max=128"`
}

// RespuestaDeposito es la intención de depósito que ve el cliente.
type RespuestaDeposito struct {
	ID         string    `json:"id"`
	Referencia string    `json:"referencia"`
	Monto      Centavos  `json:"monto"`
	Moneda     string    `json:"moneda"`
	Estado     string    `json:"estado"`
	CreadaEn   time.Time `json:"creada_en"`
}

// confirmarDepositoRequest es la señal del custodio simulado sobre una intención pendiente.
type confirmarDepositoRequest struct {
	Referencia string    `json:"referencia" validate:"required"`
	Estado     string    `json:"estado" validate:"required,oneof=aprobado"`
	Monto      Centavos  `json:"monto" validate:"required,gt=0"`
	Moneda     string    `json:"moneda"`
	OcurridoEn time.Time `json:"ocurrido_en" validate:"required"`
}

// Handler sirve el saldo, el historial y las intenciones de depósito del usuario autenticado.
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

// CrearDeposito responde 201 con la intención nueva y 200 con la ya guardada para la misma clave.
func (h *Handler) CrearDeposito(w http.ResponseWriter, r *http.Request) {
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return
	}
	var req crearDepositoRequest
	if !api.DecodeAndValidate(w, r, &req) {
		return
	}

	intencion, creada, err := h.servicio.CrearIntencionDeposito(r.Context(), usuarioID, req.Monto, req.Moneda, req.IdempotencyKey)
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	estado := http.StatusOK
	if creada {
		estado = http.StatusCreated
	}
	api.WriteData(w, estado, respuestaDeDeposito(intencion), nil)
}

// Deposito responde el estado actual de la intención del usuario autenticado.
func (h *Handler) Deposito(w http.ResponseWriter, r *http.Request) {
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NotFound", "Resource not found.", nil)
		return
	}

	intencion, err := h.servicio.ConsultarIntencionDeposito(r.Context(), usuarioID, id)
	if errors.Is(err, ErrDepositoNoEncontrado) {
		api.WriteError(w, http.StatusNotFound, "NotFound", "Resource not found.", nil)
		return
	}
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, respuestaDeDeposito(intencion), nil)
}

// ConfirmarDeposito responde 200 con la intención confirmada, la primera vez y en el reenvío.
func (h *Handler) ConfirmarDeposito(w http.ResponseWriter, r *http.Request) {
	var req confirmarDepositoRequest
	if !api.DecodeAndValidate(w, r, &req) {
		return
	}

	intencion, err := h.servicio.ConfirmarDeposito(r.Context(), ConfirmacionDeposito{
		Referencia: req.Referencia,
		Monto:      req.Monto,
		Moneda:     req.Moneda,
		OcurridoEn: req.OcurridoEn,
	})
	switch {
	case errors.Is(err, ErrDepositoNoEncontrado):
		api.WriteError(w, http.StatusNotFound, "DepositNotFound", "Deposit not found.", nil)
		return
	case errors.Is(err, ErrConfirmacionConflictiva):
		api.WriteError(w, http.StatusConflict, "ConfirmationConflict", "Confirmation does not match the deposit.", nil)
		return
	case errors.Is(err, ErrConfirmacionTardia):
		api.WriteError(w, http.StatusConflict, "LateConfirmation", "The deposit is no longer pending.", nil)
		return
	case err != nil:
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, respuestaDeDeposito(intencion), nil)
}

// respuestaDeDeposito traduce la intención del núcleo al DTO de la API.
func respuestaDeDeposito(i IntencionDeposito) RespuestaDeposito {
	return RespuestaDeposito{
		ID:         i.ID.String(),
		Referencia: i.Referencia,
		Monto:      i.Monto,
		Moneda:     i.Moneda,
		Estado:     i.Estado,
		CreadaEn:   i.CreadaEn,
	}
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
