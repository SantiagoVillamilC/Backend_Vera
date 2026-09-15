package httpserver

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
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
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rvr := recover()
			if rvr == nil {
				return
			}
			if rvr == http.ErrAbortHandler {
				panic(rvr)
			}
			slog.ErrorContext(r.Context(), "panic recovered", "err", rvr, "stack", string(debug.Stack()))
			api.WriteError(w, http.StatusInternalServerError, "InternalError", "Something went wrong.", nil)
		}()
		next.ServeHTTP(w, r)
	})
}

// Timeout corre el handler con un deadline. Si vence, responde 504 dentro del envelope y
// descarta lo que el handler alcance a escribir después: el handler escribe en un buffer
// propio (timeoutWriter), no en el ResponseWriter real. Un panic del handler se re-propaga
// en esta goroutine para que el Recoverer de la raíz lo convierta en 500.
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			tw := &timeoutWriter{header: make(http.Header)}
			done := make(chan struct{})
			panicCh := make(chan any, 1)
			go func() {
				defer func() {
					if rvr := recover(); rvr != nil {
						panicCh <- rvr
						return
					}
					close(done)
				}()
				next.ServeHTTP(tw, r.WithContext(ctx))
			}()

			select {
			case <-done:
				tw.flush(w)
			case rvr := <-panicCh:
				panic(rvr)
			case <-ctx.Done():
				api.WriteError(w, http.StatusGatewayTimeout, "Timeout", "Request timed out.", nil)
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

// RequireAuth exige un bearer token bien formado en el grupo autenticado: exactamente el
// prefijo "Bearer " seguido de un token no vacío. Cualquier otra forma (Basic x, "Bearer"
// sin espacio, vacío) da 401.
//
// TODO: la validación real del JWT sigue pendiente en internal/auth (ValidateJWT); hasta
// entonces esto solo verifica la forma del header.
func RequireAuth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if jwtSecret == "" || !ok || strings.TrimSpace(token) == "" {
				api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
				return
			}
			next.ServeHTTP(w, r)
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
