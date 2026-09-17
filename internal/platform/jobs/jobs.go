// Package jobs arma el planificador de River con los trabajos periódicos del núcleo.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// EsquemaRiver es el esquema propio de las tablas de River, aparte de public y de nucleo.
const EsquemaRiver = "river"

// ServicioDeDisparadores es lo que los workers necesitan del núcleo; lo satisface *nucleo.Servicio.
type ServicioDeDisparadores interface {
	Conciliar(ctx context.Context, ventana time.Duration) (int, error)
	DespacharOutbox(ctx context.Context) error
	ExpirarIntenciones(ctx context.Context, ttl time.Duration) (int, error)
}

// Config fija cada cuánto dispara cada trabajo y los parámetros que reciben sus workers.
type Config struct {
	OutboxInterval       time.Duration
	ConciliationInterval time.Duration
	ConciliationWindow   time.Duration
	ExpirationInterval   time.Duration
	ExpirationTTL        time.Duration
}

// DespacharOutboxArgs no lleva datos: el barrido del outbox no recibe parámetros.
type DespacharOutboxArgs struct{}

// Kind es el nombre estable de la fila encolada.
func (DespacharOutboxArgs) Kind() string { return "despachar_outbox" }

// DespacharOutboxWorker llama a DespacharOutbox del núcleo. El relleno actual lo reemplaza NF-A2-05.
type DespacharOutboxWorker struct {
	river.WorkerDefaults[DespacharOutboxArgs]
	servicio ServicioDeDisparadores
}

// Work delega en el núcleo y propaga su error para que River reintente.
func (w *DespacharOutboxWorker) Work(ctx context.Context, _ *river.Job[DespacharOutboxArgs]) error {
	return w.servicio.DespacharOutbox(ctx)
}

// ConciliarArgs no lleva datos: la ventana la fija el cliente.
type ConciliarArgs struct{}

// Kind es el nombre estable de la fila encolada.
func (ConciliarArgs) Kind() string { return "conciliar" }

// ConciliarWorker llama a Conciliar del núcleo. El relleno actual lo reemplaza NF-A4-02.1.
type ConciliarWorker struct {
	river.WorkerDefaults[ConciliarArgs]
	servicio ServicioDeDisparadores
	ventana  time.Duration
}

// Work delega en el núcleo y propaga su error para que River reintente.
func (w *ConciliarWorker) Work(ctx context.Context, _ *river.Job[ConciliarArgs]) error {
	_, err := w.servicio.Conciliar(ctx, w.ventana)
	return err
}

// ExpirarIntencionesArgs no lleva datos: el plazo lo fija el cliente.
type ExpirarIntencionesArgs struct{}

// Kind es el nombre estable de la fila encolada.
func (ExpirarIntencionesArgs) Kind() string { return "expirar_intenciones" }

// ExpirarIntencionesWorker llama a ExpirarIntenciones del núcleo con el plazo configurado.
type ExpirarIntencionesWorker struct {
	river.WorkerDefaults[ExpirarIntencionesArgs]
	servicio ServicioDeDisparadores
	ttl      time.Duration
}

// Work delega en el núcleo y propaga su error para que River reintente.
func (w *ExpirarIntencionesWorker) Work(ctx context.Context, _ *river.Job[ExpirarIntencionesArgs]) error {
	_, err := w.servicio.ExpirarIntenciones(ctx, w.ttl)
	return err
}

// trabajoPeriodico describe un trabajo periódico antes de convertirlo al tipo de River.
type trabajoPeriodico struct {
	id      string
	periodo time.Duration
	nuevo   river.PeriodicJobConstructor
}

// trabajosPeriodicos arma el plan; el barrido de expiración solo entra con un TTL positivo.
func trabajosPeriodicos(cfg Config) []trabajoPeriodico {
	trabajos := []trabajoPeriodico{
		{id: "despachar_outbox", periodo: cfg.OutboxInterval,
			nuevo: func() (river.JobArgs, *river.InsertOpts) { return DespacharOutboxArgs{}, nil }},
		{id: "conciliar", periodo: cfg.ConciliationInterval,
			nuevo: func() (river.JobArgs, *river.InsertOpts) { return ConciliarArgs{}, nil }},
	}
	if cfg.ExpirationTTL > 0 {
		trabajos = append(trabajos, trabajoPeriodico{id: "expirar_intenciones", periodo: cfg.ExpirationInterval,
			nuevo: func() (river.JobArgs, *river.InsertOpts) { return ExpirarIntencionesArgs{}, nil }})
	}
	return trabajos
}

// newPeriodicJobs traduce el plan a los trabajos periódicos de River.
func newPeriodicJobs(cfg Config) []*river.PeriodicJob {
	trabajos := trabajosPeriodicos(cfg)
	periodicos := make([]*river.PeriodicJob, 0, len(trabajos))
	for _, t := range trabajos {
		periodicos = append(periodicos, river.NewPeriodicJob(
			river.PeriodicInterval(t.periodo),
			t.nuevo,
			&river.PeriodicJobOpts{ID: t.id, RunOnStart: true},
		))
	}
	return periodicos
}

// NewClient construye el cliente de River sobre el pool de la aplicación y programa los trabajos periódicos.
func NewClient(pool *pgxpool.Pool, servicio ServicioDeDisparadores, cfg Config) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &DespacharOutboxWorker{servicio: servicio})
	river.AddWorker(workers, &ConciliarWorker{servicio: servicio, ventana: cfg.ConciliationWindow})
	river.AddWorker(workers, &ExpirarIntencionesWorker{servicio: servicio, ttl: cfg.ExpirationTTL})

	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema:       EsquemaRiver,
		Logger:       slog.Default(),
		Workers:      workers,
		Queues:       map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 4}},
		PeriodicJobs: newPeriodicJobs(cfg),
	})
}
