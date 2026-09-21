package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

// argsAuthFalso es un trabajo de auth vacío: la prueba solo ejercita el plan del cliente.
type argsAuthFalso struct{}

// Kind es el nombre estable de la fila encolada.
func (argsAuthFalso) Kind() string { return "prueba_auth" }

type workerAuthFalso struct {
	river.WorkerDefaults[argsAuthFalso]
}

func (w *workerAuthFalso) Work(context.Context, *river.Job[argsAuthFalso]) error { return nil }

// AUT-A1-03: la opción de auth agrega la cola auth y su trabajo periódico.
func TestConTrabajoDeAuthAgregaLaColaYSuPeriodico(t *testing.T) {
	t.Parallel()

	sinAuth, err := planificar()
	if err != nil {
		t.Fatalf("planificar sin auth: %v", err)
	}
	if sinAuth.usarColaAuth || len(sinAuth.periodicos) != 0 {
		t.Errorf("plan sin auth = %+v, quiero sin cola auth ni periódicos", sinAuth)
	}

	conAuth, err := planificar(ConTrabajoDeAuth(&workerAuthFalso{}, TrabajoPeriodicoDeAuth{
		ID:      "auth_prueba",
		Periodo: time.Minute,
		Args:    func() (river.JobArgs, *river.InsertOpts) { return argsAuthFalso{}, &river.InsertOpts{Queue: ColaAuth} },
	}))
	if err != nil {
		t.Fatalf("planificar con auth: %v", err)
	}
	if !conAuth.usarColaAuth {
		t.Error("la opción de auth no marca la cola auth")
	}
	if len(conAuth.periodicos) != 1 || conAuth.periodicos[0].ID != "auth_prueba" {
		t.Errorf("periódicos de auth = %+v, quiero uno con id auth_prueba", conAuth.periodicos)
	}
}

func TestConTrabajoDeAuthRechazaElTrabajoIncompleto(t *testing.T) {
	t.Parallel()

	_, err := planificar(ConTrabajoDeAuth(&workerAuthFalso{}, TrabajoPeriodicoDeAuth{ID: "sin_periodo"}))
	if err == nil {
		t.Fatal("planificar aceptó un trabajo periódico sin periodo ni args")
	}
}
