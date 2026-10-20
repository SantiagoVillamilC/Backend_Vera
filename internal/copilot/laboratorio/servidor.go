// Package laboratorio sirve la interfaz local del sandbox, sin acceso al dinero real.
package laboratorio

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/go-chi/chi/v5"
)

//go:embed web/*
var archivos embed.FS

type estado struct {
	Modelo     string `json:"modelo"`
	Version    string `json:"version"`
	Usadas     int    `json:"usadas"`
	Maximo     int    `json:"maximo"`
	Demo       bool   `json:"demo"`
	VozNatural bool   `json:"voz_natural"`
	VozFlujo   bool   `json:"voz_flujo"`
}

type tarjeta struct {
	Monto          string `json:"monto"`
	Destinatario   string `json:"destinatario"`
	RequiereCodigo bool   `json:"requiere_codigo"`
}

type turno struct {
	Texto   string                     `json:"texto"`
	Eventos []evaluacion.EventoConsola `json:"eventos"`
	Accion  *tarjeta                   `json:"accion_pendiente"`
	Estado  estado                     `json:"estado"`
}

type servidor struct {
	mu                    sync.Mutex
	consola               *evaluacion.Consola
	eventos               []evaluacion.EventoConsola
	modelo, token, puerto string
	demo                  bool
	voz                   copilot.SintetizadorVoz
	ultimoTexto, vozCache string
	audioCache            []byte
}

// Nuevo protege la sesión con un token efímero y acepta sólo hosts y orígenes locales.
// La clave de Gemini nunca se entrega al navegador.
func Nuevo(cfg evaluacion.ConfigReal, p copilot.ProveedorIA, token, host string, demo bool, voz copilot.SintetizadorVoz) (http.Handler, error) {
	_, puerto, err := net.SplitHostPort(host)
	if err != nil || token == "" {
		return nil, errors.New("laboratorio: host y token obligatorios")
	}
	s := &servidor{modelo: cfg.Modelo, token: token, puerto: puerto, demo: demo, voz: voz}
	s.consola, err = evaluacion.NuevaConsola(cfg, p, func(e evaluacion.EventoConsola) { s.eventos = append(s.eventos, e) }, time.Now)
	if err != nil {
		return nil, err
	}
	r := chi.NewRouter()
	r.Use(s.proteger)
	for ruta, nombre := range map[string]string{"/": "index.html", "/app.js": "app.js", "/audio.mjs": "audio.mjs", "/voz-flujo.mjs": "voz-flujo.mjs", "/grabador.js": "grabador.js", "/estilos.css": "estilos.css"} {
		r.Get(ruta, func(w http.ResponseWriter, _ *http.Request) {
			b, err := archivos.ReadFile("web/" + nombre)
			if err != nil {
				api.WriteError(w, 500, "InternalError", "No se pudo abrir la interfaz.", nil)
				return
			}
			content := map[string]string{"index.html": "text/html; charset=utf-8", "app.js": "text/javascript; charset=utf-8", "audio.mjs": "text/javascript; charset=utf-8", "voz-flujo.mjs": "text/javascript; charset=utf-8", "grabador.js": "text/javascript; charset=utf-8", "estilos.css": "text/css; charset=utf-8"}
			w.Header().Set("Content-Type", content[nombre])
			_, _ = w.Write(b)
		})
	}
	r.Get("/api/estado", s.api)
	r.Post("/api/preguntar", s.api)
	r.Post("/api/transcribir", s.api)
	r.Post("/api/nueva", s.api)
	r.Post("/api/voz", s.api)
	r.Post("/api/voz/flujo", s.api)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, 404, "NotFound", "Ruta no encontrada.", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, 405, "MethodNotAllowed", "Método no permitido.", nil)
	})
	return r, nil
}

func (s *servidor) proteger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; worker-src 'self'; media-src 'self' blob:; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.Host != "localhost:"+s.puerto && r.Host != "127.0.0.1:"+s.puerto {
			api.WriteError(w, 403, "Forbidden", "Abre el enlace local del lanzador.", nil)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if origen := r.Header.Get("Origin"); origen != "" && origen != "http://"+r.Host {
				api.WriteError(w, 403, "Forbidden", "Origen no permitido.", nil)
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Vera-Token")), []byte(s.token)) != 1 {
				api.WriteError(w, 401, "Unauthorized", "Abre el enlace completo que muestra el lanzador.", nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *servidor) estado() estado {
	u, m := s.consola.Consumo()
	_, flujo := s.voz.(copilot.SintetizadorVozFlujo)
	return estado{Modelo: s.modelo, Version: copilot.VersionInstruccionesVera, Usadas: u, Maximo: m, Demo: s.demo, VozNatural: s.voz != nil, VozFlujo: flujo}
}

func (s *servidor) api(w http.ResponseWriter, r *http.Request) {
	if !s.mu.TryLock() {
		api.WriteError(w, 409, "Busy", "Hay una consulta en curso. Espera a que termine.", nil)
		return
	}
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	switch r.URL.Path {
	case "/api/estado":
		api.WriteData(w, 200, s.estado(), nil)
	case "/api/nueva":
		s.limpiarVoz()
		s.consola.NuevaConversacion()
		s.eventos = nil
		api.WriteData(w, 200, s.estado(), nil)
	case "/api/voz", "/api/voz/flujo":
		s.sintetizar(ctx, w, r)
	case "/api/transcribir":
		if tipo, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); tipo != "audio/wav" {
			api.WriteError(w, 415, "UnsupportedMediaType", "Envía un clip WAV.", nil)
			return
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, copilot.MaxAudioBytes))
		if err != nil {
			s.error(w, &copilot.ErrorAudio{Regla: "audio_tamano"})
			return
		}
		defer clear(b)
		audio, err := copilot.ValidarAudio(base64.StdEncoding.EncodeToString(b), "wav")
		if err != nil {
			s.error(w, err)
			return
		}
		defer clear(audio.Datos)
		texto, err := s.consola.Transcribir(ctx, audio)
		if err != nil {
			s.error(w, err)
			return
		}
		api.WriteData(w, 200, struct {
			Transcripcion string `json:"transcripcion"`
			Estado        estado `json:"estado"`
		}{texto, s.estado()}, nil)
	case "/api/preguntar":
		s.limpiarVoz()
		if tipo, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); tipo != "text/plain" {
			api.WriteError(w, 415, "UnsupportedMediaType", "Envía la pregunta como texto.", nil)
			return
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
		if err != nil {
			api.WriteError(w, 413, "ValidationError", "La pregunta es demasiado larga.", nil)
			return
		}
		s.eventos = make([]evaluacion.EventoConsola, 0)
		respuesta, err := s.consola.Preguntar(ctx, string(b))
		if err != nil {
			s.error(w, err)
			return
		}
		var accion *tarjeta
		if a := respuesta.AccionPendiente; a != nil {
			accion = &tarjeta{a.Monto.PresentarUnidadesMayores() + " COP", a.NombreMostrado, a.RequiereCodigo}
		}
		s.ultimoTexto = respuesta.Texto
		api.WriteData(w, 200, turno{respuesta.Texto, s.eventos, accion, s.estado()}, nil)
	}
}

func (s *servidor) limpiarVoz() {
	clear(s.audioCache)
	s.audioCache = nil
	s.ultimoTexto = ""
	s.vozCache = ""
}

func (s *servidor) sintetizar(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if s.voz == nil {
		api.WriteError(w, 503, "VoiceUnavailable", "La voz natural no está disponible en esta sesión. Puedes elegir la voz del navegador.", nil)
		return
	}
	if tipo, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); tipo != "text/plain" {
		api.WriteError(w, 415, "UnsupportedMediaType", "Envía el nombre de la voz como texto.", nil)
		return
	}
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32))
	voz := string(b)
	if err != nil || (voz != "Aoede" && voz != "Kore" && voz != "Puck") || s.ultimoTexto == "" {
		api.WriteError(w, 422, "ValidationError", "Primero consulta a Vera y selecciona una voz disponible.", nil)
		return
	}
	if r.URL.Path == "/api/voz/flujo" {
		s.sintetizarFlujo(ctx, w, voz)
		return
	}
	if s.vozCache != voz || len(s.audioCache) == 0 {
		audio, err := s.consola.Sintetizar(ctx, s.voz, s.ultimoTexto, voz)
		if err != nil {
			s.error(w, err)
			return
		}
		clear(s.audioCache)
		s.audioCache = audio
		s.vozCache = voz
	}
	api.WriteData(w, 200, struct {
		Audio  string `json:"audio"`
		MIME   string `json:"mime"`
		Estado estado `json:"estado"`
	}{base64.StdEncoding.EncodeToString(s.audioCache), "audio/wav", s.estado()}, nil)
}

func (s *servidor) error(w http.ResponseWriter, err error) {
	var audio *copilot.ErrorAudio
	if errors.As(err, &audio) {
		api.WriteError(w, 422, "InvalidAudio", "El clip no es un WAV válido o supera el límite. Graba de nuevo.", nil)
		return
	}
	for _, e := range []struct {
		err       error
		status    int
		code, msg string
	}{
		{evaluacion.ErrCuotaEvaluacion, 429, "SessionLimit", "Se alcanzó el límite de esta sesión. No se harán más solicitudes."},
		{copilot.ErrLimiteIA, 429, "ProviderLimit", "Google limitó la cuota. No se reintentó la solicitud."},
		{copilot.ErrCredencialesIA, 401, "InvalidCredentials", "Google rechazó la clave. Revisa .env.local sin compartirla."},
		{copilot.ErrAudioNoEntendido, 422, "AudioNotUnderstood", "No entendí el audio. Prueba de nuevo o escribe tu pregunta."},
		{copilot.ErrBloqueoIA, 422, "ResponseBlocked", "La respuesta fue bloqueada por seguridad."},
		{copilot.ErrSolicitudIA, 422, "ValidationError", "La pregunta o solicitud no es válida."},
		{copilot.ErrTimeoutIA, 504, "Timeout", "La consulta tardó demasiado. No se reintentó."},
		{context.DeadlineExceeded, 504, "Timeout", "Se agotó el tiempo de espera. No se reintentó."},
	} {
		if errors.Is(err, e.err) {
			api.WriteError(w, e.status, e.code, e.msg, nil)
			return
		}
	}
	api.WriteError(w, 503, "Unavailable", "No se pudo completar la consulta. Inténtalo después.", nil)
}

// ProveedorDemo recorre las funciones reales con texto y transcripción fijos, sin red.
type ProveedorDemo struct{}

// Generar devuelve una transcripción o consulta de saldo simulada.
func (ProveedorDemo) Generar(_ context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	texto := "Tu saldo disponible es 120000.00 COP. Tienes 5000.00 COP retenidos."
	if s.SoloTranscripcion {
		texto = `{"transcripcion":"¿Cuánto tengo?"}`
	} else if s.Historial[len(s.Historial)-1].Rol == "usuario" {
		return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{ID: "saldo-demo", Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}}}}}, nil
	}
	return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: texto}}}}, nil
}
