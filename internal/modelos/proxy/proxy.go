// Package proxy expone los puertos locales que espera el backend de Vera (voz, STT y Ollama) y
// reenvía las peticiones por el túnel hacia el pod, registrando la última actividad real.
package proxy

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

const (
	maxCuerpoPeticion = 64 << 20
	reintentarEn      = "30"
	cuerpoApagado     = `{"error":"modelos_apagados"}` + "\n"
)

// Actividad lleva la hora de la última petición real y cuántas hay en vuelo. Una petición larga
// (un stream del LLM) mantiene el pod encendido mientras dure.
type Actividad struct {
	mu      sync.Mutex
	reloj   func() time.Time
	ultima  time.Time
	enVuelo int
}

// NuevaActividad crea el contador con un reloj inyectable.
func NuevaActividad(reloj func() time.Time) *Actividad { return &Actividad{reloj: reloj} }

// Inicio registra una petición que empieza.
func (a *Actividad) Inicio() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enVuelo++
	a.ultima = a.reloj()
}

// Fin registra una petición que termina.
func (a *Actividad) Fin() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enVuelo--
	a.ultima = a.reloj()
}

// Marcar adelanta la última actividad (un encendido reciente cuenta). Nunca la retrocede.
func (a *Actividad) Marcar(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t.After(a.ultima) {
		a.ultima = t
	}
}

// Ultima devuelve la hora de la última actividad. Cero si nunca hubo.
func (a *Actividad) Ultima() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ultima
}

// EnVuelo devuelve cuántas peticiones siguen abiertas.
func (a *Actividad) EnVuelo() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enVuelo
}

// EsSonda dice si la petición es un chequeo de salud. /readyz/modelos de la API hace GET / sobre
// cada puerto, y los modelos exponen /healthz y /api/version. Ninguno cuenta como actividad.
func EsSonda(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	switch r.URL.Path {
	case "/", "/healthz", "/readyz", "/api/version":
		return true
	}
	return false
}

// Opciones configura un manejador de proxy.
type Opciones struct {
	Destino    *url.URL
	Actividad  *Actividad
	Disponible func() bool
	Log        *slog.Logger
}

// Manejador devuelve el proxy inverso de un puerto.
func Manejador(o Opciones) http.Handler {
	rp := &httputil.ReverseProxy{
		Rewrite:       func(pr *httputil.ProxyRequest) { pr.SetURL(o.Destino) },
		FlushInterval: -1, // sin buffer: el stream de Ollama llega token a token
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
			ResponseHeaderTimeout: 10 * time.Minute, // un LLM puede tardar decenas de segundos en responder
			DisableCompression:    true,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       time.Minute,
		},
		ErrorLog: log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				o.Log.Warn("destino del proxy no responde", "error", err.Error())
			}
			responderApagado(w)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !o.Disponible() {
			responderApagado(w)
			return
		}
		if !EsSonda(r) {
			o.Actividad.Inicio()
			defer o.Actividad.Fin()
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxCuerpoPeticion)
		rp.ServeHTTP(w, r)
	})
}

func responderApagado(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", reintentarEn)
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, cuerpoApagado)
}

// Servidor es un http.Server ya enlazado a su puerto.
type Servidor struct {
	srv *http.Server
	ln  net.Listener
}

// Escuchar enlaza la dirección de inmediato (para fallar al arrancar si el puerto está ocupado).
func Escuchar(dir string, h http.Handler) (*Servidor, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", dir)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	return &Servidor{srv: srv, ln: ln}, nil
}

// Dir devuelve la dirección real de escucha.
func (s *Servidor) Dir() string { return s.ln.Addr().String() }

// Servir bloquea hasta que se cierre el servidor.
func (s *Servidor) Servir() error {
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Cerrar detiene el servidor de forma ordenada y, si no basta, a la fuerza.
func (s *Servidor) Cerrar(ctx context.Context) {
	if err := s.srv.Shutdown(ctx); err != nil {
		_ = s.srv.Close()
	}
}
