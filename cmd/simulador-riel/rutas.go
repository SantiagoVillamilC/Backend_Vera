package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// maximoCuerpoOrden es el tamaño máximo que el simulador acepta en una orden o un control.
const maximoCuerpoOrden = 64 << 10

// Simulador sirve las rutas del riel simulado y guarda su propio registro de operaciones.
type Simulador struct {
	almacen *Almacen
	control *Control
	origen  string
	ahora   func() time.Time
}

// NuevoSimulador arma el simulador con su almacén y su control de perillas.
func NuevoSimulador(almacen *Almacen, control *Control, origen string) *Simulador {
	return &Simulador{almacen: almacen, control: control, origen: origen, ahora: time.Now}
}

// Rutas monta la recepción de órdenes, la consulta de estado y el control de perillas.
func (s *Simulador) Rutas() chi.Router {
	r := chi.NewRouter()
	r.Post("/transferencias", s.Enviar)
	r.Get("/transferencias/{id}", s.ConsultarEstado)
	r.Get("/control", s.VerControl)
	r.Put("/control", s.CambiarControl)
	return r
}

// Enviar recibe una orden, aplica la caída, la latencia y el rechazo, y responde o pierde la respuesta.
func (s *Simulador) Enviar(w http.ResponseWriter, r *http.Request) {
	orden, err := leerOrden(w, r)
	if err != nil {
		escribirError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	perillas := s.control.Actuales()
	slog.InfoContext(r.Context(), "orden recibida",
		"transferencia_id", orden.TransferenciaID,
		"monto_centavos", orden.MontoCentavos.Int64(),
		"perillas", perillas)
	if s.aplicarCaida(w, r, perillas) {
		return
	}
	if demora := perillas.Demora(); demora > 0 && !esperar(r.Context(), demora) {
		return
	}
	operacion := s.resolver(orden, perillas)
	registrada, reintento, err := s.almacen.Registrar(r.Context(), operacion, s.ahora())
	if errors.Is(err, ErrOperacionEnConflicto) {
		slog.WarnContext(r.Context(), "reenvio en conflicto", "transferencia_id", orden.TransferenciaID)
		escribirError(w, http.StatusConflict, "la transferencia ya existe con otros datos", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "registrar operación", "transferencia_id", orden.TransferenciaID, "err", err)
		escribirError(w, http.StatusInternalServerError, "no se pudo registrar la orden", nil)
		return
	}
	if ocurre(perillas.PRespuestaPerdida) {
		slog.WarnContext(r.Context(), "respuesta perdida", "transferencia_id", orden.TransferenciaID, "perillas", perillas)
		<-r.Context().Done()
		return
	}
	escribirJSON(w, http.StatusOK, respuestaDe(registrada, reintento))
}

// ConsultarEstado responde lo que el simulador registró para esa transferencia.
func (s *Simulador) ConsultarEstado(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		escribirError(w, http.StatusBadRequest, "falta el identificador de la transferencia", nil)
		return
	}
	operacion, err := s.almacen.PorTransferencia(r.Context(), id)
	if errors.Is(err, ErrOperacionNoEncontrada) {
		escribirError(w, http.StatusNotFound, "la transferencia no está registrada", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "leer la operación", "transferencia_id", id, "err", err)
		escribirError(w, http.StatusInternalServerError, "no se pudo leer la operación", nil)
		return
	}
	escribirJSON(w, http.StatusOK, respuestaDe(operacion, false))
}

// VerControl devuelve las perillas vigentes.
func (s *Simulador) VerControl(w http.ResponseWriter, _ *http.Request) {
	escribirJSON(w, http.StatusOK, aHTTP(s.control.Actuales()))
}

// CambiarControl reemplaza las perillas en caliente y registra los valores de la corrida.
func (s *Simulador) CambiarControl(w http.ResponseWriter, r *http.Request) {
	perillas, err := leerPerillas(w, r)
	if err != nil {
		escribirError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	antes, ahora := s.control.Fijar(perillas)
	slog.InfoContext(r.Context(), "perillas actualizadas", "antes", antes, "ahora", ahora)
	escribirJSON(w, http.StatusOK, aHTTP(ahora))
}

// aplicarCaida ejecuta el modo de caída y avisa si la petición ya quedó resuelta.
func (s *Simulador) aplicarCaida(w http.ResponseWriter, r *http.Request, p Perillas) bool {
	switch p.ModoCaida {
	case ModoCaidaConexionRechazada:
		slog.WarnContext(r.Context(), "caida: conexion rechazada", "perillas", p)
		cerrarSinRespuesta(w)
		return true
	case ModoCaidaSilencio:
		slog.WarnContext(r.Context(), "caida: silencio hasta el plazo", "perillas", p)
		<-r.Context().Done()
		return true
	default:
		return false
	}
}

// resolver decide el desenlace que el riel registra según la probabilidad de rechazo.
func (s *Simulador) resolver(orden ordenHTTP, p Perillas) Operacion {
	operacion := Operacion{
		TransferenciaID: orden.TransferenciaID,
		IdempotencyKey:  orden.IdempotencyKey,
		Llave:           orden.Llave,
		MontoCentavos:   orden.MontoCentavos,
		Moneda:          orden.Moneda,
		Estado:          estadoConfirmada,
		OcurridoEn:      s.ahora(),
	}
	if ocurre(p.PRechazo) {
		operacion.Estado = estadoRechazada
		operacion.Motivo = motivoFondosInsuficientes
	}
	return operacion
}

// esperar duerme la demora o se rinde si el cliente cancela, y avisa si hay que seguir.
func esperar(ctx context.Context, d time.Duration) bool {
	temporizador := time.NewTimer(d)
	defer temporizador.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-temporizador.C:
		return true
	}
}

// cerrarSinRespuesta corta la conexión sin escribir respuesta; el cliente lo ve como error de transporte.
func cerrarSinRespuesta(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		slog.Error("cerrar sin respuesta: la conexión no permite hijack")
		return
	}
	conexion, _, err := hijacker.Hijack()
	if err != nil {
		slog.Error("cerrar sin respuesta", "err", err)
		return
	}
	if tcp, ok := conexion.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conexion.Close()
}

// ordenHTTP es el cuerpo de una orden tal como viaja por el cable.
type ordenHTTP struct {
	TransferenciaID string
	IdempotencyKey  string
	Llave           string
	MontoCentavos   Centavos
	Moneda          string
}

// leerOrden decodifica y valida la orden; los campos faltantes se reportan juntos.
func leerOrden(w http.ResponseWriter, r *http.Request) (ordenHTTP, error) {
	var crudo struct {
		TransferenciaID string          `json:"transferencia_id"`
		IdempotencyKey  string          `json:"idempotency_key"`
		Llave           string          `json:"llave"`
		MontoCentavos   json.RawMessage `json:"monto_centavos"`
		Moneda          string          `json:"moneda"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximoCuerpoOrden)
	if err := json.NewDecoder(r.Body).Decode(&crudo); err != nil {
		return ordenHTTP{}, errors.New("el cuerpo no es una orden JSON válida")
	}

	orden := ordenHTTP{
		TransferenciaID: strings.TrimSpace(crudo.TransferenciaID),
		IdempotencyKey:  strings.TrimSpace(crudo.IdempotencyKey),
		Llave:           strings.TrimSpace(crudo.Llave),
		Moneda:          strings.TrimSpace(crudo.Moneda),
	}
	var faltantes []string
	if orden.TransferenciaID == "" {
		faltantes = append(faltantes, "transferencia_id")
	}
	if orden.IdempotencyKey == "" {
		faltantes = append(faltantes, "idempotency_key")
	}
	if orden.Llave == "" {
		faltantes = append(faltantes, "llave")
	}
	if orden.Moneda == "" {
		faltantes = append(faltantes, "moneda")
	}
	if len(crudo.MontoCentavos) == 0 {
		faltantes = append(faltantes, "monto_centavos")
	} else if monto, err := centavosDeJSON(crudo.MontoCentavos); err == nil {
		orden.MontoCentavos = monto
	} else {
		return ordenHTTP{}, err
	}
	if len(faltantes) > 0 {
		return ordenHTTP{}, errors.New("faltan campos obligatorios: " + strings.Join(faltantes, ", "))
	}
	if orden.MontoCentavos <= 0 {
		return ordenHTTP{}, ErrMontoInvalido
	}
	return orden, nil
}

// centavosDeJSON exige una cadena JSON de centavos enteros, nunca un número.
func centavosDeJSON(datos json.RawMessage) (Centavos, error) {
	var s string
	if err := json.Unmarshal(datos, &s); err != nil {
		return 0, ErrMontoInvalido
	}
	return ParsearCentavos(s)
}

// leerPerillas decodifica el cuerpo del control y valida las perillas.
func leerPerillas(w http.ResponseWriter, r *http.Request) (Perillas, error) {
	var crudo perillasHTTP
	r.Body = http.MaxBytesReader(w, r.Body, maximoCuerpoOrden)
	if err := json.NewDecoder(r.Body).Decode(&crudo); err != nil {
		return Perillas{}, errors.New("el cuerpo no es un control JSON válido")
	}
	return aPerillas(crudo)
}

// respuestaHTTP es la forma en el cable del desenlace de una transferencia.
type respuestaHTTP struct {
	TransferenciaID string    `json:"transferencia_id"`
	Resultado       string    `json:"resultado"`
	Motivo          string    `json:"motivo,omitempty"`
	OcurridoEn      time.Time `json:"ocurrido_en"`
	Reintento       bool      `json:"reintento"`
}

// respuestaDe lleva una operación registrada a su forma en el cable.
func respuestaDe(o Operacion, reintento bool) respuestaHTTP {
	return respuestaHTTP{
		TransferenciaID: o.TransferenciaID,
		Resultado:       o.Estado,
		Motivo:          o.Motivo,
		OcurridoEn:      o.OcurridoEn,
		Reintento:       reintento,
	}
}

// errorHTTP es el cuerpo de un rechazo, con los campos faltantes cuando aplica.
type errorHTTP struct {
	Error           string   `json:"error"`
	CamposFaltantes []string `json:"campos_faltantes,omitempty"`
}

// escribirJSON responde el cuerpo con su status.
func escribirJSON(w http.ResponseWriter, status int, cuerpo any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(cuerpo); err != nil {
		slog.Error("escribir respuesta", "err", err)
	}
}

// escribirError responde un error JSON.
func escribirError(w http.ResponseWriter, status int, mensaje string, faltantes []string) {
	escribirJSON(w, status, errorHTTP{Error: mensaje, CamposFaltantes: faltantes})
}
