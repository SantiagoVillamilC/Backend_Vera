package control

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/acceso"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/falsos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/proxy"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/runpod"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

const (
	claveRunPod = "rpa_CLAVE_DE_PRUEBA_0123456789"
	tokenSTT    = "token-stt-de-prueba-0123456789abcdefgh"
	tokenVoz    = "token-voz-de-prueba-0123456789abcdefgh"
)

// relojFalso avanza solo cuando alguien duerme o lo adelanta: así los plazos largos se agotan rápido.
type relojFalso struct {
	mu sync.Mutex
	t  time.Time
}

func (r *relojFalso) Ahora() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.t
}

func (r *relojFalso) Avanzar(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.t = r.t.Add(d)
}

type bufferSeguro struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *bufferSeguro) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *bufferSeguro) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type arnes struct {
	t        *testing.T
	svc      *Servicio
	nube     *falsos.RunPod
	ssh      *falsos.SSH
	reloj    *relojFalso
	almacen  estado.Almacen
	cfg      config.Config
	act      *proxy.Actividad
	registro *bufferSeguro
	deps     Dependencias
	modelos  [3]*httptest.Server // ollama, stt, voz
}

func backendsDeModelos(t *testing.T) [3]*httptest.Server {
	t.Helper()
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			_, _ = w.Write([]byte(`{"version":"0.0.0"}`))
			return
		}
		w.WriteHeader(404)
	}))
	conToken := func(token string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" || r.Header.Get("X-Vera-Token") != token {
				w.WriteHeader(401)
				return
			}
			_, _ = w.Write([]byte("ok"))
		}))
	}
	salida := [3]*httptest.Server{ollama, conToken(tokenSTT), conToken(tokenVoz)}
	for _, s := range salida {
		t.Cleanup(s.Close)
	}
	return salida
}

func nuevoArnes(t *testing.T, ajustar ...func(*config.Config)) *arnes {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "arrancar-modelos.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &arnes{t: t, reloj: &relojFalso{t: time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)}, registro: &bufferSeguro{}}
	a.modelos = backendsDeModelos(t)
	host := func(s *httptest.Server) string { return strings.TrimPrefix(s.URL, "http://") }
	a.cfg = config.Config{NombrePod: "vera-modelos", GPUs: []string{"NVIDIA RTX A5000", "NVIDIA RTX A4000"}, Imagen: "runpod/pytorch:1.0.2-cu1281-torch280-ubuntu2404",
		DiscoGB: 40, TarifaTopeUSDH: 0.35, Inactividad: 10 * time.Minute, SesionMaxima: 4 * time.Hour, EsperaPod: 5 * time.Minute,
		EsperaSSH: 5 * time.Minute, EsperaArranque: 5 * time.Minute, EsperaModelos: 5 * time.Minute, Vigilancia: time.Second,
		DirEstado: filepath.Join(dir, "estado"), InternoOllama: host(a.modelos[0]), InternoSTT: host(a.modelos[1]), InternoVoz: host(a.modelos[2]),
		ScriptArranque: script, ScriptRemoto: "/tmp/vera-arranque.sh", RepoSHA: "abc123", LlaveSSH: "/ruta/llave"}
	for _, f := range ajustar {
		f(&a.cfg)
	}
	a.nube = falsos.NuevoRunPod(t, claveRunPod)
	a.ssh = falsos.NuevoSSH(t)
	var err error
	if a.almacen, err = estado.NuevoAlmacen(a.cfg.DirEstado); err != nil {
		t.Fatal(err)
	}
	a.act = proxy.NuevaActividad(a.reloj.Ahora)
	log := slog.New(slog.NewTextHandler(io.MultiWriter(a.registro), &slog.HandlerOptions{Level: slog.LevelDebug}))
	cliente, err := runpod.Nuevo(runpod.Config{BaseURL: a.nube.URL + "/v2", Clave: claveRunPod})
	if err != nil {
		t.Fatal(err)
	}
	dormir := func(ctx context.Context, d time.Duration) error {
		a.reloj.Avanzar(d)
		time.Sleep(time.Millisecond)
		return ctx.Err()
	}
	red := secreto.Nuevo(claveRunPod, tokenSTT, tokenVoz)
	a.deps = Dependencias{Cfg: a.cfg, Nube: cliente, Almacen: a.almacen, Actividad: a.act, Reloj: a.reloj.Ahora, Dormir: dormir, Log: log,
		LlavePublica: "ssh-ed25519 AAAATESTKEY vm", TokenSTT: tokenSTT, TokenVoz: tokenVoz, Redactor: red, EsperasTerminar: []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second},
		Acceso: acceso.Nuevo(acceso.Config{SSHBin: a.ssh.BinSSH, SCPBin: a.ssh.BinSCP, Llave: a.cfg.LlaveSSH, Huellas: a.almacen, Log: log, Redactor: red, EsperaReintentoTunel: 5 * time.Millisecond}),
		Sonda:  NuevaSonda(a.cfg, tokenSTT, tokenVoz), IntervaloSondeo: 10 * time.Second}
	a.svc, err = Nuevo(a.deps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.svc.Cerrar)
	return a
}

func (a *arnes) esperarFase(fase estado.Fase) Vista {
	a.t.Helper()
	limite := time.Now().Add(10 * time.Second)
	for {
		v := a.svc.Vista()
		// Con el pod listo el proceso ssh del tunel arranca en otra goroutine: se espera a que quede vivo.
		if v.Estado == string(fase) && (fase != estado.Listo || a.svc.Disponible()) {
			return v
		}
		if time.Now().After(limite) {
			a.t.Fatalf("la fase %s no llego, esta en %+v\nregistro:\n%s", fase, v, a.registro.String())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (a *arnes) encenderYEsperar() Vista {
	a.t.Helper()
	if _, _, err := a.svc.Encender(); err != nil {
		a.t.Fatal(err)
	}
	return a.esperarFase(estado.Listo)
}
