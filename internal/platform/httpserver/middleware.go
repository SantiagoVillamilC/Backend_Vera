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

// RequestID genera un id de petición en el servidor y lo guarda en el contexto, ignorando el X-Request-Id del cliente para que no pueda fijarlo.
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

// Recoverer registra el panic con su stack y responde 500 en el envelope, re-propaga http.ErrAbortHandler y desenvuelve panicWithStack.
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

// panicWithStack lleva el valor del panic recuperado en la goroutine del Timeout junto con su stack de origen.
type panicWithStack struct {
	value any
	stack []byte
}

// Timeout corre el handler con deadline y responde 504 en el envelope, descartando lo que el handler escriba después y sin responder si el cliente canceló.
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

// timeoutWriter acumula la respuesta del handler para poder descartarla completa si gana el timeout.
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

// RequireAuth exige identidad válida en el grupo autenticado, la deja en el contexto y da 401 en el envelope ante cualquier error o id vacío.
func RequireAuth(quien IdentidadDeLaPeticion) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if quien == nil {
				api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
				return
			}
			id, err := quien.Identidad(r)
			if err != nil || strings.TrimSpace(id.UsuarioID) == "" {
				api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(identidad.ConIdentidad(r.Context(), id)))
		})
	}
}

// clientIPKey da la clave del rate limit, cayendo a r.RemoteAddr sin puerto si ClientIP no resolvió una IP.
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

// RateLimitExempting aplica el rate limit por IP salvo a los paths exentos, para que los probes no consuman el bucket compartido.
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

// MethodNotAllowedEnvelope reemplaza el 405 de chi por el envelope sobre el mismo ResponseWriter, conservando el header Allow.
func MethodNotAllowedEnvelope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mw := &methodNotAllowedWriter{ResponseWriter: w}
		next.ServeHTTP(mw, r)
		if mw.intercepted {
			api.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed.", nil)
		}
	})
}

// methodNotAllowedWriter retiene el 405 del handler de chi para que MethodNotAllowedEnvelope lo reemplace por el envelope.
type methodNotAllowedWriter struct {
	http.ResponseWriter
	intercepted bool
}

// WriteHeader intercepta el 405 y lo marca para que el envelope se escriba al volver del handler.
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
