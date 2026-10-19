package nucleo

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// MonedaCOP es la moneda de la billetera y la que viaja en la respuesta.
const MonedaCOP = "COP"

// estadoConfirmacionRechazado es la señal del custodio que rechaza la intención sin asentarla.
const estadoConfirmacionRechazado = "rechazado"

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

// RespuestaMontoMaximo es el monto máximo enviable en centavos, la moneda y el instante del cálculo (HU-NF-06, criterio 14).
type RespuestaMontoMaximo struct {
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
	Estado     string    `json:"estado" validate:"required,oneof=aprobado rechazado"`
	Monto      Centavos  `json:"monto" validate:"required_if=Estado aprobado,gte=0"`
	Moneda     string    `json:"moneda"`
	OcurridoEn time.Time `json:"ocurrido_en" validate:"required_if=Estado aprobado"`
	CUS        string    `json:"cus" validate:"omitempty,max=128"`
}

// Handler sirve el saldo, el historial y las intenciones de depósito del usuario autenticado.
type Handler struct {
	servicio *Servicio
	pseURL   string
}

// NewHandler construye el handler sobre el servicio del núcleo y falla si el servicio es nulo.
func NewHandler(s *Servicio, pseURL ...string) *Handler {
	if s == nil {
		panic(ErrServicioNulo)
	}
	h := &Handler{servicio: s}
	if len(pseURL) > 0 {
		h.pseURL = pseURL[0]
	}
	return h
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

// MontoMaximo responde cuánto puede enviar el usuario sin que el saldo insuficiente por GMF lo sorprenda (HU-NF-06, criterio 14).
func (h *Handler) MontoMaximo(w http.ResponseWriter, r *http.Request) {
	for _, p := range parametrosDeDestino {
		if r.URL.Query().Has(p) {
			api.WriteError(w, http.StatusBadRequest, "TargetNotAllowed",
				"This endpoint only returns the maximum amount of the authenticated user.", nil)
			return
		}
	}
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return
	}

	momento := time.Now().UTC()
	monto, err := h.servicio.MontoMaximoEnviable(r.Context(), usuarioID, momento)
	if err != nil {
		api.WriteAPIError(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, RespuestaMontoMaximo{Monto: monto, Moneda: MonedaCOP, CalculadoEn: momento}, nil)
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
	api.WriteData(w, estado, respuestaDeDepositoConPago(intencion, h.pseURL), nil)
}

// respuestaDeDepositoConPago arma el DTO del POST con el enlace del PSE simulado.
func respuestaDeDepositoConPago(i IntencionDeposito, origen string) api.DepositoConPago {
	return api.DepositoConPago{
		Id:         i.ID,
		Referencia: i.Referencia,
		Monto:      strconv.FormatInt(i.Monto.Int64(), 10),
		Moneda:     api.DepositoConPagoMoneda(i.Moneda),
		Estado:     api.DepositoConPagoEstado(i.Estado),
		CreadaEn:   i.CreadaEn,
		UrlPago:    urlPagoPSE(origen, i),
	}
}

// urlPagoPSE usa los datos persistidos para que el reintento de la misma intencion repita el enlace.
func urlPagoPSE(origen string, i IntencionDeposito) string {
	return strings.TrimSuffix(origen, "/") + "/pse?referencia=" + url.QueryEscape(i.Referencia) +
		"&monto=" + strconv.FormatInt(i.Monto.Int64(), 10)
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

// ConfirmarDeposito responde 200 con la intención confirmada o rechazada, la primera vez y en el reenvío.
func (h *Handler) ConfirmarDeposito(w http.ResponseWriter, r *http.Request) {
	var req confirmarDepositoRequest
	if !api.DecodeAndValidate(w, r, &req) {
		return
	}

	var intencion IntencionDeposito
	var err error
	if estadoPersistido(req.Estado) == EstadoDepositoRechazada {
		intencion, err = h.servicio.RechazarDeposito(r.Context(), req.Referencia)
	} else {
		intencion, err = h.servicio.ConfirmarDeposito(r.Context(), ConfirmacionDeposito{
			Referencia: req.Referencia,
			Monto:      req.Monto,
			Moneda:     req.Moneda,
			OcurridoEn: req.OcurridoEn,
			CUS:        req.CUS,
		})
	}
	escribirRespuestaDeConfirmacion(w, r, intencion, err)
}

// estadoPersistido traduce el estado que envía el custodio al que persiste el ciclo de vida del depósito.
func estadoPersistido(estadoSenal string) string {
	if estadoSenal == estadoConfirmacionRechazado {
		return EstadoDepositoRechazada
	}
	return EstadoDepositoConfirmada
}

// escribirRespuestaDeConfirmacion traduce el resultado de confirmar o rechazar a la respuesta HTTP.
func escribirRespuestaDeConfirmacion(w http.ResponseWriter, r *http.Request, intencion IntencionDeposito, err error) {
	switch {
	case errors.Is(err, ErrDepositoNoEncontrado):
		api.WriteError(w, http.StatusNotFound, "DepositNotFound", "Deposit not found.", nil)
	case errors.Is(err, ErrConfirmacionConflictiva):
		api.WriteError(w, http.StatusConflict, "ConfirmationConflict", "Confirmation does not match the deposit.", nil)
	case errors.Is(err, ErrConfirmacionTardia):
		api.WriteError(w, http.StatusConflict, "LateConfirmation", "The deposit is no longer pending.", nil)
	case err != nil:
		api.WriteAPIError(w, r, err)
	default:
		api.WriteData(w, http.StatusOK, respuestaDeDeposito(intencion), nil)
	}
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

// crearTransferenciaRequest es el cuerpo de POST /transferencias: destino, monto en centavos y clave del cliente.
type crearTransferenciaRequest struct {
	Destino        string   `json:"destino" validate:"required,max=320"`
	Monto          Centavos `json:"monto" validate:"required,gt=0"`
	Moneda         string   `json:"moneda" validate:"omitempty,eq=COP"`
	IdempotencyKey string   `json:"idempotency_key" validate:"required,max=128"`
}

// RespuestaTransferencia es el envío interno que ve el cliente.
type RespuestaTransferencia struct {
	ID            string    `json:"id"`
	Destino       string    `json:"destino"`
	Monto         Centavos  `json:"monto"`
	Moneda        string    `json:"moneda"`
	Estado        string    `json:"estado"`
	CreadaEn      time.Time `json:"creada_en"`
	ActualizadaEn time.Time `json:"actualizada_en"`
}

// Transferir responde 201 con el envío nuevo y 200 con el ya guardado para la misma clave.
func (h *Handler) Transferir(w http.ResponseWriter, r *http.Request) {
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return
	}
	var req crearTransferenciaRequest
	if !api.DecodeAndValidate(w, r, &req) {
		return
	}

	transferencia, creada, err := h.servicio.Transferir(r.Context(), SolicitudTransferencia{
		UsuarioID:      usuarioID,
		CuentaOrigen:   NombreCuentaDisponible(usuarioID),
		LlaveDestino:   req.Destino,
		Monto:          req.Monto,
		Moneda:         req.Moneda,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		escribirErrorDeTransferencia(w, r, err)
		return
	}
	estado := http.StatusOK
	if creada {
		estado = http.StatusCreated
	}
	api.WriteData(w, estado, respuestaDeTransferencia(transferencia), nil)
}

// escribirErrorDeTransferencia traduce el rechazo del envío al envelope con su código estable.
func escribirErrorDeTransferencia(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrDestinoObligatorio):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "The destination is required.", nil)
	case errors.Is(err, ErrMontoInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "The amount is not a positive integer of minor units.", nil)
	case errors.Is(err, ErrMonedaInvalida):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "The currency must be COP.", nil)
	case errors.Is(err, ErrDestinoNoExiste):
		api.WriteError(w, http.StatusNotFound, "RecipientNotFound", "The destination does not exist.", nil)
	case errors.Is(err, ErrDestinoPropio):
		api.WriteError(w, http.StatusUnprocessableEntity, "SelfTransferNotAllowed", "You cannot send money to yourself.", nil)
	case errors.Is(err, ErrSaldoInsuficiente):
		api.WriteError(w, http.StatusUnprocessableEntity, "InsufficientFunds", "Insufficient funds.", nil)
	case errors.Is(err, ErrTopeOperacionExcedido):
		api.WriteError(w, http.StatusUnprocessableEntity, "OperationLimitExceeded", "The amount exceeds the low-balance deposit limit.", nil)
	case errors.Is(err, ErrCupoMensualExcedido):
		api.WriteError(w, http.StatusUnprocessableEntity, "MonthlyQuotaExceeded", "The monthly low-balance deposit quota is exhausted.", nil)
	case errors.Is(err, ErrIdempotenciaEnConflicto):
		api.WriteError(w, http.StatusConflict, "IdempotencyConflict", "The idempotency key was reused with a different request.", nil)
	default:
		api.WriteAPIError(w, r, err)
	}
}

// respuestaDeTransferencia traduce la transferencia del núcleo al DTO de la API.
func respuestaDeTransferencia(t Transferencia) RespuestaTransferencia {
	return RespuestaTransferencia{
		ID:            t.ID.String(),
		Destino:       t.LlaveDestino,
		Monto:         t.Monto,
		Moneda:        t.Moneda,
		Estado:        t.Estado,
		CreadaEn:      t.CreadaEn,
		ActualizadaEn: t.ActualizadaEn,
	}
}

// crearTransferenciaInterbancariaRequest es el cuerpo de POST /transferencias/interbancarias: la llave, su tipo, el monto y la clave del cliente.
type crearTransferenciaInterbancariaRequest struct {
	TipoLlave      string   `json:"tipo_llave" validate:"required"`
	Llave          string   `json:"llave" validate:"required,max=320"`
	Monto          Centavos `json:"monto" validate:"required,gt=0"`
	Moneda         string   `json:"moneda" validate:"omitempty,eq=COP"`
	IdempotencyKey string   `json:"idempotency_key" validate:"required,max=128"`
}

// RespuestaEnvioPorLlave es el envío por llave en proceso que ve el cliente.
type RespuestaEnvioPorLlave struct {
	ID            string    `json:"id"`
	TipoLlave     string    `json:"tipo_llave"`
	Llave         string    `json:"llave"`
	Monto         Centavos  `json:"monto"`
	Moneda        string    `json:"moneda"`
	Estado        string    `json:"estado"`
	CreadaEn      time.Time `json:"creada_en"`
	ActualizadaEn time.Time `json:"actualizada_en"`
}

// TransferirInterbancaria responde 201 con el envío por llave nuevo y 200 con el ya guardado para la misma clave.
func (h *Handler) TransferirInterbancaria(w http.ResponseWriter, r *http.Request) {
	usuarioID, ok := identidad.UsuarioIDDeContexto(r.Context())
	if !ok {
		api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
		return
	}
	var req crearTransferenciaInterbancariaRequest
	if !api.DecodeAndValidate(w, r, &req) {
		return
	}

	transferencia, creada, err := h.servicio.TransferirPorLlave(r.Context(), SolicitudEnvioPorLlave{
		UsuarioID:      usuarioID,
		CuentaOrigen:   NombreCuentaDisponible(usuarioID),
		TipoLlave:      req.TipoLlave,
		Llave:          req.Llave,
		Monto:          req.Monto,
		Moneda:         req.Moneda,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		escribirErrorDeEnvioPorLlave(w, r, err)
		return
	}
	estado := http.StatusOK
	if creada {
		estado = http.StatusCreated
	}
	api.WriteData(w, estado, respuestaDeEnvioPorLlave(transferencia, req.TipoLlave), nil)
}

// escribirErrorDeEnvioPorLlave traduce el rechazo del envío por llave al envelope con su código estable.
func escribirErrorDeEnvioPorLlave(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrLlaveInvalida):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "The destination key is not valid.", nil)
	case errors.Is(err, ErrMontoInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "The amount is not a positive integer of minor units.", nil)
	case errors.Is(err, ErrMonedaInvalida):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "The currency must be COP.", nil)
	case errors.Is(err, ErrLlaveNoEncontrada):
		api.WriteError(w, http.StatusNotFound, "RecipientNotFound", "The destination key does not exist.", nil)
	case errors.Is(err, ErrDirectorioSinRespuesta), errors.Is(err, ErrDirectorioNoDisponible):
		api.WriteError(w, http.StatusServiceUnavailable, "DirectoryUnavailable", "The key directory is temporarily unavailable.", nil)
	case errors.Is(err, ErrSaldoInsuficiente):
		api.WriteError(w, http.StatusUnprocessableEntity, "InsufficientFunds", "Insufficient funds.", nil)
	case errors.Is(err, ErrTopeOperacionExcedido):
		api.WriteError(w, http.StatusUnprocessableEntity, "OperationLimitExceeded", "The amount exceeds the low-balance deposit limit.", nil)
	case errors.Is(err, ErrCupoMensualExcedido):
		api.WriteError(w, http.StatusUnprocessableEntity, "MonthlyQuotaExceeded", "The monthly low-balance deposit quota is exhausted.", nil)
	case errors.Is(err, ErrIdempotenciaEnConflicto):
		api.WriteError(w, http.StatusConflict, "IdempotencyConflict", "The idempotency key was reused with a different request.", nil)
	default:
		api.WriteAPIError(w, r, err)
	}
}

// respuestaDeEnvioPorLlave traduce la transferencia del núcleo al DTO de la API, con el tipo de llave de la petición.
func respuestaDeEnvioPorLlave(t Transferencia, tipoLlave string) RespuestaEnvioPorLlave {
	return RespuestaEnvioPorLlave{
		ID:            t.ID.String(),
		TipoLlave:     tipoLlave,
		Llave:         t.LlaveDestino,
		Monto:         t.Monto,
		Moneda:        t.Moneda,
		Estado:        t.Estado,
		CreadaEn:      t.CreadaEn,
		ActualizadaEn: t.ActualizadaEn,
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
