package proxy

import (
	"bufio"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nuevoProxy(t *testing.T, destino string, disponible func() bool) (*httptest.Server, *Actividad) {
	t.Helper()
	u, err := url.Parse(destino)
	if err != nil {
		t.Fatal(err)
	}
	act := NuevaActividad(time.Now)
	p := httptest.NewServer(Manejador(Opciones{Destino: u, Actividad: act, Disponible: disponible, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	t.Cleanup(p.Close)
	return p, act
}

func siempre() bool { return true }

func TestApagadoDevuelve503JsonConRetryAfterYNoCuentaActividad(t *testing.T) {
	llamado := false
	respaldo := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { llamado = true }))
	t.Cleanup(respaldo.Close)
	p, act := nuevoProxy(t, respaldo.URL, func() bool { return false })
	res, err := http.Post(p.URL+"/api/chat", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	cuerpo, _ := io.ReadAll(res.Body)
	if res.StatusCode != 503 || strings.TrimSpace(string(cuerpo)) != `{"error":"modelos_apagados"}` || res.Header.Get("Retry-After") == "" ||
		!strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("status=%d cuerpo=%q cab=%v", res.StatusCode, cuerpo, res.Header)
	}
	if llamado || !act.Ultima().IsZero() {
		t.Fatal("con el pod apagado no debe llegar nada al destino ni contar actividad")
	}
}

func TestReenviaCuerpoYCabecerasYCuentaActividad(t *testing.T) {
	respaldo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cuerpo, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Eco-Token", r.Header.Get("X-Vera-Token"))
		_, _ = w.Write([]byte("eco:" + string(cuerpo) + ":" + r.URL.Path))
	}))
	t.Cleanup(respaldo.Close)
	p, act := nuevoProxy(t, respaldo.URL, siempre)
	req, _ := http.NewRequest(http.MethodPost, p.URL+"/transcribir", strings.NewReader("audio"))
	req.Header.Set("X-Vera-Token", "tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	cuerpo, _ := io.ReadAll(res.Body)
	if string(cuerpo) != "eco:audio:/transcribir" || res.Header.Get("X-Eco-Token") != "tok" {
		t.Fatalf("cuerpo=%q cab=%v", cuerpo, res.Header)
	}
	if act.Ultima().IsZero() {
		t.Fatal("una peticion real debe contar como actividad")
	}
}

func TestLasSondasDeSaludNoCuentanComoActividad(t *testing.T) {
	respaldo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(respaldo.Close)
	p, act := nuevoProxy(t, respaldo.URL, siempre)
	for _, ruta := range []string{"/", "/healthz", "/readyz", "/api/version"} {
		res, err := http.Get(p.URL + ruta)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
	}
	if !act.Ultima().IsZero() {
		t.Fatal("GET /, /healthz, /readyz y /api/version no deben contar como actividad")
	}
	res, err := http.Get(p.URL + "/api/tags")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if act.Ultima().IsZero() {
		t.Fatal("GET /api/tags si es uso real")
	}
}

func TestStreamingSinBufferYPeticionEnVuelo(t *testing.T) {
	liberar := make(chan struct{})
	respaldo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "primero\n")
		w.(http.Flusher).Flush()
		<-liberar
		_, _ = io.WriteString(w, "segundo\n")
	}))
	t.Cleanup(respaldo.Close)
	p, act := nuevoProxy(t, respaldo.URL, siempre)
	res, err := http.Post(p.URL+"/api/chat", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	lector := bufio.NewReader(res.Body)
	linea, err := lector.ReadString('\n')
	if err != nil || linea != "primero\n" {
		t.Fatalf("el primer fragmento debe llegar antes de que termine el destino: %q %v", linea, err)
	}
	if act.EnVuelo() != 1 {
		t.Fatalf("en vuelo = %d, esperaba 1 durante el stream", act.EnVuelo())
	}
	close(liberar)
	if resto, _ := io.ReadAll(lector); string(resto) != "segundo\n" {
		t.Fatalf("resto %q", resto)
	}
	deadline := time.Now().Add(2 * time.Second)
	for act.EnVuelo() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if act.EnVuelo() != 0 {
		t.Fatal("la peticion debio salir de vuelo")
	}
}

func TestDestinoCaidoDevuelve503(t *testing.T) {
	respaldo := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := respaldo.URL
	respaldo.Close()
	p, _ := nuevoProxy(t, url, siempre)
	res, err := http.Get(p.URL + "/api/tags")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	cuerpo, _ := io.ReadAll(res.Body)
	if res.StatusCode != 503 || strings.TrimSpace(string(cuerpo)) != `{"error":"modelos_apagados"}` {
		t.Fatalf("status=%d cuerpo=%q", res.StatusCode, cuerpo)
	}
}

func TestRespuestaLentaDeUnLLMNoSeCorta(t *testing.T) {
	var servidas atomic.Int32
	respaldo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond)
		servidas.Add(1)
		_, _ = w.Write([]byte("lento"))
	}))
	t.Cleanup(respaldo.Close)
	p, _ := nuevoProxy(t, respaldo.URL, siempre)
	res, err := http.Post(p.URL+"/api/generate", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 200 || servidas.Load() != 1 {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestActividadConRelojInyectado(t *testing.T) {
	ahora := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	act := NuevaActividad(func() time.Time { return ahora })
	act.Inicio()
	act.Fin()
	if !act.Ultima().Equal(ahora) || act.EnVuelo() != 0 {
		t.Fatalf("ultima=%v enVuelo=%d", act.Ultima(), act.EnVuelo())
	}
	act.Marcar(ahora.Add(time.Minute))
	if !act.Ultima().Equal(ahora.Add(time.Minute)) {
		t.Fatal("Marcar debe adelantar la ultima actividad")
	}
	act.Marcar(ahora)
	if !act.Ultima().Equal(ahora.Add(time.Minute)) {
		t.Fatal("Marcar no debe retroceder")
	}
}
