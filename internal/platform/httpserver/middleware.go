package httpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/google/uuid"
)

// RequestID genera en el servidor un id único por petición y lo guarda en el contexto,
// donde middleware.GetReqID lo lee (logging.WithRequestID lo estampa en cada registro). El
// X-Request-Id que mande el cliente se ignora a propósito: si se aceptara, un cliente podría
// fijar el id que aparece en los logs. El id generado también viaja en el header de respuesta
// para que quien llama pueda correlacionar.
func RequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := uuid.NewString()
			w.Header().Set(middleware.RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), middleware.RequestIDKey, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Recoverer recupera un panic, lo registra con slog junto a su stack y responde 500 dentro
// del envelope. Reemplaza a middleware.Recoverer, que escribe un 500 sin cuerpo y rompe el
// contrato de respuesta. Re-propaga http.ErrAbortHandler para no cambiar el aborto del socket.
//
// Un panic que atravesó el Timeout llega envuelto en panicWithStack: se desenvuelve para
// loguear el stack del punto donde se originó y no el de la goroutine de Timeout que solo
// re-propaga.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rvr := recover()
			if rvr == nil {
				return
			}
			value, stack := rvr, debug.Stack()
			if p, ok := rvr.(panicWithStack); ok {
				value, stack = p.value, p.stack
			}
			if value == http.ErrAbortHandler {
				panic(value)
			}
			slog.ErrorContext(r.Context(), "panic recovered", "err", value, "stack", string(stack))
			api.WriteError(w, http.StatusInternalServerError, "InternalError", "Something went wrong.", nil)
		}()
		next.ServeHTTP(w, r)
	})
}

// panicWithStack lleva el valor del panic recuperado en la goroutine del Timeout junto con
// el stack del momento en que se originó. Ver Recoverer.
type panicWithStack struct {
	value any
	stack []byte
}

// Timeout corre el handler con un deadline. Si vence, responde 504 dentro del envelope y
// descarta lo que el handler alcance a escribir después: el handler escribe en un buffer
// propio (timeoutWriter), no en el ResponseWriter real. Un panic del handler se re-propaga
// en esta goroutine para que el Recoverer de la raíz lo convierta en 500.
//
// Si el cliente corta la conexión, el contexto se cancela con context.Canceled: eso no es un
// vencimiento, no se escribe respuesta (el cliente ya no está leyendo).
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			tw := &timeoutWriter{header: make(http.Header)}
			done := make(chan struct{})
			panicCh := make(chan panicWithStack, 1)
			go func() {
				defer func() {
					if rvr := recover(); rvr != nil {
						panicCh <- panicWithStack{value: rvr, stack: debug.Stack()}
						return
					}
					close(done)
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			select {
			case <-done:
				tw.flush(w)
			case p := <-panicCh:
				panic(p)
			case <-ctx.Done():
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					api.WriteError(w, http.StatusGatewayTimeout, "Timeout", "Request timed out.", nil)
				}
			}
		})
	}
}

// timeoutWriter acumula la respuesta del handler para poder descartarla completa si el
// timeout gana la carrera.
type timeoutWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

// Header devuelve los headers acumulados.
func (tw *timeoutWriter) Header() http.Header { return tw.header }

// WriteHeader registra el primer status; los siguientes se ignoran.
func (tw *timeoutWriter) WriteHeader(status int) {
	if tw.status == 0 {
		tw.status = status
	}
}

// Write acumula el cuerpo y deja implícito un 200 si nadie llamó WriteHeader antes.
func (tw *timeoutWriter) Write(b []byte) (int, error) {
	if tw.status == 0 {
		tw.status = http.StatusOK
	}
	return tw.body.Write(b)
}

// flush copia al ResponseWriter real la respuesta acumulada por el handler.
func (tw *timeoutWriter) flush(w http.ResponseWriter) {
	for k, vals := range tw.header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	status := tw.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(tw.body.Bytes())
}

// RequireAuth exige una identidad válida en el grupo autenticado: le pregunta a la
// implementación inyectada quién es el usuario de la petición y, si la responde, deja su
// usuario_id en el contexto con identidad.ConUsuarioID. Cualquier error o un id vacío da 401
// dentro del envelope, de modo que ningún handler autenticado corre sin usuario.
//
// La credencial concreta la decide el adaptador, no este middleware: hoy es la cabecera de
// prueba de auth.UsuarioDePrueba; cuando exista el JWT real, main inyecta el validador que lea
// Authorization y este código no cambia.
func RequireAuth(quien IdentidadDeLaPeticion) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if quien == nil {
				api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
				return
			}
			usuarioID, err := quien.UsuarioID(r)
			if err != nil || strings.TrimSpace(usuarioID) == "" {
				api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(identidad.ConUsuarioID(r.Context(), usuarioID)))
		})
	}
}

// clientIPKey da la clave del rate limit. Si el middleware de ClientIP no resolvió una IP
// (por ejemplo, la petición no trae el header esperado), cae a r.RemoteAddr sin puerto para
// no colapsar todas esas peticiones en un único bucket.
func clientIPKey(r *http.Request) (string, error) {
	ip := middleware.GetClientIP(r.Context())
	if ip == "" {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			ip = host
		} else {
			ip = r.RemoteAddr
		}
	}
	return httprate.CanonicalizeIP(ip), nil
}

// RateLimitExempting aplica el rate limit por IP salvo a los paths indicados. Los probes de
// un orquestador sondean cada pocos segundos y no deben consumir ni ser bloqueados por el
// bucket compartido: sin la excepción, un prober recibe 429.
func RateLimitExempting(exemptPaths []string, requests int, window time.Duration) func(http.Handler) http.Handler {
	exempt := make(map[string]struct{}, len(exemptPaths))
	for _, p := range exemptPaths {
		exempt[p] = struct{}{}
	}
	limited := httprate.LimitBy(requests, window, clientIPKey, httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusTooManyRequests, "TooManyRequests", "Rate limit exceeded.", nil)
	}))
	return func(next http.Handler) http.Handler {
		limitedNext := limited(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := exempt[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			limitedNext.ServeHTTP(w, r)
		})
	}
}

// MethodNotAllowedEnvelope responde el 405 dentro del envelope conservando el header Allow.
// El handler por defecto de chi lo arma a partir de los métodos registrados para la ruta y
// después escribe 405 (mux.go: methodNotAllowedHandler); ese listado vive en
// rctx.methodsAllowed, que no tiene accesor exportado, así que en vez de reimplementarlo se
// intercepta la respuesta: se deja pasar el WriteHeader del handler propio y, si fue un 405,
// se reemplaza el cuerpo por el envelope sobre el mismo ResponseWriter, con el Allow que chi
// ya había puesto en los headers.
func MethodNotAllowedEnvelope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mw := &methodNotAllowedWriter{ResponseWriter: w}
		next.ServeHTTP(mw, r)
		if mw.intercepted {
			api.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed.", nil)
		}
	})
}

// methodNotAllowedWriter retiene el 405 del handler por defecto de chi para que
// MethodNotAllowedEnvelope lo reemplace por el envelope, conservando headers previos (Allow).
type methodNotAllowedWriter struct {
	http.ResponseWriter
	intercepted bool
}

// WriteHeader intercepta el 405: no lo reenvía, lo marca para que el envelope se escriba al
// volver del handler.
func (mw *methodNotAllowedWriter) WriteHeader(status int) {
	if status == http.StatusMethodNotAllowed {
		mw.intercepted = true
		return
	}
	mw.ResponseWriter.WriteHeader(status)
}

// Write descarta el cuerpo que el handler por defecto escriba tras un 405 interceptado.
func (mw *methodNotAllowedWriter) Write(b []byte) (int, error) {
	if mw.intercepted {
		return len(b), nil
	}
	return mw.ResponseWriter.Write(b)
}
