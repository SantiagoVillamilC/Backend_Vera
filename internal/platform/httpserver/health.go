package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthResource expone los dos probes del servicio. Es el único recurso que guarda el
// pool crudo, porque necesita Ping y nunca corre una consulta de negocio.
type HealthResource struct {
	pool *pgxpool.Pool
}

// NewHealthResource construye el recurso de salud sobre el pool compartido.
func NewHealthResource(pool *pgxpool.Pool) HealthResource {
	return HealthResource{pool: pool}
}

// Live responde que el proceso está vivo sin tocar ninguna dependencia, para que un
// bache de la base no reinicie un proceso sano.
func (rs HealthResource) Live(w http.ResponseWriter, _ *http.Request) {
	api.WriteData(w, http.StatusOK, map[string]string{"status": "alive"}, nil)
}

// Ready comprueba las dependencias con su propio presupuesto de 2s y responde 503 si
// alguna falla. El detalle queda en el log y nunca viaja en el cuerpo.
func (rs HealthResource) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := rs.pool.Ping(ctx); err != nil {
		slog.ErrorContext(ctx, "readiness check failed", "err", err)
		api.WriteError(w, http.StatusServiceUnavailable, "NotReady", "One or more dependencies are unavailable.", nil)
		return
	}
	api.WriteData(w, http.StatusOK, map[string]string{"status": "ready"}, nil)
}
