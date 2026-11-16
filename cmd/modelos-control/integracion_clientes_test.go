package main

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/destinolocal"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/sttlocal"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/vozlocal"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/falsos"
)

// Prueba de punta a punta entre dos piezas que se desarrollaron por separado: los clientes de la API
// (vozlocal y sttlocal, que aceptan una IPv4 privada exacta autorizada por VERA_MODELOS_HOST_PERMITIDO) y
// modelos-control (que escucha en esa IP). La IP privada es la de una interfaz real de la máquina, el
// equivalente al gateway del puente de Docker de la VM. Sin una IP privada se omite.

// ipPrivadaLocal devuelve una IPv4 privada de una interfaz de esta máquina que no sea loopback.
func ipPrivadaLocal(t *testing.T) string {
	t.Helper()
	direcciones, err := net.InterfaceAddrs()
	if err != nil {
		t.Skipf("no se pudieron leer las interfaces: %v", err)
	}
	for _, d := range direcciones {
		red, ok := d.(*net.IPNet)
		if !ok || red.IP.To4() == nil || red.IP.IsLoopback() || !red.IP.IsPrivate() {
			continue
		}
		// Se comprueba que se pueda ligar: una interfaz caída aparece en la lista pero no escucha.
		l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", net.JoinHostPort(red.IP.String(), "0"))
		if err != nil {
			continue
		}
		_ = l.Close()
		return red.IP.String()
	}
	t.Skip("esta máquina no tiene una IPv4 privada ligable fuera de loopback")
	return ""
}

func wavMinimo() []byte {
	datos := make([]byte, 4800)
	b := make([]byte, 0, 44+len(datos))
	b = append(b, "RIFF"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(36+len(datos)))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint32(b, 24000)
	b = binary.LittleEndian.AppendUint32(b, 48000)
	b = binary.LittleEndian.AppendUint16(b, 2)
	b = binary.LittleEndian.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(datos)))
	return append(b, datos...)
}

// modelosFalsos son los tres servidores que el túnel dejaría en los extremos locales: el contrato de
// la sonda de modelos-control (Ollama /api/version sin token, Whisper y Kokoro /healthz con X-Vera-Token) más
// las rutas que usan los clientes de la API.
func modelosFalsos(t *testing.T) (ollama, stt, voz *httptest.Server) {
	t.Helper()
	conToken := func(token string, h http.HandlerFunc) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Vera-Token") != token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}))
		t.Cleanup(s.Close)
		return s
	}
	ollama = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"version":"0.40.10"}`)
	}))
	t.Cleanup(ollama.Close)
	stt = conToken(tokenSTT, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = io.WriteString(w, "ok")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"transcripcion":"hola vera","modelo":"small","idioma":"es"}`)
	})
	voz = conToken(tokenVoz, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = io.WriteString(w, "ok")
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wavMinimo())
	})
	return ollama, stt, voz
}

func llamarControl(t *testing.T, direccion, metodo, ruta string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), metodo, "http://"+direccion+ruta, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenControl)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = res.Body.Close() }()
	cuerpo, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(cuerpo)
}

func esperarEstado(t *testing.T, direccion, estado string) {
	t.Helper()
	limite := time.Now().Add(15 * time.Second)
	for {
		if codigo, cuerpo := llamarControl(t, direccion, http.MethodGet, "/estado"); codigo == 200 && strings.Contains(cuerpo, `"estado":"`+estado+`"`) {
			return
		}
		if time.Now().After(limite) {
			_, cuerpo := llamarControl(t, direccion, http.MethodGet, "/estado")
			t.Fatalf("el estado %q no llegó, está en %s", estado, cuerpo)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func raw503(t *testing.T, url string) {
	t.Helper()
	res, err := http.Get(url) //nolint:gosec,noctx // prueba contra un servidor local
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	cuerpo, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || strings.TrimSpace(string(cuerpo)) != `{"error":"modelos_apagados"}` {
		t.Fatalf("GET %s: status=%d cuerpo=%s", url, res.StatusCode, cuerpo)
	}
}

func TestClientesDeLaAPIAlcanzanModelosControlEnUnaIPPrivadaAutorizada(t *testing.T) {
	ip := ipPrivadaLocal(t)
	t.Logf("IP privada usada como gateway de prueba: %s", ip)
	f := falsos.NuevoRunPod(t, claveRunPod)
	ssh := falsos.NuevoSSH(t)
	ollama, stt, voz := modelosFalsos(t)
	env := entornoCompleto(t, f.URL)
	env["MODELOS_SSH_BIN"], env["MODELOS_SCP_BIN"] = ssh.BinSSH, ssh.BinSCP
	env["MODELOS_INTERNO_OLLAMA"] = strings.TrimPrefix(ollama.URL, "http://")
	env["MODELOS_INTERNO_STT"] = strings.TrimPrefix(stt.URL, "http://")
	env["MODELOS_INTERNO_VOZ"] = strings.TrimPrefix(voz.URL, "http://")
	puertos := map[string]string{}
	for _, v := range []string{"MODELOS_ESCUCHA_VOZ", "MODELOS_ESCUCHA_STT", "MODELOS_ESCUCHA_OLLAMA"} {
		_, p, _ := net.SplitHostPort(env[v])
		env[v] = net.JoinHostPort(ip, p)
		puertos[v] = p
	}
	urlVoz, urlSTT := "http://"+env["MODELOS_ESCUCHA_VOZ"], "http://"+env["MODELOS_ESCUCHA_STT"]

	ctx, parar := context.WithCancel(t.Context())
	terminado := make(chan int, 1)
	var errores strings.Builder
	go func() { terminado <- ejecutar(ctx, []string{"servir"}, lookup(env), io.Discard, &errores) }()
	defer func() { parar(); <-terminado }()
	esperarEstado(t, env["MODELOS_PUERTO_CONTROL"], "apagado")

	// Sin autorización la IP privada no se acepta: es lo que protege a la API de un destino cambiado.
	t.Setenv(destinolocal.VariableHostPermitido, "")
	if _, err := vozlocal.Nueva(urlVoz, tokenVoz); !errors.Is(err, copilot.ErrSolicitudIA) {
		t.Fatalf("sin VERA_MODELOS_HOST_PERMITIDO vozlocal debía rechazar la IP privada: %v", err)
	}
	if _, err := sttlocal.Nuevo(urlSTT, tokenSTT, 5*time.Second); !errors.Is(err, copilot.ErrSolicitudIA) {
		t.Fatalf("sin VERA_MODELOS_HOST_PERMITIDO sttlocal debía rechazar la IP privada: %v", err)
	}
	// Con la autorización exacta de esa IP, sí.
	t.Setenv(destinolocal.VariableHostPermitido, ip)
	clienteVoz, err := vozlocal.Nueva(urlVoz, tokenVoz)
	if err != nil {
		t.Fatalf("vozlocal rechazó la IP autorizada: %v", err)
	}
	clienteSTT, err := sttlocal.Nuevo(urlSTT, tokenSTT, 5*time.Second)
	if err != nil {
		t.Fatalf("sttlocal rechazó la IP autorizada: %v", err)
	}

	// Pod apagado: el proxy responde 503 modelos_apagados y los clientes lo ven como no disponible.
	for _, v := range []string{"MODELOS_ESCUCHA_VOZ", "MODELOS_ESCUCHA_STT", "MODELOS_ESCUCHA_OLLAMA"} {
		raw503(t, "http://"+env[v]+"/healthz")
	}
	if _, err := clienteVoz.Sintetizar(t.Context(), "hola", ""); !errors.Is(err, copilot.ErrNoDisponibleIA) {
		t.Fatalf("voz con el pod apagado: %v", err)
	}
	audio := copilot.AudioIA{MIME: "audio/wav", Datos: wavMinimo()}
	if _, err := clienteSTT.Transcribir(t.Context(), audio); !errors.Is(err, copilot.ErrNoDisponibleIA) {
		t.Fatalf("STT con el pod apagado: %v", err)
	}

	// Pod encendido (falso): encender, esperar listo y comprobar que los clientes llegan a los modelos.
	if codigo, cuerpo := llamarControl(t, env["MODELOS_PUERTO_CONTROL"], http.MethodPost, "/encender"); codigo != http.StatusAccepted && codigo != http.StatusOK {
		t.Fatalf("encender: %d %s", codigo, cuerpo)
	}
	esperarEstado(t, env["MODELOS_PUERTO_CONTROL"], "listo")
	if _, err := clienteVoz.Sintetizar(t.Context(), "hola", ""); err != nil {
		t.Fatalf("voz con el pod listo: %v", err)
	}
	if texto, err := clienteSTT.Transcribir(t.Context(), audio); err != nil || texto != "hola vera" {
		t.Fatalf("STT con el pod listo: %q %v", texto, err)
	}
	if res, err := http.Get("http://" + env["MODELOS_ESCUCHA_OLLAMA"] + "/api/version"); err != nil || res.StatusCode != http.StatusOK { //nolint:noctx // prueba local
		t.Fatalf("Ollama con el pod listo: %v", err)
	} else {
		_ = res.Body.Close()
	}

	// Apagar: vuelve el 503 y el pod falso ya no existe.
	if codigo, cuerpo := llamarControl(t, env["MODELOS_PUERTO_CONTROL"], http.MethodPost, "/apagar"); codigo != http.StatusAccepted && codigo != http.StatusOK {
		t.Fatalf("apagar: %d %s", codigo, cuerpo)
	}
	esperarEstado(t, env["MODELOS_PUERTO_CONTROL"], "apagado")
	raw503(t, urlVoz+"/healthz")
	if f.Vivos("vera-modelos") != 0 {
		t.Fatal("el pod falso sigue vivo tras apagar")
	}
	if strings.Contains(errores.String(), claveRunPod) || strings.Contains(errores.String(), tokenVoz) {
		t.Fatalf("el registro contiene secretos:\n%s", errores.String())
	}
}
