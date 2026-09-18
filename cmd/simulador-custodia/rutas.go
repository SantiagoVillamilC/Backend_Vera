package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// maximoCuerpoNotificacion es el tamaño máximo que el simulador acepta en una notificación.
const maximoCuerpoNotificacion = 64 << 10

// ErrMontoNotificacion reporta un monto que no es un entero positivo de centavos.
var ErrMontoNotificacion = errors.New("el monto no es un entero positivo en centavos")

// Simulador sirve las tres rutas del banco custodio simulado.
type Simulador struct {
	almacen *Almacen
	origen  string
	ahora   func() time.Time
}

// NuevoSimulador arma el simulador con su almacén y el identificador con que se nombra a sí mismo.
func NuevoSimulador(almacen *Almacen, origen string) *Simulador {
	return &Simulador{almacen: almacen, origen: origen, ahora: time.Now}
}

// Rutas monta las dos notificaciones y la consulta del extracto.
func (s *Simulador) Rutas() chi.Router {
	r := chi.NewRouter()
	r.Post("/notificaciones/depositos", s.Deposito)
	r.Post("/notificaciones/retiros", s.Retiro)
	r.Get("/extracto", s.Extracto)
	return r
}

// Deposito registra una notificación de depósito.
func (s *Simulador) Deposito(w http.ResponseWriter, r *http.Request) {
	s.notificar(w, r, tipoDeposito)
}

// Retiro registra una notificación de retiro, aunque hoy ninguna historia lo invoque.
func (s *Simulador) Retiro(w http.ResponseWriter, r *http.Request) {
	s.notificar(w, r, tipoRetiro)
}

// notificar valida la notificación, la registra con idempotencia por referencia y responde el movimiento.
func (s *Simulador) notificar(w http.ResponseWriter, r *http.Request, tipo string) {
	nota, faltantes, err := leerNotificacion(w, r)
	if err != nil {
		escribirError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len(faltantes) > 0 {
		escribirError(w, http.StatusBadRequest, "faltan campos obligatorios", faltantes)
		return
	}
	movimiento, reintento, err := s.almacen.Registrar(r.Context(), Movimiento{
		Referencia:    nota.Referencia,
		Tipo:          tipo,
		MontoCentavos: nota.MontoCentavos,
		OcurridoEn:    nota.OcurridoEn,
	}, s.ahora())
	if errors.Is(err, ErrReferenciaEnConflicto) {
		escribirError(w, http.StatusConflict, "la referencia ya existe con otro monto o tipo", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "registrar movimiento", "referencia", nota.Referencia, "err", err)
		escribirError(w, http.StatusInternalServerError, "no se pudo registrar el movimiento", nil)
		return
	}
	escribirJSON(w, http.StatusOK, respuestaMovimiento{
		ID:            movimiento.ID,
		Referencia:    movimiento.Referencia,
		Tipo:          movimiento.Tipo,
		MontoCentavos: movimiento.MontoCentavos,
		OcurridoEn:    movimiento.OcurridoEn,
		RegistradoEn:  movimiento.RegistradoEn,
		Reintento:     reintento,
	})
}

// notificacion es el cuerpo ya validado de una notificación.
type notificacion struct {
	Referencia    string
	MontoCentavos Centavos
	OcurridoEn    time.Time
}

// leerNotificacion decodifica y valida el cuerpo; los campos faltantes se reportan juntos.
func leerNotificacion(w http.ResponseWriter, r *http.Request) (notificacion, []string, error) {
	var crudo struct {
		Referencia    string          `json:"referencia"`
		MontoCentavos json.RawMessage `json:"monto_centavos"`
		OcurridoEn    *time.Time      `json:"ocurrido_en"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximoCuerpoNotificacion)
	if err := json.NewDecoder(r.Body).Decode(&crudo); err != nil {
		return notificacion{}, nil, errors.New("el cuerpo no es una notificación JSON válida")
	}

	nota := notificacion{Referencia: strings.TrimSpace(crudo.Referencia)}
	faltantes := make([]string, 0, 3)
	if nota.Referencia == "" {
		faltantes = append(faltantes, "referencia")
	}
	if len(crudo.MontoCentavos) == 0 {
		faltantes = append(faltantes, "monto_centavos")
	} else if monto, err := centavosDeJSON(crudo.MontoCentavos); err == nil {
		nota.MontoCentavos = monto
	}
	if crudo.OcurridoEn == nil || crudo.OcurridoEn.IsZero() {
		faltantes = append(faltantes, "ocurrido_en")
	} else {
		nota.OcurridoEn = crudo.OcurridoEn.UTC()
	}
	if len(faltantes) > 0 {
		return nota, faltantes, nil
	}
	if nota.MontoCentavos <= 0 {
		return nota, nil, ErrMontoNotificacion
	}
	return nota, nil, nil
}

// centavosDeJSON exige una cadena JSON de centavos enteros, nunca un número.
func centavosDeJSON(datos json.RawMessage) (Centavos, error) {
	var s string
	if err := json.Unmarshal(datos, &s); err != nil {
		return 0, ErrMontoInvalido
	}
	return ParsearCentavos(s)
}

// Extracto responde el corte completo al instante pedido y rechaza un corte futuro.
func (s *Simulador) Extracto(w http.ResponseWriter, r *http.Request) {
	corte, err := time.Parse(time.RFC3339, r.URL.Query().Get("corte_en"))
	if err != nil {
		escribirError(w, http.StatusBadRequest, "corte_en debe ser un instante RFC3339", nil)
		return
	}
	corte = corte.UTC()
	if corte.After(s.ahora()) {
		escribirError(w, http.StatusBadRequest, "corte_en no puede ser futuro", nil)
		return
	}
	saldo, err := s.almacen.SaldoHasta(r.Context(), corte)
	if err != nil {
		slog.ErrorContext(r.Context(), "calcular el saldo", "err", err)
		escribirError(w, http.StatusInternalServerError, "no se pudo calcular el saldo", nil)
		return
	}
	escribirJSON(w, http.StatusOK, extractoHTTP{CorteEn: corte, SaldoCentavos: saldo, Origen: s.origen})
}

// extractoHTTP es el corte completo que viaja por el cable.
type extractoHTTP struct {
	CorteEn       time.Time `json:"corte_en"`
	SaldoCentavos Centavos  `json:"saldo_centavos"`
	Origen        string    `json:"origen"`
}

// respuestaMovimiento es la respuesta de una notificación, idéntica en un reenvío salvo la marca.
type respuestaMovimiento struct {
	ID            int64     `json:"id"`
	Referencia    string    `json:"referencia"`
	Tipo          string    `json:"tipo"`
	MontoCentavos Centavos  `json:"monto_centavos"`
	OcurridoEn    time.Time `json:"ocurrido_en"`
	RegistradoEn  time.Time `json:"registrado_en"`
	Reintento     bool      `json:"reintento"`
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
