package nucleo

import (
	"context"
	"log/slog"
	"time"
)

// Conciliar es el relleno de la fase 0: NF-A4-02.1 lo reemplaza por la conciliación real.
func (s *Servicio) Conciliar(ctx context.Context, ventana time.Duration) (int, error) {
	slog.InfoContext(ctx, "nucleo: Conciliar pendiente (NF-A4-02.1)", "ventana", ventana)
	return 0, nil
}
