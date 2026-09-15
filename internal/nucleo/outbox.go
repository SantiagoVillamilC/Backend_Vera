package nucleo

import (
	"context"
	"log/slog"
)

// DespacharOutbox es el relleno de la fase 0: NF-A2-05 lo reemplaza por el barrido real de la
// tabla outbox.
func (s *Servicio) DespacharOutbox(ctx context.Context) error {
	slog.InfoContext(ctx, "nucleo: DespacharOutbox pendiente (NF-A2-05)")
	return nil
}
