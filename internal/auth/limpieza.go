package auth

import (
	"context"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// tamanoLoteDeCierre acota cuántas pendientes vencidas cierra cada transacción del job.
const tamanoLoteDeCierre = 100

// CerrarPendientesVencidas cierra en lotes las credenciales pendientes vencidas y devuelve cuántas cerró.
func (g *GestorDeEstados) CerrarPendientesVencidas(ctx context.Context, pool *pgxpool.Pool, ventana time.Duration) (int, error) {
	total := 0
	for {
		cerradas, err := g.cerrarLote(ctx, pool, ventana)
		if err != nil {
			return total, err
		}
		total += cerradas
		if cerradas < tamanoLoteDeCierre {
			return total, nil
		}
	}
}

// cerrarLote reclama las pendientes vencidas con FOR UPDATE SKIP LOCKED y las transiciona a cerrado.
func (g *GestorDeEstados) cerrarLote(ctx context.Context, pool *pgxpool.Pool, ventana time.Duration) (int, error) {
	var cerradas int
	_, err := enTransaccion(ctx, poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			ids, err := q.PendientesVencidasParaCierre(ctx, repositorio.PendientesVencidasParaCierreParams{
				VentanaSegundos: ventana.Seconds(),
				Limite:          tamanoLoteDeCierre,
			})
			if err != nil {
				return struct{}{}, err
			}
			for _, id := range ids {
				if err := g.TransicionarCredencial(ctx, q, uuid.UUID(id.Bytes), EstadoPendienteVerificacion, EstadoCerrado, MotivoPendienteVencida); err != nil {
					return struct{}{}, err
				}
				cerradas++
			}
			return struct{}{}, nil
		})
	if err != nil {
		return 0, err
	}
	return cerradas, nil
}

// CerrarPendientesArgs no lleva datos: la ventana la fija el worker.
type CerrarPendientesArgs struct{}

// Kind es el nombre estable de la fila encolada.
func (CerrarPendientesArgs) Kind() string { return "auth_cerrar_pendientes_vencidas" }

// CerrarPendientesWorker cierra las credenciales pendientes vencidas sobre la cola auth.
type CerrarPendientesWorker struct {
	river.WorkerDefaults[CerrarPendientesArgs]
	pool    *pgxpool.Pool
	gestor  *GestorDeEstados
	ventana time.Duration
}

// NuevoCerrarPendientesWorker construye el worker de limpieza con su pool, su gestor y la ventana.
func NuevoCerrarPendientesWorker(pool *pgxpool.Pool, gestor *GestorDeEstados, ventana time.Duration) *CerrarPendientesWorker {
	return &CerrarPendientesWorker{pool: pool, gestor: gestor, ventana: ventana}
}

// Work cierra el lote de pendientes vencidas y propaga el error para que River reintente.
func (w *CerrarPendientesWorker) Work(ctx context.Context, _ *river.Job[CerrarPendientesArgs]) error {
	_, err := w.gestor.CerrarPendientesVencidas(ctx, w.pool, w.ventana)
	return err
}

// TrabajoPeriodicoDeLimpieza arma el trabajo periódico del cierre de pendientes para la cola auth.
func TrabajoPeriodicoDeLimpieza(intervalo time.Duration) jobs.TrabajoPeriodicoDeAuth {
	return jobs.TrabajoPeriodicoDeAuth{
		ID:      "auth_cerrar_pendientes_vencidas",
		Periodo: intervalo,
		Args: func() (river.JobArgs, *river.InsertOpts) {
			return CerrarPendientesArgs{}, &river.InsertOpts{Queue: jobs.ColaAuth}
		},
	}
}
