package control

import (
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
)

func (a *arnes) reconciliar() (Informe, error) {
	a.t.Helper()
	return Reconciliar(a.t.Context(), ParamsReconciliar{Cfg: a.cfg, Nube: a.deps.Nube, Almacen: a.almacen, Reloj: a.reloj.Ahora,
		Dormir: a.deps.Dormir, Log: a.deps.Log, Esperas: []time.Duration{time.Second, time.Second}})
}

func (a *arnes) guardar(e estado.Estado) {
	a.t.Helper()
	if err := a.almacen.Guardar(e); err != nil {
		a.t.Fatal(err)
	}
}

func TestReconciliarTerminaUnPodQueSuperaLaSesionMaxima(t *testing.T) {
	a := nuevoArnes(t)
	viejo := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora().Add(-5*time.Hour))
	a.guardar(estado.Estado{Fase: estado.Listo, PodID: viejo, SesionInicio: a.reloj.Ahora().Add(-5 * time.Hour), UltimaActividad: a.reloj.Ahora()})
	inf, err := a.reconciliar()
	if err != nil || a.nube.Existe(viejo) || len(inf.Terminados) != 1 || !strings.Contains(inf.Terminados[0], "sesion maxima") {
		t.Fatalf("inf=%+v err=%v", inf, err)
	}
	if e, _ := a.almacen.Cargar(); e.Fase != estado.Apagado || e.PodID != "" {
		t.Fatalf("estado %+v", e)
	}
}

func TestReconciliarTerminaPorInactividadSiElProcesoPrincipalMurio(t *testing.T) {
	a := nuevoArnes(t)
	id := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora().Add(-time.Hour))
	a.guardar(estado.Estado{Fase: estado.Listo, PodID: id, SesionInicio: a.reloj.Ahora().Add(-time.Hour), UltimaActividad: a.reloj.Ahora().Add(-20 * time.Minute)})
	inf, err := a.reconciliar()
	if err != nil || a.nube.Existe(id) || len(inf.Terminados) != 1 || !strings.Contains(inf.Terminados[0], "inactividad") {
		t.Fatalf("inf=%+v err=%v", inf, err)
	}
}

func TestReconciliarConservaUnPodSano(t *testing.T) {
	a := nuevoArnes(t)
	id := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora().Add(-time.Hour))
	a.guardar(estado.Estado{Fase: estado.Listo, PodID: id, SesionInicio: a.reloj.Ahora().Add(-time.Hour), UltimaActividad: a.reloj.Ahora().Add(-time.Minute)})
	inf, err := a.reconciliar()
	if err != nil || !a.nube.Existe(id) || len(inf.Terminados) != 0 || len(inf.Conservados) != 1 {
		t.Fatalf("inf=%+v err=%v", inf, err)
	}
}

func TestReconciliarTerminaHuerfanosYDuplicadosPeroNoLosAjenos(t *testing.T) {
	a := nuevoArnes(t)
	legitimo := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora().Add(-time.Hour))
	duplicado := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora().Add(-30*time.Minute))
	ajeno := a.nube.AnadirPod("tesis-campana", "RUNNING", a.reloj.Ahora().Add(-10*time.Hour))
	a.guardar(estado.Estado{Fase: estado.Listo, PodID: legitimo, SesionInicio: a.reloj.Ahora().Add(-time.Hour), UltimaActividad: a.reloj.Ahora()})
	if _, err := a.reconciliar(); err != nil {
		t.Fatal(err)
	}
	if !a.nube.Existe(legitimo) || a.nube.Existe(duplicado) || !a.nube.Existe(ajeno) {
		t.Fatalf("legitimo=%v duplicado=%v ajeno=%v", a.nube.Existe(legitimo), a.nube.Existe(duplicado), a.nube.Existe(ajeno))
	}
}

func TestReconciliarTerminaUnPodSiElEstadoEsperadoEsApagado(t *testing.T) {
	a := nuevoArnes(t)
	id := a.nube.AnadirPod("vera-modelos", "EXITED", a.reloj.Ahora().Add(-time.Minute))
	if inf, err := a.reconciliar(); err != nil || a.nube.Existe(id) || len(inf.Terminados) != 1 {
		t.Fatalf("inf=%+v err=%v", inf, err)
	}
}

func TestReconciliarRespetaUnaCreacionEnCurso(t *testing.T) {
	a := nuevoArnes(t)
	reciente := a.nube.AnadirPod("vera-modelos", "STARTING", a.reloj.Ahora().Add(-time.Minute))
	a.guardar(estado.Estado{Fase: estado.Encendiendo, ActualizadoEn: a.reloj.Ahora()})
	if _, err := a.reconciliar(); err != nil || !a.nube.Existe(reciente) {
		t.Fatalf("un pod recien creado mientras el estado dice Encendiendo no debe tocarse: err=%v", err)
	}
	a.reloj.Avanzar(time.Hour)
	if _, err := a.reconciliar(); err != nil || a.nube.Existe(reciente) {
		t.Fatalf("pasada la gracia debe terminarse: err=%v", err)
	}
}

func TestReconciliarConElDeleteFallandoReportaElError(t *testing.T) {
	a := nuevoArnes(t)
	id := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora().Add(-5*time.Hour))
	a.nube.FallosDelete = 100
	inf, err := a.reconciliar()
	if err == nil || len(inf.Fallos) != 1 || !a.nube.Existe(id) {
		t.Fatalf("inf=%+v err=%v", inf, err)
	}
}

func TestReconciliarNoEscribeElEstadoSiElServicioEstaVivo(t *testing.T) {
	a := nuevoArnes(t)
	liberar, ok, err := a.almacen.BloquearServicio()
	if err != nil || !ok {
		t.Fatal(err)
	}
	t.Cleanup(liberar)
	id := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora().Add(-5*time.Hour))
	a.guardar(estado.Estado{Fase: estado.Listo, PodID: id, SesionInicio: a.reloj.Ahora().Add(-5 * time.Hour), UltimaActividad: a.reloj.Ahora()})
	if _, err := a.reconciliar(); err != nil || a.nube.Existe(id) {
		t.Fatalf("err=%v", err)
	}
	if e, _ := a.almacen.Cargar(); e.Fase != estado.Listo {
		t.Fatalf("el reconcile no debe pisar el estado del servicio vivo: %+v", e)
	}
}

func TestReconciliarSinListaDevuelveError(t *testing.T) {
	a := nuevoArnes(t)
	a.nube.ListarFalla = true
	if _, err := a.reconciliar(); err == nil {
		t.Fatal("esperaba error")
	}
}

func TestReconciliarCorrigeUnEstadoListoCuyoPodYaNoExiste(t *testing.T) {
	a := nuevoArnes(t)
	a.guardar(estado.Estado{Fase: estado.Listo, PodID: "pod9999", SesionInicio: a.reloj.Ahora()})
	if _, err := a.reconciliar(); err != nil {
		t.Fatal(err)
	}
	if e, _ := a.almacen.Cargar(); e.Fase != estado.Apagado || e.PodID != "" {
		t.Fatalf("estado %+v", e)
	}
}
