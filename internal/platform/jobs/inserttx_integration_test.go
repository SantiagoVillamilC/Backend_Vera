//go:build integration

package jobs

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// argsDePrueba es un trabajo vacío: la prueba solo ejercita el encolado, no la ejecución.
type argsDePrueba struct{}

// Kind es el nombre estable de la fila encolada.
func (argsDePrueba) Kind() string { return "prueba_insert_tx" }

type workerDePrueba struct {
	river.WorkerDefaults[argsDePrueba]
}

func (w *workerDePrueba) Work(context.Context, *river.Job[argsDePrueba]) error { return nil }

func abrirPoolDeJobs(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		tb.Skip("APP_DATABASE_URL no definida: la prueba de integración necesita un Postgres real")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		tb.Fatalf("abrir pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	return pool
}

// TestInsertTxSobreUnaTransaccionDePgx demuestra contra Postgres real lo que NF-A2-05 necesita:
// river.Client encola dentro de una pgx.Tx abierta con InsertTx, el job no existe si la
// transacción revierte y sí existe si confirma.
func TestInsertTxSobreUnaTransaccionDePgx(t *testing.T) {
	pool := abrirPoolDeJobs(t)
	ctx := t.Context()

	workers := river.NewWorkers()
	river.AddWorker(workers, &workerDePrueba{})
	cliente, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Schema:  EsquemaRiver,
		Workers: workers,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
	})
	if err != nil {
		t.Fatalf("river.NewClient: %v", err)
	}

	contarJob := func(id int64) int {
		t.Helper()
		var total int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM river.river_job WHERE id = $1", id).Scan(&total); err != nil {
			t.Fatalf("contar job %d: %v", id, err)
		}
		return total
	}

	txCommit, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin para commit: %v", err)
	}
	insertado, err := cliente.InsertTx(ctx, txCommit, argsDePrueba{}, nil)
	if err != nil {
		t.Fatalf("InsertTx sobre transacción abierta: %v", err)
	}
	if err := txCommit.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM river.river_job WHERE id = $1", insertado.Job.ID)
	})
	if contarJob(insertado.Job.ID) != 1 {
		t.Errorf("el job %d no quedó tras el COMMIT", insertado.Job.ID)
	}

	txRollback, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin para rollback: %v", err)
	}
	descartado, err := cliente.InsertTx(ctx, txRollback, argsDePrueba{}, nil)
	if err != nil {
		t.Fatalf("InsertTx sobre transacción abierta: %v", err)
	}
	if err := txRollback.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if contarJob(descartado.Job.ID) != 0 {
		t.Errorf("el job %d quedó tras el ROLLBACK", descartado.Job.ID)
	}
}
