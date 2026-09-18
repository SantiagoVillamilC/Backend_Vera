package main

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// maximoCuerpoNotificacion es el tamaño máximo que el simulador acepta en una notificación.
const maximoCuerpoNotificacion = 64 << 10

// maximoCuerpoPerillas es el tamaño máximo que el control acepta en un cambio de perillas.
const maximoCuerpoPerillas = 4 << 10

// topeEsperaSilencio queda sobre los 10s del cliente y bajo el WriteTimeout de 30s para que el 504 de respaldo sea alcanzable.
const topeEsperaSilencio = 25 * time.Second

// ErrMontoNotificacion reporta un monto que no es un entero positivo de centavos.
var ErrMontoNotificacion = errors.New("el monto no es un entero positivo en centavos")

// Simulador sirve las rutas del banco custodio simulado y su control de perillas.
type Simulador struct {
	almacen    *Almacen
	origen     string
	ahora      func() time.Time
	perillas   *ControlPerillas
	sorteo     func() float64
	topeEspera time.Duration
}

// NuevoSimulador arma el simulador con su almacén, su nombre, el control de perillas y el tope de espera de producción.
func NuevoSimulador(almacen *Almacen, origen string, perillas *ControlPerillas) *Simulador {
	return &Simulador{almacen: almacen, origen: origen, ahora: time.Now, perillas: perillas, sorteo: rand.Float64, topeEspera: topeEsperaSilencio}
}

// Rutas monta las dos notificaciones, la consulta del extracto y el control de perillas.
func (s *Simulador) Rutas() chi.Router {
	r := chi.NewRouter()
	r.Post("/notificaciones/depositos", s.Deposito)
	r.Post("/notificaciones/retiros", s.Retiro)
	r.Get("/extracto", s.Extracto)
	r.Get("/control/perillas", s.VerPerillas)
	r.Put("/control/perillas", s.CambiarPerillas)
	return r
}

// fallasDeNegocio aplica modo de caída, rechazo y latencia, y dice si la petición ya se atendió.
func (s *Simulador) fallasDeNegocio(w http.ResponseWriter, r *http.Request) bool {
	if s.fallasSinLatencia(w, r) {
		return true
	}
	s.esperarLatencia(r, s.perillas.Vigentes())
	return false
}

// fallasSinLatencia aplica modo de caída y rechazo sin leer el cuerpo, y dice si la petición ya se atendió.
func (s *Simulador) fallasSinLatencia(w http.ResponseWriter, r *http.Request) bool {
	p := s.perillas.Vigentes()
	switch p.ModoCaida {
	case modoRechaza:
		cortarConexion(w)
		return true
	case modoSilencio:
		s.esperarAlCliente(w, r)
		return true
	}
	if s.falla(p.PRechazo) {
		escribirError(w, http.StatusServiceUnavailable, "el simulador rechazó la petición", nil)
		return true
	}
	return false
}

// falla sortea contra una probabilidad, con 0 nunca falla y con 1 siempre falla.
func (s *Simulador) falla(probabilidad float64) bool { return s.sorteo() < probabilidad }

// esperarLatencia duerme la latencia base más el jitter uniforme, sin pasarse del plazo del cliente.
func (s *Simulador) esperarLatencia(r *http.Request, p Perillas) {
	espera := time.Duration(p.LatenciaMS) * time.Millisecond
	if p.LatenciaVarMS > 0 {
		espera += time.Duration(s.sorteo()*float64(p.LatenciaVarMS)) * time.Millisecond
	}
	if espera <= 0 {
		return
	}
	temporizador := time.NewTimer(espera)
	defer temporizador.Stop()
	select {
	case <-temporizador.C:
	case <-r.Context().Done():
	}
}

// cortarConexion cierra el socket sin responder, que es la caída por conexión rechazada.
func cortarConexion(w http.ResponseWriter) {
	if !secuestrarYCerrar(w) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}

// secuestrarYCerrar le quita la conexión a net/http y la cierra, y dice si pudo.
func secuestrarYCerrar(w http.ResponseWriter) bool {
	secuestrador, ok := w.(http.Hijacker)
	if !ok {
		return false
	}
	conn, _, err := secuestrador.Hijack()
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// esperarAlCliente aguarda a que el cliente se rinda o vence su tope, y al vencer lo corta en vez de dejar un 200 vacío.
func (s *Simulador) esperarAlCliente(w http.ResponseWriter, r *http.Request) {
	temporizador := time.NewTimer(s.topeEspera)
	defer temporizador.Stop()
	select {
	case <-r.Context().Done():
	case <-temporizador.C:
		if !secuestrarYCerrar(w) {
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}
}

// VerPerillas responde las seis perillas vigentes.
func (s *Simulador) VerPerillas(w http.ResponseWriter, r *http.Request) {
	escribirJSON(w, http.StatusOK, s.perillas.Vigentes())
}

// CambiarPerillas aplica un cambio parcial en caliente y responde las perillas resultantes.
func (s *Simulador) CambiarPerillas(w http.ResponseWriter, r *http.Request) {
	var parcial perillasParciales
	r.Body = http.MaxBytesReader(w, r.Body, maximoCuerpoPerillas)
	decodificador := json.NewDecoder(r.Body)
	decodificador.DisallowUnknownFields()
	if err := decodificador.Decode(&parcial); err != nil {
		escribirError(w, http.StatusBadRequest, "el cuerpo no es un cambio de perillas JSON válido", nil)
		return
	}
	if err := decodificador.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		escribirError(w, http.StatusBadRequest, "el cuerpo trae datos sobrantes tras el JSON", nil)
		return
	}
	actuales, err := s.perillas.Aplicar(parcial)
	if err != nil {
		escribirError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	slog.InfoContext(r.Context(), "perillas cambiadas por el control", "perillas", actuales)
	escribirJSON(w, http.StatusOK, actuales)
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
	if s.fallasSinLatencia(w, r) {
		return
	}
	nota, faltantes, err := leerNotificacion(w, r)
	if err != nil {
		escribirError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len(faltantes) > 0 {
		escribirError(w, http.StatusBadRequest, "faltan campos obligatorios", faltantes)
		return
	}
	s.esperarLatencia(r, s.perillas.Vigentes())
	if s.falla(s.perillas.Vigentes().PNotificacionPerdida) {
		escribirJSON(w, http.StatusOK, respuestaDescartada{Descartada: true})
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
	if s.falla(s.perillas.Vigentes().PRespuestaPerdida) {
		s.esperarAlCliente(w, r)
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

// respuestaDescartada acepta una notificación que la perilla de notificación perdida descartó.
type respuestaDescartada struct {
	Descartada bool `json:"descartada"`
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
	if s.fallasDeNegocio(w, r) {
		return
	}
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
