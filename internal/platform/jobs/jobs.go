// Package jobs arma el planificador de trabajos (River) que dispara las dos operaciones
// periódicas del núcleo: el barrido del outbox y la conciliación. La correctitud no depende del
// disparador: el outbox se escribe en la misma transacción que el asiento.
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

// ServicioDeDisparadores es lo que los workers necesitan del núcleo. Lo declara el consumidor y
// lo satisface *nucleo.Servicio.
type ServicioDeDisparadores interface {
	Conciliar(ctx context.Context, ventana time.Duration) (int, error)
	DespacharOutbox(ctx context.Context) error
}

// Config fija cada cuánto dispara cada trabajo y la ventana que recibe Conciliar.
type Config struct {
	OutboxInterval       time.Duration
	ConciliationInterval time.Duration
	ConciliationWindow   time.Duration
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

// NewClient construye el cliente de River sobre el pool de la aplicación, registra los dos
// workers y programa los dos trabajos periódicos. Los workers comparten el pool y el rol del
// resto del servicio; un pozo chico de workers alcanza para dos tareas livianas.
func NewClient(pool *pgxpool.Pool, servicio ServicioDeDisparadores, cfg Config) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &DespacharOutboxWorker{servicio: servicio})
	river.AddWorker(workers, &ConciliarWorker{servicio: servicio, ventana: cfg.ConciliationWindow})

	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema:  EsquemaRiver,
		Logger:  slog.Default(),
		Workers: workers,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 4}},
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(cfg.OutboxInterval),
				func() (river.JobArgs, *river.InsertOpts) { return DespacharOutboxArgs{}, nil },
				&river.PeriodicJobOpts{ID: "despachar_outbox", RunOnStart: true},
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(cfg.ConciliationInterval),
				func() (river.JobArgs, *river.InsertOpts) { return ConciliarArgs{}, nil },
				&river.PeriodicJobOpts{ID: "conciliar", RunOnStart: true},
			),
		},
	})
}
