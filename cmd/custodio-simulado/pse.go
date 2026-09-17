package main

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/google/uuid"
)

// El tope de peticiones del PSE simulado, la ventana del limitador por IP y el tope del cuerpo.
const (
	topePeticionesPSE    = 30
	ventanaPeticionesPSE = time.Minute
	maximoCuerpoPSE      = 64 << 10
)

// PaginaPSE sirve el flujo de PSE simulado y enruta la respuesta del usuario al despachador.
type PaginaPSE struct {
	despachador *Despachador
	ahora       func() time.Time
	nuevaRef    func() string
}

// NewPaginaPSE deja el reloj y la referencia con sus valores por defecto.
func NewPaginaPSE(despachador *Despachador) *PaginaPSE {
	return &PaginaPSE{
		despachador: despachador,
		ahora:       time.Now,
		nuevaRef:    func() string { return uuid.NewString() },
	}
}

// Rutas monta la página de PSE y los dos desenlaces del usuario.
func (p *PaginaPSE) Rutas() chi.Router {
	r := chi.NewRouter()
	r.Use(httprate.LimitBy(topePeticionesPSE, ventanaPeticionesPSE, claveDeIP, httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "demasiadas peticiones al PSE simulado", http.StatusTooManyRequests)
	})))
	r.Get("/pse", p.Formulario)
	r.Post("/pse/autorizar", p.Autorizar)
	r.Post("/pse/cancelar", p.Cancelar)
	return r
}

// claveDeIP agrupa el rate limit por la IP del cliente, sin el puerto.
func claveDeIP(r *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return httprate.CanonicalizeIP(host), nil
}

// Formulario muestra la redirección simulada con la referencia y el monto de la intención.
func (p *PaginaPSE) Formulario(w http.ResponseWriter, r *http.Request) {
	referencia := strings.TrimSpace(r.URL.Query().Get("referencia"))
	monto, err := montoDeTexto(r.URL.Query().Get("monto"))
	if referencia == "" || err != nil {
		http.Error(w, "referencia y monto (en centavos) son obligatorios", http.StatusBadRequest)
		return
	}
	escribirPagina(w, plantillaPSE, datosPSE{Referencia: referencia, Monto: monto, Presentado: presentarCentavos(monto)})
}

// Autorizar registra el evento aprobado con un CUS nuevo y dispara sus dos patas.
func (p *PaginaPSE) Autorizar(w http.ResponseWriter, r *http.Request) {
	p.resolver(w, r, estadoAprobado, "cus-"+p.nuevaRef())
}

// Cancelar registra el evento rechazado, sin CUS, y dispara la señal de rechazo.
func (p *PaginaPSE) Cancelar(w http.ResponseWriter, r *http.Request) {
	p.resolver(w, r, estadoRechazado, "")
}

// resolver guarda el evento del usuario y avisa al despachador.
func (p *PaginaPSE) resolver(w http.ResponseWriter, r *http.Request, estado, cus string) {
	if !mismoOrigen(r) {
		http.Error(w, "origen no permitido", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximoCuerpoPSE)
	if err := r.ParseForm(); err != nil {
		var excedido *http.MaxBytesError
		if errors.As(err, &excedido) {
			http.Error(w, "formulario demasiado grande", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "formulario inválido", http.StatusBadRequest)
		return
	}
	referencia := strings.TrimSpace(r.PostForm.Get("referencia"))
	monto, err := montoDeTexto(r.PostForm.Get("monto"))
	if referencia == "" || err != nil {
		http.Error(w, "referencia y monto (en centavos) son obligatorios", http.StatusBadRequest)
		return
	}

	ahora := p.ahora()
	envio := Envio{Referencia: referencia, Monto: monto, Moneda: "COP", Estado: estado, CUS: cus, OcurridoEn: ahora}
	if _, err := p.despachador.almacen.Registrar(r.Context(), envio, ahora); err != nil {
		slog.ErrorContext(r.Context(), "registrar evento", "referencia", referencia, "err", err)
		http.Error(w, "no se pudo registrar el evento", http.StatusInternalServerError)
		return
	}
	p.despachador.Despertar()

	escribirPagina(w, plantillaResultado, datosResultado{
		Referencia: referencia, Monto: monto, Presentado: presentarCentavos(monto),
		Estado: estado, CUS: cus, Aprobado: estado == estadoAprobado,
	})
}

// mismoOrigen acepta una petición sin Origin o cuyo host coincide con el Host de la petición.
func mismoOrigen(r *http.Request) bool {
	origen := r.Header.Get("Origin")
	if origen == "" {
		return true
	}
	u, err := url.Parse(origen)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == r.Host
}

// datosPSE alimenta la página con la referencia y el monto que viajaron en la URL.
type datosPSE struct {
	Referencia string
	Monto      int64
	Presentado string
}

// datosResultado alimenta la página con el desenlace que eligió el usuario.
type datosResultado struct {
	Referencia string
	Monto      int64
	Presentado string
	Estado     string
	CUS        string
	Aprobado   bool
}

// montoDeTexto parsea el monto en centavos y exige un entero positivo.
func montoDeTexto(s string) (int64, error) {
	monto, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || monto <= 0 {
		return 0, fmt.Errorf("monto inválido: %q", s)
	}
	return monto, nil
}

// presentarCentavos rinde el monto entero de centavos con dos decimales.
func presentarCentavos(centavos int64) string {
	signo := ""
	if centavos < 0 {
		signo, centavos = "-", -centavos
	}
	return fmt.Sprintf("%s%d,%02d", signo, centavos/100, centavos%100)
}

// escribirPagina sirve una plantilla HTML ya escapada y registra un fallo de render.
func escribirPagina(w http.ResponseWriter, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.Execute(w, data); err != nil {
		slog.Error("renderizar página", "err", err)
	}
}

var plantillaPSE = template.Must(template.New("pse").Parse(`<!doctype html>
<html lang="es">
<head><meta charset="utf-8"><title>PSE simulado</title></head>
<body>
<h1>Flujo de PSE simulado</h1>
<p>Referencia: <strong>{{.Referencia}}</strong></p>
<p>Monto: <strong>{{.Presentado}} COP</strong> ({{.Monto}} centavos)</p>
<form method="post" action="/pse/autorizar">
<input type="hidden" name="referencia" value="{{.Referencia}}">
<input type="hidden" name="monto" value="{{.Monto}}">
<button type="submit">Autorizar</button>
</form>
<form method="post" action="/pse/cancelar">
<input type="hidden" name="referencia" value="{{.Referencia}}">
<input type="hidden" name="monto" value="{{.Monto}}">
<button type="submit">Cancelar</button>
</form>
</body>
</html>`))

var plantillaResultado = template.Must(template.New("resultado").Parse(`<!doctype html>
<html lang="es">
<head><meta charset="utf-8"><title>PSE simulado</title></head>
<body>
<h1>Pago {{if .Aprobado}}autorizado{{else}}cancelado{{end}}</h1>
<p>Referencia: <strong>{{.Referencia}}</strong></p>
<p>Monto: <strong>{{.Presentado}} COP</strong></p>
<p>Estado: <strong>{{.Estado}}</strong></p>
{{if .CUS}}<p>CUS: <strong>{{.CUS}}</strong></p>{{end}}
</body>
</html>`))
