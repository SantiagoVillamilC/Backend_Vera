package runpod

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/falsos"
)

const claveFalsa = "rpa_CLAVE_DE_PRUEBA_0123456789"

func nuevoCliente(t *testing.T) (*Cliente, *falsos.RunPod) {
	t.Helper()
	f := falsos.NuevoRunPod(t, claveFalsa)
	c, err := Nuevo(Config{BaseURL: f.URL + "/v2", Clave: claveFalsa})
	if err != nil {
		t.Fatal(err)
	}
	return c, f
}

func solicitud() SolicitudPod {
	return SolicitudPod{Nombre: "vera-modelos", Imagen: "runpod/pytorch:1.0.2-cu1281-torch280-ubuntu2404",
		GPU: "NVIDIA RTX A5000", DiscoGB: 40, LlavePublica: "ssh-ed25519 AAAATESTKEY vm"}
}

func TestCuerpoDeCreacionTieneSoloLasClavesDeLaV2(t *testing.T) {
	datos, err := json.Marshal(solicitud().cuerpo())
	if err != nil {
		t.Fatal(err)
	}
	const esperado = `{"name":"vera-modelos","image":"runpod/pytorch:1.0.2-cu1281-torch280-ubuntu2404","gpu":{"id":"NVIDIA RTX A5000","count":1},` +
		`"cloud":"SECURE","disk":40,"ports":["22/tcp"],"env":{"PUBLIC_KEY":"ssh-ed25519 AAAATESTKEY vm"},"startSsh":true}`
	if string(datos) != esperado {
		t.Fatalf("cuerpo\n got: %s\nwant: %s", datos, esperado)
	}
}

func TestCrearDevuelvePodYLaClaveSoloViajaEnAuthorization(t *testing.T) {
	c, f := nuevoCliente(t)
	pod, err := c.Crear(t.Context(), solicitud())
	if err != nil {
		t.Fatal(err)
	}
	if pod.ID == "" || !pod.CostoLegible || pod.Costo != 0.27 || pod.Nube != "SECURE" || pod.GPU != "NVIDIA RTX A5000" {
		t.Fatalf("pod %+v", pod)
	}
	if aut := f.Autorizaciones(); len(aut) != 1 || aut["Bearer "+claveFalsa] != 1 {
		t.Fatalf("autorizaciones %v", aut)
	}
	for _, cuerpo := range f.Cuerpos() {
		if strings.Contains(string(cuerpo), claveFalsa) {
			t.Fatal("la clave aparece en el cuerpo de la peticion")
		}
	}
}

func TestCrearSinCostoLegible(t *testing.T) {
	c, f := nuevoCliente(t)
	f.Crear = []falsos.RespuestaCrear{{SinCosto: true}}
	pod, err := c.Crear(t.Context(), solicitud())
	if err != nil || pod.CostoLegible {
		t.Fatalf("pod=%+v err=%v", pod, err)
	}
}

func TestListarVivosPaginaYFiltraPorNombre(t *testing.T) {
	c, f := nuevoCliente(t)
	f.PaginaTam = 1
	ahora := time.Now()
	f.AnadirPod("vera-modelos", "RUNNING", ahora)
	f.AnadirPod("tesis-campana", "RUNNING", ahora)
	f.AnadirPod("vera-modelos", "EXITED", ahora)
	muerto := f.AnadirPod("vera-modelos", "RUNNING", ahora)
	if err := c.Terminar(t.Context(), muerto); err != nil {
		t.Fatal(err)
	}
	pods, err := c.ListarVivos(t.Context(), "vera-modelos")
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 2 {
		t.Fatalf("esperaba 2 pods vivos (RUNNING y EXITED), hubo %d: %+v", len(pods), pods)
	}
	paginas := 0
	for _, l := range f.Llamadas() {
		if l == "GET /v2/pods" {
			paginas++
		}
	}
	if paginas < 3 {
		t.Fatalf("no recorrio la paginacion: %d paginas", paginas)
	}
}

func TestObtenerLeeSSHDirectoYFallbackDeRuntime(t *testing.T) {
	c, f := nuevoCliente(t)
	id := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	pod, err := c.Obtener(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if pod.SSH == nil || pod.SSH.Host != "203.0.113.7" || pod.SSH.Puerto != 40022 || pod.SSH.Usuario != "root" {
		t.Fatalf("ssh %+v", pod.SSH)
	}
	var cuerpo = `{"id":"p1","name":"vera-modelos","status":"RUNNING","ssh":{"direct":null},"runtime":{"ports":[{"private":8888,"public":1,"ip":"198.51.100.1","type":"http"},{"private":22,"public":43210,"ip":"198.51.100.9","type":"tcp"}]}}`
	pod, err = decodificarPod([]byte(cuerpo))
	if err != nil || pod.SSH == nil || pod.SSH.Host != "198.51.100.9" || pod.SSH.Puerto != 43210 || pod.SSH.Usuario != "root" {
		t.Fatalf("pod=%+v err=%v", pod, err)
	}
}

func TestObtenerRechazaDestinoSSHPeligroso(t *testing.T) {
	for _, cuerpo := range []string{
		`{"id":"p1","name":"x","status":"RUNNING","ssh":{"direct":{"host":"-oProxyCommand=x","port":22,"username":"root"}}}`,
		`{"id":"p1","name":"x","status":"RUNNING","ssh":{"direct":{"host":"1.2.3.4","port":22,"username":"-l"}}}`,
		`{"id":"p1","name":"x","status":"RUNNING","ssh":{"direct":{"host":"1.2.3.4","port":70000,"username":"root"}}}`,
		`{"id":"../x","name":"x","status":"RUNNING"}`,
	} {
		if _, err := decodificarPod([]byte(cuerpo)); !errors.Is(err, ErrRespuestaInvalida) {
			t.Fatalf("cuerpo %s: err=%v", cuerpo, err)
		}
	}
}

func TestErroresNoFiltranLaClaveYSeClasifican(t *testing.T) {
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/pods/a404":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"title":"Not Found","status":404,"detail":"pod not found"}`))
		case "/v2/pods/a429":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"title":"Too Many","status":429,"detail":"slow down"}`))
		default:
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"title":"Unauthorized","status":401,"detail":"la clave ` + claveFalsa + ` es mala"}`))
		}
	}))
	t.Cleanup(servidor.Close)
	c, err := Nuevo(Config{BaseURL: servidor.URL + "/v2", Clave: claveFalsa})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Obtener(t.Context(), "a404"); !errors.Is(err, ErrNoExiste) {
		t.Fatalf("404: %v", err)
	}
	_, err = c.Obtener(t.Context(), "a429")
	var api *ErrorAPI
	if !errors.As(err, &api) || api.Estado != 429 || api.ReintentarEn != 7*time.Second || !api.Reintentable() {
		t.Fatalf("429: %v", err)
	}
	_, err = c.Obtener(t.Context(), "otro")
	if err == nil || strings.Contains(err.Error(), claveFalsa) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401: %v", err)
	}
	if errors.As(err, &api) && api.Reintentable() {
		t.Fatal("un 401 no es reintentable")
	}
}

func TestOfertaLeeDisponibilidadYTarifaSecure(t *testing.T) {
	c, f := nuevoCliente(t)
	f.Catalogo["NVIDIA RTX A4000"] = falsos.EntradaCatalogo{Secure: true, Disponibilidad: "NONE", PrecioSecure: 0.2}
	o, err := c.Oferta(t.Context(), "NVIDIA RTX A5000")
	if err != nil || !o.Encontrada || !o.Secure || o.Disponibilidad != "HIGH" || o.TarifaSecure != 0.27 {
		t.Fatalf("oferta %+v err=%v", o, err)
	}
	o, err = c.Oferta(t.Context(), "NVIDIA RTX A4000")
	if err != nil || o.Disponibilidad != "NONE" {
		t.Fatalf("oferta %+v err=%v", o, err)
	}
	o, err = c.Oferta(t.Context(), "NVIDIA NO EXISTE")
	if err != nil || o.Encontrada {
		t.Fatalf("oferta %+v err=%v", o, err)
	}
}

func TestTerminarYNoExiste(t *testing.T) {
	c, f := nuevoCliente(t)
	id := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	if err := c.Terminar(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if err := c.Terminar(t.Context(), id); !errors.Is(err, ErrNoExiste) {
		t.Fatalf("segundo DELETE: %v", err)
	}
	if err := c.Terminar(t.Context(), "../etc"); err == nil {
		t.Fatal("un id peligroso no debe llegar a la URL")
	}
}

func TestBaseURLInsegura(t *testing.T) {
	for _, base := range []string{"http://api.runpod.io/v2", "ftp://x", "", "https://user:pw@api.runpod.io/v2"} {
		if _, err := Nuevo(Config{BaseURL: base, Clave: claveFalsa}); err == nil {
			t.Fatalf("la base %q debio rechazarse", base)
		}
	}
	if _, err := Nuevo(Config{BaseURL: "https://api.runpod.io/v2", Clave: ""}); err == nil {
		t.Fatal("sin clave debe fallar")
	}
}

func TestCrearEncoladoCancelaYFalla(t *testing.T) {
	var borrado bool
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v2/queued-pods/q123" {
			borrado = true
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Location", "/v2/queued-pods/q123")
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(servidor.Close)
	c, _ := Nuevo(Config{BaseURL: servidor.URL + "/v2", Clave: claveFalsa})
	if _, err := c.Crear(t.Context(), solicitud()); !errors.Is(err, ErrEncolado) || !borrado {
		t.Fatalf("err=%v borrado=%v", err, borrado)
	}
}
