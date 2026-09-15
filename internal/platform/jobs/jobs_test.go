package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

type servicioFalso struct {
	outboxLlamado    bool
	conciliarLlamado bool
	ventana          time.Duration
	err              error
}

func (s *servicioFalso) DespacharOutbox(context.Context) error {
	s.outboxLlamado = true
	return s.err
}

func (s *servicioFalso) Conciliar(_ context.Context, ventana time.Duration) (int, error) {
	s.conciliarLlamado = true
	s.ventana = ventana
	return 0, s.err
}

func TestDespacharOutboxWorkerDelegaYPropagaElError(t *testing.T) {
	t.Parallel()

	errBarrido := errors.New("falló el barrido")
	servicio := &servicioFalso{err: errBarrido}

	worker := &DespacharOutboxWorker{servicio: servicio}
	err := worker.Work(t.Context(), &river.Job[DespacharOutboxArgs]{})
	if !errors.Is(err, errBarrido) {
		t.Fatalf("Work() = %v, want que envuelva %v", err, errBarrido)
	}
	if !servicio.outboxLlamado {
		t.Error("Work() no llamó a DespacharOutbox")
	}
}

func TestConciliarWorkerPasaLaVentanaYDelega(t *testing.T) {
	t.Parallel()

	ventana := 5 * time.Minute
	servicio := &servicioFalso{}

	worker := &ConciliarWorker{servicio: servicio, ventana: ventana}
	if err := worker.Work(t.Context(), &river.Job[ConciliarArgs]{}); err != nil {
		t.Fatalf("Work() error inesperado: %v", err)
	}
	if !servicio.conciliarLlamado {
		t.Error("Work() no llamó a Conciliar")
	}
	if servicio.ventana != ventana {
		t.Errorf("ventana = %v, want %v", servicio.ventana, ventana)
	}
}
