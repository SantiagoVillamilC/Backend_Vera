package copilot

import (
	"context"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/riverqueue/river"
)

// Expirar resuelve lotes de conversaciones sin competir por los candados ya tomados.
func (s *ControlAcciones) Expirar(ctx context.Context) (int, error) {
	return EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (int, error) {
		conversaciones, err := q.ConversacionesConAccionesVencidas(ctx, 100)
		if err != nil {
			return 0, err
		}
		total := 0
		for _, c := range conversaciones {
			acciones, err := q.VencerAccionesDeConversacion(ctx, c.ID)
			if err != nil {
				return 0, err
			}
			for _, a := range acciones {
				if c.Estado == "activo" {
					if err := anexarEstadoAccion(ctx, q, a); err != nil {
						return 0, err
					}
				}
				total++
			}
		}
		return total, nil
	})
}

// ExpirarAccionesArgs dispara un barrido sin datos personales en la cola.
type ExpirarAccionesArgs struct{}

// Kind identifica el barrido del asistente en River.
func (ExpirarAccionesArgs) Kind() string { return "expirar_acciones_asistente" }

// ExpirarAccionesWorker conserva los reintentos y el ciclo de apagado de River.
type ExpirarAccionesWorker struct {
	river.WorkerDefaults[ExpirarAccionesArgs]
	control *ControlAcciones
}

// Work propaga los errores para conservar los reintentos de River.
func (w *ExpirarAccionesWorker) Work(ctx context.Context, _ *river.Job[ExpirarAccionesArgs]) error {
	_, err := w.control.Expirar(ctx)
	return err
}

// OpcionExpiracionAcciones registra el barrido al inicio y cada minuto.
func OpcionExpiracionAcciones(c *ControlAcciones) jobs.OpcionDeAuth {
	return jobs.ConTrabajoDeAuth(&ExpirarAccionesWorker{control: c}, jobs.TrabajoPeriodicoDeAuth{ID: "expirar_acciones_asistente", Periodo: time.Minute, Args: func() (river.JobArgs, *river.InsertOpts) {
		return ExpirarAccionesArgs{}, &river.InsertOpts{Queue: jobs.ColaAuth}
	}})
}
