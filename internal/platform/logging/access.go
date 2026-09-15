// Package logging implementa el logger de acceso estructurado sobre slog y el handler que
// estampa el request_id de chi en cada registro.
package logging

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// AccessLogger reemplaza a middleware.Logger en la cadena: mismo punto, pero una sola
// línea slog por petición en vez de texto plano fuera de slog. Va por encima de Recoverer
// para que su entrada exista cuando Recoverer busque el logger de la petición.
func AccessLogger() func(http.Handler) http.Handler {
	return middleware.RequestLogger(accessFormatter{})
}

type accessFormatter struct{}

// NewLogEntry crea la entrada de log que chi asocia a la petición.
func (accessFormatter) NewLogEntry(r *http.Request) middleware.LogEntry {
	return accessEntry{r: r}
}

type accessEntry struct{ r *http.Request }

// Write emite el registro de acceso cuando la petición termina. El path es r.URL.Path y
// nunca el RequestURI ni los headers, porque ahí viajan los bearer tokens.
func (e accessEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ interface{}) {
	slog.InfoContext(e.r.Context(), "request",
		"method", e.r.Method,
		"path", e.r.URL.Path,
		"status", status,
		"bytes", bytes,
		"duration_ms", float64(elapsed)/float64(time.Millisecond),
		"client_ip", middleware.GetClientIP(e.r.Context()),
	)
}

// Panic convierte un panic recuperado en un registro ERROR con su stack. Recoverer llama
// a este método a través de GetLogEntry cuando el logger de acceso está instalado.
func (e accessEntry) Panic(v interface{}, stack []byte) {
	slog.ErrorContext(e.r.Context(), "panic recovered", "err", v, "stack", string(stack))
}
