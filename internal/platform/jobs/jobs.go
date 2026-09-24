// Package jobs arma el planificador de River con los trabajos periódicos del módulo de auth.
package jobs

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// EsquemaRiver es el esquema propio de las tablas de River, aparte de public y de auth.
const EsquemaRiver = "river"

// ColaAuth es la cola de River reservada a los trabajos del módulo de auth.
const ColaAuth = "auth"

// TrabajoPeriodicoDeAuth describe un trabajo periódico de auth antes de traducirlo al tipo de River.
type TrabajoPeriodicoDeAuth struct {
	ID      string
	Periodo time.Duration
	Args    river.PeriodicJobConstructor
}

// OpcionDeAuth extiende el planificador con workers y trabajos periódicos del módulo de auth.
type OpcionDeAuth func(*planAuth) error

// planAuth acumula lo que las opciones de auth aportan antes de construir el cliente.
type planAuth struct {
	usarColaAuth bool
	workers      []func(*river.Workers)
	periodicos   []TrabajoPeriodicoDeAuth
}

// ConTrabajoDeAuth registra un worker de auth y su trabajo periódico en la cola auth.
func ConTrabajoDeAuth[T river.JobArgs](worker river.Worker[T], periodico TrabajoPeriodicoDeAuth) OpcionDeAuth {
	return func(p *planAuth) error {
		if worker == nil {
			return errors.New("jobs: ConTrabajoDeAuth exige un worker")
		}
		if periodico.ID == "" || periodico.Args == nil || periodico.Periodo <= 0 {
			return fmt.Errorf("jobs: ConTrabajoDeAuth exige un trabajo periódico con id, periodo y args, got %+v", periodico)
		}
		p.usarColaAuth = true
		p.workers = append(p.workers, func(workers *river.Workers) { river.AddWorker(workers, worker) })
		p.periodicos = append(p.periodicos, periodico)
		return nil
	}
}

// planificar aplica las opciones de auth sobre el plan.
func planificar(opts ...OpcionDeAuth) (planAuth, error) {
	var plan planAuth
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&plan); err != nil {
			return planAuth{}, err
		}
	}
	return plan, nil
}

// NewClient construye el cliente de River sobre el pool de la aplicación, con los workers y
// trabajos periódicos que registren las opciones de auth.
func NewClient(pool *pgxpool.Pool, opts ...OpcionDeAuth) (*river.Client[pgx.Tx], error) {
	plan, err := planificar(opts...)
	if err != nil {
		return nil, err
	}

	workers := river.NewWorkers()
	for _, agregar := range plan.workers {
		agregar(workers)
	}

	colas := map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 4}}
	if plan.usarColaAuth {
		colas[ColaAuth] = river.QueueConfig{MaxWorkers: 1}
	}

	periodicos := make([]*river.PeriodicJob, 0, len(plan.periodicos))
	for _, t := range plan.periodicos {
		periodicos = append(periodicos, river.NewPeriodicJob(
			river.PeriodicInterval(t.Periodo),
			t.Args,
			&river.PeriodicJobOpts{ID: t.ID, RunOnStart: true},
		))
	}

	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema:       EsquemaRiver,
		Logger:       slog.Default(),
		Workers:      workers,
		Queues:       colas,
		PeriodicJobs: periodicos,
	})
}
