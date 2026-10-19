package copilot

import (
	"context"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/riverqueue/river"
)

// RetenerHistorialArgs no lleva identificadores ni contenido personal a River.
type RetenerHistorialArgs struct{}

// Kind identifica el mantenimiento del historial del asistente.
func (RetenerHistorialArgs) Kind() string { return "retener_historial_asistente" }

// RetenerHistorialWorker reutiliza el apagado y los reintentos del cliente River.
type RetenerHistorialWorker struct {
	river.WorkerDefaults[RetenerHistorialArgs]
	historial *ServicioHistorial
}

// Work borra lotes y permite que River reintente errores técnicos.
func (w *RetenerHistorialWorker) Work(ctx context.Context, _ *river.Job[RetenerHistorialArgs]) error {
	_, err := w.historial.Retener(ctx)
	return err
}

// OpcionRetencionHistorial registra el barrido al inicio y cada quince minutos.
func OpcionRetencionHistorial(h *ServicioHistorial) jobs.OpcionDeAuth {
	return jobs.ConTrabajoDeAuth(&RetenerHistorialWorker{historial: h}, jobs.TrabajoPeriodicoDeAuth{ID: "retener_historial_asistente", Periodo: 15 * time.Minute, Args: func() (river.JobArgs, *river.InsertOpts) {
		return RetenerHistorialArgs{}, &river.InsertOpts{Queue: jobs.ColaAuth}
	}})
}
