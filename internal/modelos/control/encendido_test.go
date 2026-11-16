package control

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/falsos"
)

func TestEncendidoFelizLlegaAListoYPersisteElEstado(t *testing.T) {
	a := nuevoArnes(t)
	a.nube.GetsHastaRun = 3
	v, iniciado, err := a.svc.Encender()
	if err != nil || !iniciado || v.Estado != "encendiendo" {
		t.Fatalf("v=%+v iniciado=%v err=%v", v, iniciado, err)
	}
	v = a.esperarFase(estado.Listo)
	if v.GPU != "NVIDIA RTX A5000" || v.TarifaUSDH != 0.27 || v.PodID == "" {
		t.Fatalf("vista %+v", v)
	}
	if !a.svc.Disponible() {
		t.Fatal("con el pod listo y el tunel vivo el proxy debe estar disponible")
	}
	guardado, err := a.almacen.Cargar()
	if err != nil || guardado.Fase != estado.Listo || guardado.PodID != v.PodID {
		t.Fatalf("estado en disco %+v err=%v", guardado, err)
	}
	if a.nube.Vivos("vera-modelos") != 1 {
		t.Fatal("debe haber exactamente un pod")
	}
	var cuerpo struct {
		Env   map[string]string `json:"env"`
		Cloud string            `json:"cloud"`
		Ports []string          `json:"ports"`
	}
	if err := json.Unmarshal(a.nube.Cuerpos()[0], &cuerpo); err != nil {
		t.Fatal(err)
	}
	if cuerpo.Cloud != "SECURE" || cuerpo.Env["PUBLIC_KEY"] != "ssh-ed25519 AAAATESTKEY vm" || len(cuerpo.Ports) != 1 || cuerpo.Ports[0] != "22/tcp" {
		t.Fatalf("cuerpo de creacion %s", a.nube.Cuerpos()[0])
	}
	if len(cuerpo.Env) != 1 {
		t.Fatal("los tokens de los modelos no deben ir en el env del pod, que RunPod devuelve en sus respuestas")
	}
	stdin := a.ssh.Stdin()
	for _, esperado := range []string{"WHISPER_TOKEN='" + tokenSTT + "'", "KOKORO_TOKEN='" + tokenVoz + "'", "REPO_SHA='abc123'", "exec bash /tmp/vera-arranque.sh"} {
		if !strings.Contains(stdin, esperado) {
			t.Errorf("falta %q en el stdin del arranque", esperado)
		}
	}
	if strings.Contains(a.ssh.Argv(), tokenSTT) || strings.Contains(a.ssh.Argv(), tokenVoz) {
		t.Fatal("los tokens no pueden aparecer en argv")
	}
	if !strings.Contains(a.registro.String(), "POD CREADO id="+v.PodID) {
		t.Fatalf("falta el aviso inmediato del id en el registro:\n%s", a.registro.String())
	}
}

func TestEncenderEsIdempotenteYSoloHayUnPod(t *testing.T) {
	a := nuevoArnes(t)
	if _, iniciado, err := a.svc.Encender(); err != nil || !iniciado {
		t.Fatalf("iniciado=%v err=%v", iniciado, err)
	}
	if _, iniciado, err := a.svc.Encender(); err != nil || iniciado {
		t.Fatalf("segundo encendido iniciado=%v err=%v", iniciado, err)
	}
	a.esperarFase(estado.Listo)
	if _, iniciado, err := a.svc.Encender(); err != nil || iniciado {
		t.Fatalf("encendido estando listo iniciado=%v err=%v", iniciado, err)
	}
	creaciones := 0
	for _, l := range a.nube.Llamadas() {
		if l == "POST /v2/pods" {
			creaciones++
		}
	}
	if creaciones != 1 {
		t.Fatalf("se crearon %d pods", creaciones)
	}
}

func TestSinExistenciasEnNingunaGPU(t *testing.T) {
	a := nuevoArnes(t)
	a.nube.Catalogo["NVIDIA RTX A5000"] = falsos.EntradaCatalogo{Secure: true, Disponibilidad: "NONE", PrecioSecure: 0.27}
	a.nube.Catalogo["NVIDIA RTX A4000"] = falsos.EntradaCatalogo{Secure: true, Disponibilidad: "NONE", PrecioSecure: 0.2}
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.SinExistencias)
	if !strings.Contains(v.Mensaje, "NVIDIA RTX A5000") || a.nube.Vivos("vera-modelos") != 0 {
		t.Fatalf("vista %+v", v)
	}
	for _, l := range a.nube.Llamadas() {
		if l == "POST /v2/pods" {
			t.Fatal("sin existencias no se debe intentar crear")
		}
	}
	if a.svc.Disponible() {
		t.Fatal("sin pod no hay modelos")
	}
}

func TestUsaLaGPUDeRespaldoCuandoLaPreferidaNoTieneExistencias(t *testing.T) {
	a := nuevoArnes(t)
	a.nube.Catalogo["NVIDIA RTX A5000"] = falsos.EntradaCatalogo{Secure: true, Disponibilidad: "NONE", PrecioSecure: 0.27}
	a.nube.Catalogo["NVIDIA RTX A4000"] = falsos.EntradaCatalogo{Secure: true, Disponibilidad: "MEDIUM", PrecioSecure: 0.2}
	v := a.encenderYEsperar()
	if v.GPU != "NVIDIA RTX A4000" {
		t.Fatalf("gpu %s", v.GPU)
	}
}

func TestLaTarifaDelCatalogoSobreElTopeSeRechazaSinCrear(t *testing.T) {
	a := nuevoArnes(t)
	a.nube.Catalogo["NVIDIA RTX A5000"] = falsos.EntradaCatalogo{Secure: true, Disponibilidad: "HIGH", PrecioSecure: 0.89}
	a.nube.Catalogo["NVIDIA RTX A4000"] = falsos.EntradaCatalogo{Secure: true, Disponibilidad: "HIGH", PrecioSecure: 0.50}
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.SinExistencias)
	if !strings.Contains(v.Mensaje, "tope") {
		t.Fatalf("el mensaje debe explicar el tope: %+v", v)
	}
	if len(a.nube.Cuerpos()) != 0 {
		t.Fatal("no debe crear nada por encima del tope")
	}
}

func TestTarifaDeLaRespuestaDeCreacionSobreElTopeTerminaElPod(t *testing.T) {
	for nombre, resp := range map[string]falsos.RespuestaCrear{
		"sobre el tope": {Costo: falsos.Costo(0.89)},
		"sin costo":     {SinCosto: true},
		"costo cero":    {Costo: falsos.Costo(0)},
		"otra nube":     {Nube: "COMMUNITY"},
		"otra GPU":      {GPU: "NVIDIA GeForce RTX 4090"},
	} {
		t.Run(nombre, func(t *testing.T) {
			a := nuevoArnes(t)
			a.nube.Crear = []falsos.RespuestaCrear{resp}
			if _, _, err := a.svc.Encender(); err != nil {
				t.Fatal(err)
			}
			v := a.esperarFase(estado.Error)
			if a.nube.Vivos("vera-modelos") != 0 || v.PodID != "" {
				t.Fatalf("el pod debio terminarse: vivos=%d vista=%+v", a.nube.Vivos("vera-modelos"), v)
			}
			if !strings.Contains(a.registro.String(), "POD CREADO id=") {
				t.Fatal("el id debe imprimirse apenas se crea, incluso si luego se rechaza")
			}
		})
	}
}

func TestFalloDeCreacionSePropagaSinPodHuerfano(t *testing.T) {
	casos := map[string]struct {
		estado int
		fase   estado.Fase
	}{
		"saldo insuficiente (402)": {402, estado.Error},
		"cuerpo invalido (422)":    {422, estado.Error},
		"sin capacidad (400)":      {400, estado.SinExistencias},
	}
	for nombre, c := range casos {
		t.Run(nombre, func(t *testing.T) {
			a := nuevoArnes(t)
			a.nube.Crear = []falsos.RespuestaCrear{{Estado: c.estado}, {Estado: c.estado}}
			if _, _, err := a.svc.Encender(); err != nil {
				t.Fatal(err)
			}
			a.esperarFase(c.fase)
			if a.nube.Vivos("vera-modelos") != 0 {
				t.Fatal("no debe quedar ningun pod")
			}
		})
	}
}

func TestHuellaSshDistintaFallaConAlertaYTerminaElPod(t *testing.T) {
	a := nuevoArnes(t)
	a.ssh.Modo("huella")
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.Error)
	if !strings.Contains(v.Mensaje, "huella") || a.nube.Vivos("vera-modelos") != 0 {
		t.Fatalf("vista %+v vivos=%d", v, a.nube.Vivos("vera-modelos"))
	}
	if !strings.Contains(a.registro.String(), "ALERTA") {
		t.Fatalf("falta la alerta de huella en el registro:\n%s", a.registro.String())
	}
}

func TestEsperaAQueSshAcepteAntesDeArrancar(t *testing.T) {
	a := nuevoArnes(t)
	a.ssh.Modo("rechaza:3")
	a.encenderYEsperar()
	if a.ssh.Comprobaciones() < 4 {
		t.Fatalf("comprobaciones=%d", a.ssh.Comprobaciones())
	}
}

func TestFalloDelScriptDeArranqueTerminaElPod(t *testing.T) {
	a := nuevoArnes(t)
	a.ssh.Modo("fallo_script")
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.Error)
	if !strings.Contains(v.Mensaje, "arranque") || a.nube.Vivos("vera-modelos") != 0 {
		t.Fatalf("vista %+v", v)
	}
}

func TestModelosQueNoResponden(t *testing.T) {
	a := nuevoArnes(t, func(c *config.Config) { c.EsperaModelos = 30 * time.Second })
	a.modelos[1].Close() // STT caido
	if _, _, err := a.svc.Encender(); err != nil {
		t.Fatal(err)
	}
	v := a.esperarFase(estado.Error)
	if !strings.Contains(v.Mensaje, "modelos") || a.nube.Vivos("vera-modelos") != 0 {
		t.Fatalf("vista %+v", v)
	}
}

func TestSinScriptDeArranqueNoCreaNadaYResponde412(t *testing.T) {
	a := nuevoArnes(t, func(c *config.Config) { c.ScriptArranque = "/ruta/que/no/existe.sh" })
	_, _, err := a.svc.Encender()
	if err == nil || !strings.Contains(err.Error(), "script") {
		t.Fatalf("err=%v", err)
	}
	if len(a.nube.Llamadas()) != 0 {
		t.Fatal("no debe tocar RunPod si falta el script")
	}
}

func TestEncenderConUnHuerfanoPreviamenteLoTermina(t *testing.T) {
	a := nuevoArnes(t)
	huerfano := a.nube.AnadirPod("vera-modelos", "RUNNING", a.reloj.Ahora())
	a.encenderYEsperar()
	if a.nube.Existe(huerfano) || a.nube.Vivos("vera-modelos") != 1 {
		t.Fatalf("huerfano vivo=%v vivos=%d", a.nube.Existe(huerfano), a.nube.Vivos("vera-modelos"))
	}
}
