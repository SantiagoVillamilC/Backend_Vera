package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/riverqueue/river"
)

type servicioFalso struct {
	outboxLlamado     bool
	conciliarLlamado  bool
	expiracionLlamada bool
	ventana           time.Duration
	ttl               time.Duration
	err               error
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

func (s *servicioFalso) ExpirarIntenciones(_ context.Context, ttl time.Duration) (int, error) {
	s.expiracionLlamada = true
	s.ttl = ttl
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

// NF-A1-07.3: el worker de expiración delega con el TTL configurado y propaga el error a River.
func TestExpirarIntencionesWorkerPasaElTTLYDelega(t *testing.T) {
	t.Parallel()

	ttl := 30 * time.Second
	servicio := &servicioFalso{}

	worker := &ExpirarIntencionesWorker{servicio: servicio, ttl: ttl}
	if err := worker.Work(t.Context(), &river.Job[ExpirarIntencionesArgs]{}); err != nil {
		t.Fatalf("Work() error inesperado: %v", err)
	}
	if !servicio.expiracionLlamada {
		t.Error("Work() no llamó a ExpirarIntenciones")
	}
	if servicio.ttl != ttl {
		t.Errorf("ttl = %v, want %v", servicio.ttl, ttl)
	}
}

// NF-A1-07.3: con el TTL apagado el barrido de expiración no se programa; los otros dos siguen.
func TestTrabajosPeriodicosApaganLaExpiracionConTTLCero(t *testing.T) {
	t.Parallel()

	base := Config{OutboxInterval: 5 * time.Second, ConciliationInterval: time.Minute, ExpirationInterval: time.Minute}
	encendido := base
	encendido.ExpirationTTL = time.Minute

	periodicosEncendido := trabajosPeriodicos(encendido)
	if !tieneTrabajo(periodicosEncendido, "expirar_intenciones") {
		t.Error("con TTL positivo falta el trabajo expirar_intenciones")
	}
	if len(periodicosEncendido) != 3 {
		t.Errorf("con TTL positivo hay %d trabajos, want 3", len(periodicosEncendido))
	}

	periodicosApagado := trabajosPeriodicos(base)
	if tieneTrabajo(periodicosApagado, "expirar_intenciones") {
		t.Error("con TTL en cero no debe programarse expirar_intenciones")
	}
	for _, id := range []string{"despachar_outbox", "conciliar"} {
		if !tieneTrabajo(periodicosApagado, id) {
			t.Errorf("falta el trabajo %q", id)
		}
	}
}

func tieneTrabajo(trabajos []trabajoPeriodico, id string) bool {
	for _, t := range trabajos {
		if t.id == id {
			return true
		}
	}
	return false
}

// argsAuthFalso es un trabajo de auth vacío: la prueba solo ejercita el plan del cliente.
type argsAuthFalso struct{}

// Kind es el nombre estable de la fila encolada.
func (argsAuthFalso) Kind() string { return "prueba_auth" }

type workerAuthFalso struct {
	river.WorkerDefaults[argsAuthFalso]
}

func (w *workerAuthFalso) Work(context.Context, *river.Job[argsAuthFalso]) error { return nil }

// AUT-A1-03: la opción de auth agrega la cola auth y su periódico sin alterar los del núcleo.
func TestConTrabajoDeAuthAgregaLaColaYNoTocaLosPeriodicosDelNucleo(t *testing.T) {
	t.Parallel()

	cfg := Config{OutboxInterval: 5 * time.Second, ConciliationInterval: time.Minute, ExpirationInterval: time.Minute, ExpirationTTL: time.Minute}
	periodicosNucleo := trabajosPeriodicos(cfg)

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
	if len(periodicosNucleo) != 3 {
		t.Errorf("periódicos del núcleo = %d, quiero 3", len(periodicosNucleo))
	}
	for _, id := range []string{"despachar_outbox", "conciliar", "expirar_intenciones"} {
		if !tieneTrabajo(periodicosNucleo, id) {
			t.Errorf("falta el trabajo del núcleo %q", id)
		}
	}
}

func TestConTrabajoDeAuthRechazaElTrabajoIncompleto(t *testing.T) {
	t.Parallel()

	_, err := planificar(ConTrabajoDeAuth(&workerAuthFalso{}, TrabajoPeriodicoDeAuth{ID: "sin_periodo"}))
	if err == nil {
		t.Fatal("planificar aceptó un trabajo periódico sin periodo ni args")
	}
}
