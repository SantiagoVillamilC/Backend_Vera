package control

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/falsos"
)

var cuatroVolumenes = []config.Volumen{
	{Centro: "US-IL-1", ID: "t35aiij709"}, {Centro: "US-CA-2", ID: "3ztqv80lvj"},
	{Centro: "CA-MTL-1", ID: "a96l2w9mbs"}, {Centro: "EU-SE-1", ID: "5fr195v982"},
}

const sinInstancias = "There are no instances currently available"

func conVolumenes(t *testing.T) *arnes {
	t.Helper()
	a := nuevoArnes(t, func(c *config.Config) { c.Volumenes = cuatroVolumenes })
	a.nube.Volumenes = map[string]string{"t35aiij709": "US-IL-1", "3ztqv80lvj": "US-CA-2", "a96l2w9mbs": "CA-MTL-1", "5fr195v982": "EU-SE-1"}
	return a
}

// centrosPedidos devuelve el primer dataCenterIds de cada POST /v2/pods, en orden.
func centrosPedidos(t *testing.T, a *arnes) []string {
	t.Helper()
	var salida []string
	for _, crudo := range a.nube.Cuerpos() {
		var c struct {
			Centros []string `json:"dataCenterIds"`
		}
		if err := json.Unmarshal(crudo, &c); err != nil || len(c.Centros) != 1 {
			t.Fatalf("cuerpo sin un centro: %s err=%v", crudo, err)
		}
		salida = append(salida, c.Centros[0])
	}
	return salida
}

func igual(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

func TestPrimerCentroSinExistenciasYSegundoConExito(t *testing.T) {
	a := conVolumenes(t)
	a.nube.CrearPorCentro = map[string]falsos.RespuestaCrear{"US-IL-1": {Estado: 500, Detalle: sinInstancias}}
	v := a.encenderYEsperar()
	if v.Centro != "US-CA-2" || v.Volumen != "3ztqv80lvj" {
		t.Fatalf("vista %+v", v)
	}
	if got := centrosPedidos(t, a); !igual(got, []string{"US-IL-1", "US-CA-2"}) {
		t.Fatalf("centros probados %v: un 500 de capacidad no se reintenta en el mismo centro", got)
	}
	if a.nube.Vivos("vera-modelos") != 1 {
		t.Fatal("debe haber exactamente un pod")
	}
	guardado, err := a.almacen.Cargar()
	if err != nil || guardado.Centro != "US-CA-2" || guardado.Volumen != "3ztqv80lvj" {
		t.Fatalf("estado en disco %+v err=%v", guardado, err)
	}
	registro := a.registro.String()
	for _, esperado := range []string{"intento de creacion", "sin existencias en el centro", "US-IL-1", "US-CA-2"} {
		if !strings.Contains(registro, esperado) {
			t.Errorf("falta %q en el registro:\n%s", esperado, registro)
		}
	}
}

func TestSigueAlSiguienteCentroConCualquierRechazoDeCapacidad(t *testing.T) {
	a := conVolumenes(t)
	a.nube.CrearPorCentro = map[string]falsos.RespuestaCrear{
		"US-IL-1": {Estado: 400, Detalle: "could not be placed"}, "US-CA-2": {Estado: 403, Detalle: "no access to pool"},
		"CA-MTL-1": {Estado: 500, Detalle: sinInstancias}}
	v := a.encenderYEsperar()
	if v.Centro != "EU-SE-1" {
		t.Fatalf("vista %+v", v)
	}
	if got := centrosPedidos(t, a); !igual(got, []string{"US-IL-1", "US-CA-2", "CA-MTL-1", "EU-SE-1"}) {
		t.Fatalf("orden de los centros %v", got)
	}
}

func TestTodosLosCentrosSinExistenciasNombraLosCentrosProbados(t *testing.T) {
	a := conVolumenes(t)
	sin := falsos.RespuestaCrear{Estado: 500, Detalle: sinInstancias}
	a.nube.CrearPorCentro = map[string]falsos.RespuestaCrear{"US-IL-1": sin, "US-CA-2": sin, "CA-MTL-1": sin, "EU-SE-1": sin}
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.SinExistencias)
	for _, centro := range []string{"US-IL-1", "US-CA-2", "CA-MTL-1", "EU-SE-1"} {
		if !strings.Contains(v.Mensaje, centro) {
			t.Errorf("el mensaje no nombra %s: %s", centro, v.Mensaje)
		}
	}
	if a.nube.Vivos("vera-modelos") != 0 || v.PodID != "" || v.Centro != "" || v.Volumen != "" {
		t.Fatalf("no debe quedar pod ni centro: %+v", v)
	}
	if a.svc.Disponible() {
		t.Fatal("sin pod no hay modelos")
	}
}

func TestTodosLosCentrosRechazanConElMismoDetalleLoMuestraElMensaje(t *testing.T) {
	a := conVolumenes(t)
	a.nube.CrearPorCentro = map[string]falsos.RespuestaCrear{}
	for _, v := range cuatroVolumenes {
		a.nube.CrearPorCentro[v.Centro] = falsos.RespuestaCrear{Estado: 400, Detalle: "volumen rechazado en " + v.Centro}
	}
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.SinExistencias)
	if !strings.Contains(v.Mensaje, "volumen rechazado en EU-SE-1") {
		t.Fatalf("el ultimo detalle de RunPod debe verse en el mensaje: %s", v.Mensaje)
	}
}

func TestUnErrorQueNoEsDeCapacidadNoPasaAlSiguienteCentro(t *testing.T) {
	for nombre, estadoHTTP := range map[string]int{"validacion 422": 422, "saldo 402": 402, "peticion grande 413": 413} {
		t.Run(nombre, func(t *testing.T) {
			a := conVolumenes(t)
			a.nube.CrearPorCentro = map[string]falsos.RespuestaCrear{"US-IL-1": {Estado: estadoHTTP, Detalle: "rechazo definitivo"}}
			if _, _, err := a.svc.Encender(); err != nil {
				t.Fatal(err)
			}
			v := a.esperarFase(estado.Error)
			if got := centrosPedidos(t, a); !igual(got, []string{"US-IL-1"}) {
				t.Fatalf("no debia probar otros centros: %v", got)
			}
			if a.nube.Vivos("vera-modelos") != 0 || v.PodID != "" {
				t.Fatalf("vista %+v", v)
			}
		})
	}
}

func TestUnCincoCientosGenericoSeReintentaEnElMismoCentro(t *testing.T) {
	a := conVolumenes(t)
	a.nube.CrearPorCentro = map[string]falsos.RespuestaCrear{"US-IL-1": {Estado: 500}}
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	a.esperarFase(estado.Error)
	if got := centrosPedidos(t, a); !igual(got, []string{"US-IL-1", "US-IL-1", "US-IL-1"}) {
		t.Fatalf("un 500 sin 'no instances' es un fallo transitorio, no capacidad: %v", got)
	}
}

func TestLaTarifaSobreElTopeConVolumenTerminaElPodSinProbarOtroCentro(t *testing.T) {
	a := conVolumenes(t)
	a.nube.Crear = []falsos.RespuestaCrear{{Costo: falsos.Costo(0.89)}}
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.Error)
	if a.nube.Vivos("vera-modelos") != 0 || v.PodID != "" || len(a.nube.Cuerpos()) != 1 {
		t.Fatalf("vivos=%d cuerpos=%d vista=%+v", a.nube.Vivos("vera-modelos"), len(a.nube.Cuerpos()), v)
	}
}

func TestUnPodUbicadoEnOtroCentroSeTermina(t *testing.T) {
	a := conVolumenes(t)
	a.nube.Crear = []falsos.RespuestaCrear{{Centro: "EU-SE-1"}}
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.Error)
	if a.nube.Vivos("vera-modelos") != 0 || !strings.Contains(v.Mensaje, "centro") {
		t.Fatalf("vivos=%d vista=%+v", a.nube.Vivos("vera-modelos"), v)
	}
}

func TestUnPodSinCentroInformadoSeAceptaConAviso(t *testing.T) {
	a := conVolumenes(t)
	a.nube.Crear = []falsos.RespuestaCrear{{SinCentro: true}}
	v := a.encenderYEsperar()
	if v.Centro != "US-IL-1" || !strings.Contains(a.registro.String(), "no informo el centro") {
		t.Fatalf("vista %+v\nregistro:\n%s", v, a.registro.String())
	}
}

func TestSinVolumenesElCuerpoNoTrae(t *testing.T) {
	a := nuevoArnes(t)
	v := a.encenderYEsperar()
	cuerpo := string(a.nube.Cuerpos()[0])
	if strings.Contains(cuerpo, "mounts") || strings.Contains(cuerpo, "dataCenterIds") || v.Centro != "" || v.Volumen != "" {
		t.Fatalf("cuerpo %s vista %+v", cuerpo, v)
	}
	if strings.Contains(a.ssh.Stdin(), "VERA_VOLUMEN_MONTADO") {
		t.Fatal("sin volumen no se debe avisar al script que hay uno montado")
	}
}

func TestElCuerpoDeCreacionConVolumenEsExactamenteElDelEsquema(t *testing.T) {
	a := conVolumenes(t)
	a.encenderYEsperar()
	const esperado = `{"name":"vera-modelos","image":"runpod/pytorch:1.0.2-cu1281-torch280-ubuntu2404","gpu":{"id":"NVIDIA RTX A5000","count":1},` +
		`"cloud":"SECURE","disk":40,"ports":["22/tcp"],"env":{"PUBLIC_KEY":"ssh-ed25519 AAAATESTKEY vm"},"startSsh":true,` +
		`"dataCenterIds":["US-IL-1"],"mounts":{"network":[{"volumeId":"t35aiij709","path":"/workspace"}]}}`
	if got := string(a.nube.Cuerpos()[0]); got != esperado {
		t.Fatalf("cuerpo\n got: %s\nwant: %s", got, esperado)
	}
}

func TestElScriptRecibeQueElVolumenEstaMontadoPorStdin(t *testing.T) {
	a := conVolumenes(t)
	a.encenderYEsperar()
	if !strings.Contains(a.ssh.Stdin(), "export VERA_VOLUMEN_MONTADO=1\n") {
		t.Fatalf("falta la variable en el stdin: %s", a.ssh.Stdin())
	}
	if strings.Contains(a.ssh.Argv(), "VERA_VOLUMEN_MONTADO") {
		t.Fatal("la variable va por stdin, no por argv")
	}
	var cuerpo struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(a.nube.Cuerpos()[0], &cuerpo); err != nil || len(cuerpo.Env) != 1 {
		t.Fatalf("el env del pod no debe cambiar: %v %v", cuerpo.Env, err)
	}
}

func TestLaVistaYElJsonDeEstadoTraenCentroYVolumen(t *testing.T) {
	a := conVolumenes(t)
	a.encenderYEsperar()
	texto, err := jsonDe(a.svc.Vista())
	if err != nil || !strings.Contains(texto, `"centro":"US-IL-1"`) || !strings.Contains(texto, `"volumen":"t35aiij709"`) {
		t.Fatalf("json de la vista %s err=%v", texto, err)
	}
}

func TestApagarLimpiaElCentroYNingunCodigoTocaLosVolumenes(t *testing.T) {
	a := conVolumenes(t)
	a.encenderYEsperar()
	a.svc.Apagar()
	v := a.esperarFase(estado.Apagado)
	if v.Centro != "" || v.Volumen != "" || v.PodID != "" {
		t.Fatalf("vista %+v", v)
	}
	guardado, _ := a.almacen.Cargar()
	if guardado.Centro != "" || guardado.Volumen != "" {
		t.Fatalf("estado %+v", guardado)
	}
	sinVolumenes(t, a)
}

func TestReconcileTerminaHuerfanosYNoTocaVolumenes(t *testing.T) {
	a := conVolumenes(t)
	huerfano := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora().Add(-5*time.Hour))
	inf, err := Reconciliar(context.Background(), ParamsReconciliar{Cfg: a.cfg, Nube: a.deps.Nube, Almacen: a.almacen, Reloj: a.reloj.Ahora,
		Dormir: a.deps.Dormir, Log: a.deps.Log, Esperas: a.deps.EsperasTerminar})
	if err != nil || len(inf.Terminados) != 1 || a.nube.Existe(huerfano) {
		t.Fatalf("inf=%+v err=%v", inf, err)
	}
	sinVolumenes(t, a)
}

// sinVolumenes comprueba que ninguna llamada a RunPod pidió, creó o borró un volumen de red.
func sinVolumenes(t *testing.T, a *arnes) {
	t.Helper()
	for _, l := range a.nube.Llamadas() {
		if strings.Contains(l, "network-volumes") || strings.Contains(l, "volumes") {
			t.Fatalf("modelos-control no debe tocar volumenes, pero llamo %q", l)
		}
	}
}
