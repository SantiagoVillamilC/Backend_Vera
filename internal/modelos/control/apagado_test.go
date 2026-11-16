package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/falsos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/proxy"
)

func TestApagaPorInactividadConRelojInyectado(t *testing.T) {
	a := nuevoArnes(t)
	a.encenderYEsperar()
	a.reloj.Avanzar(9 * time.Minute)
	a.svc.Tick(t.Context())
	if a.svc.Vista().Estado != "listo" {
		t.Fatalf("a los 9 minutos debe seguir listo: %+v", a.svc.Vista())
	}
	a.reloj.Avanzar(2 * time.Minute)
	a.svc.Tick(t.Context())
	v := a.esperarFase(estado.Apagado)
	if !strings.Contains(v.Mensaje, "sin peticiones") || a.nube.Vivos("vera-modelos") != 0 || a.svc.Disponible() {
		t.Fatalf("vista %+v vivos=%d", v, a.nube.Vivos("vera-modelos"))
	}
}

func TestLaActividadReiniciaElPlazoDeInactividad(t *testing.T) {
	a := nuevoArnes(t)
	a.encenderYEsperar()
	a.reloj.Avanzar(9 * time.Minute)
	a.act.Inicio()
	a.act.Fin()
	a.reloj.Avanzar(9 * time.Minute)
	a.svc.Tick(t.Context())
	if a.svc.Vista().Estado != "listo" {
		t.Fatalf("la actividad reciente debia mantenerlo: %+v", a.svc.Vista())
	}
}

func TestUnStreamEnVueloNoApagaPorInactividadPeroLaSesionMaximaSi(t *testing.T) {
	a := nuevoArnes(t)
	a.encenderYEsperar()
	a.act.Inicio() // un stream del LLM que sigue abierto
	a.reloj.Avanzar(30 * time.Minute)
	a.svc.Tick(t.Context())
	if a.svc.Vista().Estado != "listo" {
		t.Fatalf("un stream en vuelo no debe apagarse por inactividad: %+v", a.svc.Vista())
	}
	a.reloj.Avanzar(4 * time.Hour)
	a.svc.Tick(t.Context())
	v := a.esperarFase(estado.Apagado)
	if !strings.Contains(v.Mensaje, "sesion maxima") {
		t.Fatalf("vista %+v", v)
	}
	a.act.Fin()
}

func TestElProxyNoCuentaLasSondasSinTokenComoActividad(t *testing.T) {
	a := nuevoArnes(t)
	a.encenderYEsperar()
	destino, _ := url.Parse(a.modelos[0].URL)
	p := httptest.NewServer(proxy.Manejador(proxy.Opciones{Destino: destino, Actividad: a.act, Disponible: a.svc.Disponible, Log: a.deps.Log}))
	t.Cleanup(p.Close)
	for range 5 {
		a.reloj.Avanzar(3 * time.Minute)
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, p.URL+"/", nil) // lo que envia /readyz/modelos de la API, sin token
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		a.svc.Tick(t.Context())
	}
	a.esperarFase(estado.Apagado)
}

func TestSesionMaximaForzaElApagadoAunConActividad(t *testing.T) {
	a := nuevoArnes(t, func(c *config.Config) { c.SesionMaxima = time.Hour })
	a.encenderYEsperar()
	for range 11 {
		a.reloj.Avanzar(5 * time.Minute)
		a.act.Inicio()
		a.act.Fin()
		a.svc.Tick(t.Context())
	}
	if a.svc.Vista().Estado != "listo" {
		t.Fatalf("a los 55 minutos sigue vigente: %+v", a.svc.Vista())
	}
	a.reloj.Avanzar(6 * time.Minute)
	a.act.Inicio()
	a.act.Fin()
	a.svc.Tick(t.Context())
	v := a.esperarFase(estado.Apagado)
	if !strings.Contains(v.Mensaje, "sesion maxima") || a.nube.Vivos("vera-modelos") != 0 {
		t.Fatalf("vista %+v", v)
	}
}

func TestApagarReintentaElDeleteHastaVerificar(t *testing.T) {
	a := nuevoArnes(t)
	a.encenderYEsperar()
	a.nube.FallosDelete = 3
	a.svc.Apagar()
	a.esperarFase(estado.Apagado)
	if a.nube.Vivos("vera-modelos") != 0 {
		t.Fatal("el pod debe estar terminado")
	}
}

func TestSiNoSePuedeTerminarElEstadoErrorEsVisibleYSePuedeReintentar(t *testing.T) {
	a := nuevoArnes(t)
	v := a.encenderYEsperar()
	a.nube.FallosDelete = 1000
	a.svc.Apagar()
	visto := a.esperarFase(estado.Error)
	if visto.PodID != v.PodID || !strings.Contains(visto.Mensaje, "NO se pudo terminar") || !strings.Contains(a.registro.String(), "ALERTA") {
		t.Fatalf("vista %+v", visto)
	}
	if _, _, err := a.svc.Encender(); err == nil {
		t.Fatal("con un pod en error no se puede encender otro: hay que apagar primero")
	}
	a.nube.FallosDelete = 0
	a.svc.Apagar()
	a.esperarFase(estado.Apagado)
	if a.nube.Vivos("vera-modelos") != 0 {
		t.Fatal("el segundo intento debio terminar el pod")
	}
}

func TestApagarDuranteElEncendidoCancelaYNoDejaPod(t *testing.T) {
	a := nuevoArnes(t)
	a.ssh.Modo("rechaza:100000")
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	for a.svc.Vista().PodID == "" {
		time.Sleep(time.Millisecond)
	}
	a.svc.Apagar()
	a.esperarFase(estado.Apagado)
	if a.nube.Vivos("vera-modelos") != 0 {
		t.Fatal("no debe quedar pod")
	}
}

func TestSiElPodDesapareceSeInformaError(t *testing.T) {
	a := nuevoArnes(t)
	v := a.encenderYEsperar()
	cliente := a.deps.Nube
	if err := cliente.Terminar(t.Context(), v.PodID); err != nil {
		t.Fatal(err)
	}
	a.svc.Tick(t.Context())
	got := a.esperarFase(estado.Error)
	if got.PodID != "" || a.svc.Disponible() {
		t.Fatalf("vista %+v", got)
	}
}

func TestEncenderListoCuentaComoActividad(t *testing.T) {
	a := nuevoArnes(t)
	a.encenderYEsperar()
	a.reloj.Avanzar(9 * time.Minute)
	if _, iniciado, err := a.svc.Encender(); err != nil || iniciado {
		t.Fatalf("iniciado=%v err=%v", iniciado, err)
	}
	a.reloj.Avanzar(9 * time.Minute)
	a.svc.Tick(t.Context())
	if a.svc.Vista().Estado != "listo" {
		t.Fatalf("un encendido reciente cuenta como actividad: %+v", a.svc.Vista())
	}
}

func TestVistaInformaMinutosYCosto(t *testing.T) {
	a := nuevoArnes(t)
	a.encenderYEsperar()
	a.reloj.Avanzar(time.Hour)
	v := a.svc.Vista()
	if v.MinutosEncendido < 60 || v.CostoEstimadoUSD < 0.269 || v.CostoEstimadoUSD > 0.29 || v.MinutosHastaApagadoPorInactividad == nil ||
		*v.MinutosHastaApagadoPorInactividad != 0 || v.MinutosHastaSesionMaxima == nil || *v.MinutosHastaSesionMaxima < 179 || *v.MinutosHastaSesionMaxima > 180 {
		t.Fatalf("vista %+v", v)
	}
	apagado := nuevoArnes(t).svc.Vista()
	if apagado.Estado != "apagado" || apagado.MinutosHastaApagadoPorInactividad != nil || apagado.CostoEstimadoUSD != 0 {
		t.Fatalf("vista apagada %+v", apagado)
	}
}

func TestRecuperarTerminaUnHuerfanoConElEstadoApagado(t *testing.T) {
	a := nuevoArnes(t)
	huerfano := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora())
	ajeno := a.nube.AnadirPod("tesis-campana", "RUNNING", a.reloj.Ahora())
	if err := a.svc.Recuperar(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.esperarFase(estado.Apagado)
	if a.nube.Existe(huerfano) || !a.nube.Existe(ajeno) {
		t.Fatalf("huerfano vivo=%v ajeno vivo=%v", a.nube.Existe(huerfano), a.nube.Existe(ajeno))
	}
}

func TestRecuperarRetomaUnPodListoTrasReiniciar(t *testing.T) {
	a := nuevoArnes(t)
	v := a.encenderYEsperar()
	a.svc.Cerrar() // el proceso muere sin terminar el pod
	segundo, err := Nuevo(a.deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(segundo.Cerrar)
	if segundo.Vista().Estado != "listo" || segundo.Disponible() {
		t.Fatalf("antes de reconciliar no hay tunel: %+v", segundo.Vista())
	}
	if err := segundo.Recuperar(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !segundo.Disponible() || segundo.Vista().PodID != v.PodID || a.nube.Vivos("vera-modelos") != 1 {
		t.Fatalf("vista %+v", segundo.Vista())
	}
}

func TestRecuperarConEstadoListoPeroSinPodQuedaApagado(t *testing.T) {
	a := nuevoArnes(t)
	if err := a.almacen.Guardar(estado.Estado{Fase: estado.Listo, PodID: "pod9999", SesionInicio: a.reloj.Ahora()}); err != nil {
		t.Fatal(err)
	}
	segundo, _ := Nuevo(a.deps)
	t.Cleanup(segundo.Cerrar)
	if err := segundo.Recuperar(t.Context()); err != nil {
		t.Fatal(err)
	}
	if v := segundo.Vista(); v.Estado != "apagado" || v.PodID != "" {
		t.Fatalf("vista %+v", v)
	}
}

func TestRecuperarTerminaUnEncendidoInterrumpido(t *testing.T) {
	a := nuevoArnes(t)
	id := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora())
	if err := a.almacen.Guardar(estado.Estado{Fase: estado.Encendiendo, PodID: id, SesionInicio: a.reloj.Ahora()}); err != nil {
		t.Fatal(err)
	}
	segundo, _ := Nuevo(a.deps)
	t.Cleanup(segundo.Cerrar)
	if err := segundo.Recuperar(t.Context()); err != nil {
		t.Fatal(err)
	}
	for segundo.Vista().Estado != "apagado" {
		time.Sleep(time.Millisecond)
	}
	if a.nube.Existe(id) {
		t.Fatal("el pod de un encendido interrumpido debe terminarse")
	}
}

func TestRecuperarSinPoderListarDejaErrorVisible(t *testing.T) {
	a := nuevoArnes(t)
	a.nube.ListarFalla = true
	if err := a.svc.Recuperar(t.Context()); err == nil {
		t.Fatal("esperaba error")
	}
	if a.svc.Vista().Estado != "error" {
		t.Fatalf("vista %+v", a.svc.Vista())
	}
}

func TestLaClaveNuncaApareceEnRegistrosArchivosNiArgv(t *testing.T) {
	a := nuevoArnes(t)
	a.encenderYEsperar()
	a.reloj.Avanzar(11 * time.Minute)
	a.svc.Tick(t.Context())
	a.esperarFase(estado.Apagado)
	a.nube.Crear = []falsos.RespuestaCrear{{Estado: 500}, {Estado: 500}, {Estado: 500}}
	a.ssh.Modo("fallo_script")
	if _, _, err := a.svc.Encender(); err == nil {
		a.esperarFase(estado.Error)
	}
	vista, _ := jsonDe(a.svc.Vista())
	superficies := map[string]string{"registro": a.registro.String(), "argv": a.ssh.Argv(), "env": a.ssh.Env(), "vista": vista}
	_ = filepath.Walk(a.cfg.DirEstado, func(ruta string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			datos, _ := os.ReadFile(ruta)
			superficies["archivo "+ruta] = string(datos)
		}
		return nil
	})
	for nombre, texto := range superficies {
		for _, secreto := range []string{claveRunPod, tokenSTT, tokenVoz} {
			if strings.Contains(texto, secreto) {
				t.Errorf("%s contiene un secreto", nombre)
			}
		}
	}
	for aut := range a.nube.Autorizaciones() {
		if aut != "Bearer "+claveRunPod {
			t.Errorf("autorizacion inesperada %q", aut)
		}
	}
}

func jsonDe(v any) (string, error) {
	datos, err := json.Marshal(v)
	return string(datos), err
}
