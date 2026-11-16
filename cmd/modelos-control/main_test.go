package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/falsos"
)

const (
	claveRunPod  = "rpa_CLAVE_DE_PRUEBA_0123456789"
	tokenControl = "token-de-control-de-prueba-0123456789abcdef"
	tokenSTT     = "token-stt-de-prueba-0123456789abcdefgh"
	tokenVoz     = "token-voz-de-prueba-0123456789abcdefgh"
)

func puertoLibre(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func escribirSecreto(t *testing.T, dir, nombre, contenido string) string {
	t.Helper()
	ruta := filepath.Join(dir, nombre)
	if err := os.WriteFile(ruta, []byte(contenido), 0o600); err != nil {
		t.Fatal(err)
	}
	return ruta
}

// entornoCompleto prepara archivos de secretos, directorio de estado y direcciones libres.
func entornoCompleto(t *testing.T, urlRunPod string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	return map[string]string{
		"MODELOS_RUNPOD_URL":            urlRunPod + "/v2",
		"MODELOS_DIR_ESTADO":            filepath.Join(dir, "estado"),
		"MODELOS_ARCHIVO_CLAVE_RUNPOD":  escribirSecreto(t, dir, "runpod.env", "RUNPOD_API_KEY="+claveRunPod+"\n"),
		"MODELOS_ARCHIVO_TOKEN_CONTROL": escribirSecreto(t, dir, "control.token", tokenControl+"\n"),
		"MODELOS_ARCHIVO_TOKENS":        escribirSecreto(t, dir, "modelos.env", "VERA_STT_TOKEN="+tokenSTT+"\nVERA_VOZ_TOKEN="+tokenVoz+"\n"),
		"MODELOS_LLAVE_SSH":             filepath.Join(dir, "llave"),
		"MODELOS_LLAVE_PUBLICA_SSH":     escribirSecreto(t, dir, "llave.pub", "ssh-ed25519 AAAATESTKEY vm\n"),
		"MODELOS_SCRIPT_ARRANQUE":       escribirSecreto(t, dir, "arrancar.sh", "#!/bin/sh\nexit 0\n"),
		"MODELOS_PUERTO_CONTROL":        puertoLibre(t),
		"MODELOS_ESCUCHA_VOZ":           puertoLibre(t),
		"MODELOS_ESCUCHA_STT":           puertoLibre(t),
		"MODELOS_ESCUCHA_OLLAMA":        puertoLibre(t),
		"MODELOS_INTERNO_VOZ":           puertoLibre(t),
		"MODELOS_INTERNO_STT":           puertoLibre(t),
		"MODELOS_INTERNO_OLLAMA":        puertoLibre(t),
		"MODELOS_VIGILANCIA":            "50ms",
	}
}

func lookup(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestSecoImprimeLaConfiguracionSinRedYSinSecretos(t *testing.T) {
	var salida, errores bytes.Buffer
	env := map[string]string{"MODELOS_ARCHIVO_CLAVE_RUNPOD": "/ruta/inexistente/runpod.env", "RUNPOD_API_KEY": claveRunPod}
	codigo := ejecutar(t.Context(), []string{"--seco"}, lookup(env), &salida, &errores)
	if codigo != 0 {
		t.Fatalf("codigo %d: %s", codigo, errores.String())
	}
	var m map[string]any
	if err := json.Unmarshal(salida.Bytes(), &m); err != nil {
		t.Fatalf("salida no es json: %v\n%s", err, salida.String())
	}
	if strings.Contains(salida.String()+errores.String(), claveRunPod) || !strings.Contains(salida.String(), "/ruta/inexistente/runpod.env") {
		t.Fatalf("salida %s", salida.String())
	}
}

func TestConfiguracionInvalidaSale2SinSecretos(t *testing.T) {
	var salida, errores bytes.Buffer
	codigo := ejecutar(t.Context(), []string{"servir"}, lookup(map[string]string{"MODELOS_PUERTO_CONTROL": "0.0.0.0:80", "RUNPOD_API_KEY": claveRunPod}), &salida, &errores)
	if codigo != 2 || !strings.Contains(errores.String(), "MODELOS_PUERTO_CONTROL") || strings.Contains(errores.String(), claveRunPod) {
		t.Fatalf("codigo=%d err=%s", codigo, errores.String())
	}
}

func TestSubcomandoDesconocido(t *testing.T) {
	var salida, errores bytes.Buffer
	if codigo := ejecutar(t.Context(), []string{"volar"}, lookup(nil), &salida, &errores); codigo != 64 {
		t.Fatalf("codigo %d", codigo)
	}
}

func TestReconcileTerminaHuerfanosYSaleConCero(t *testing.T) {
	f := falsos.NuevoRunPod(t, claveRunPod)
	id := f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	ajeno := f.AnadirPod("tesis", "RUNNING", time.Now())
	env := entornoCompleto(t, f.URL)
	var salida, errores bytes.Buffer
	codigo := ejecutar(t.Context(), []string{"reconcile"}, lookup(env), &salida, &errores)
	if codigo != 0 || f.Existe(id) || !f.Existe(ajeno) {
		t.Fatalf("codigo=%d existe=%v ajeno=%v err=%s", codigo, f.Existe(id), f.Existe(ajeno), errores.String())
	}
	if !strings.Contains(salida.String(), id) || strings.Contains(salida.String()+errores.String(), claveRunPod) {
		t.Fatalf("salida %s", salida.String())
	}
}

func TestReconcileConDeleteFallandoSaleConUno(t *testing.T) {
	f := falsos.NuevoRunPod(t, claveRunPod)
	f.AnadirPod("vera-modelos", "RUNNING", time.Now())
	f.FallosDelete = 1000
	env := entornoCompleto(t, f.URL)
	var salida, errores bytes.Buffer
	instantaneo := opciones{dormir: func(context.Context, time.Duration) error { return nil }}
	if codigo := ejecutarCon(t.Context(), []string{"reconcile"}, lookup(env), &salida, &errores, instantaneo); codigo != 1 {
		t.Fatalf("codigo %d err=%s", codigo, errores.String())
	}
}

func TestServirExponeControlYProxyApagadoYSeCierraConLaSenal(t *testing.T) {
	f := falsos.NuevoRunPod(t, claveRunPod)
	env := entornoCompleto(t, f.URL)
	ctx, parar := context.WithCancel(t.Context())
	terminado := make(chan int, 1)
	var errores bytes.Buffer
	go func() { terminado <- ejecutar(ctx, []string{"servir"}, lookup(env), io.Discard, &errores) }()
	estado := func(token string) (int, string) {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+env["MODELOS_PUERTO_CONTROL"]+"/estado", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer func() { _ = res.Body.Close() }()
		cuerpo, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(cuerpo)
	}
	limite := time.Now().Add(5 * time.Second)
	for {
		if codigo, _ := estado(tokenControl); codigo == 200 {
			break
		}
		if time.Now().After(limite) {
			t.Fatalf("la API de control no arranco: %s", errores.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if codigo, cuerpo := estado(tokenControl); codigo != 200 || !strings.Contains(cuerpo, `"estado":"apagado"`) {
		t.Fatalf("codigo=%d cuerpo=%s", codigo, cuerpo)
	}
	if codigo, _ := estado("incorrecto"); codigo != 401 {
		t.Fatalf("codigo %d", codigo)
	}
	for _, puerto := range []string{env["MODELOS_ESCUCHA_VOZ"], env["MODELOS_ESCUCHA_STT"], env["MODELOS_ESCUCHA_OLLAMA"]} {
		pet, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+puerto+"/api/chat", strings.NewReader("{}"))
		res, err := http.DefaultClient.Do(pet)
		if err != nil {
			t.Fatal(err)
		}
		cuerpo, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != 503 || strings.TrimSpace(string(cuerpo)) != `{"error":"modelos_apagados"}` || res.Header.Get("Retry-After") == "" {
			t.Fatalf("puerto %s: status=%d cuerpo=%s", puerto, res.StatusCode, cuerpo)
		}
	}
	parar()
	select {
	case codigo := <-terminado:
		if codigo != 0 {
			t.Fatalf("codigo %d: %s", codigo, errores.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no se cerro con la senal")
	}
	if strings.Contains(errores.String(), claveRunPod) || strings.Contains(errores.String(), tokenControl) {
		t.Fatalf("el registro contiene secretos:\n%s", errores.String())
	}
}

func TestServirFallaSiOtraInstanciaTieneElBloqueo(t *testing.T) {
	f := falsos.NuevoRunPod(t, claveRunPod)
	env := entornoCompleto(t, f.URL)
	ctx, parar := context.WithCancel(t.Context())
	defer parar()
	primero := make(chan int, 1)
	go func() { primero <- ejecutar(ctx, []string{"servir"}, lookup(env), io.Discard, io.Discard) }()
	limite := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(env["MODELOS_DIR_ESTADO"], "servicio.lock")); err == nil {
			break
		}
		if time.Now().After(limite) {
			t.Fatal("el primero no arranco")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var errores bytes.Buffer
	env2 := map[string]string{}
	for k, v := range env {
		env2[k] = v
	}
	for _, k := range []string{"MODELOS_PUERTO_CONTROL", "MODELOS_ESCUCHA_VOZ", "MODELOS_ESCUCHA_STT", "MODELOS_ESCUCHA_OLLAMA"} {
		env2[k] = puertoLibre(t)
	}
	if codigo := ejecutar(ctx, []string{"servir"}, lookup(env2), io.Discard, &errores); codigo != 1 || !strings.Contains(errores.String(), "otra instancia") {
		t.Fatalf("codigo=%d err=%s", codigo, errores.String())
	}
}
