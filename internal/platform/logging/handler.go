package logging

import (
	"context"
	"log/slog"

	"github.com/go-chi/chi/v5/middleware"
)

// requestIDHandler agrega a cada registro el request_id que chi dejó en el contexto. Se
// embebe slog.Handler para promover Enabled sin cambios.
type requestIDHandler struct {
	slog.Handler
}

// WithRequestID envuelve h para que todo registro hecho con un contexto de petición lleve
// el id que el middleware RequestID guardó ahí, bajo la clave request_id.
func WithRequestID(h slog.Handler) slog.Handler {
	return requestIDHandler{Handler: h}
}

// Handle copia el registro, le suma el id y lo delega al handler interno.
func (h requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := middleware.GetReqID(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs vuelve a envolver para no perder la inyección del id.
func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup vuelve a envolver para no perder la inyección del id.
func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{Handler: h.Handler.WithGroup(name)}
}
