package acceso

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/falsos"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

const (
	claveRunPod = "rpa_CLAVE_DE_PRUEBA_0123456789"
	tokenVoz    = "token-de-voz-de-prueba-0123456789abcdef"
)

func nuevoSSH(t *testing.T) (*SSH, *falsos.SSH) {
	t.Helper()
	f := falsos.NuevoSSH(t)
	almacen, err := estado.NuevoAlmacen(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := Nuevo(Config{SSHBin: f.BinSSH, SCPBin: f.BinSCP, Llave: "/ruta/llave", Huellas: almacen,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Redactor: secreto.Nuevo(claveRunPod, tokenVoz),
		EsperaReintentoTunel: 10 * time.Millisecond})
	return s, f
}

var destino = Destino{PodID: "pod0001", Host: "203.0.113.7", Puerto: 40022, Usuario: "root"}

func TestComprobarUsaOpcionesSegurasYHuellasPorPod(t *testing.T) {
	s, f := nuevoSSH(t)
	if err := s.Comprobar(t.Context(), destino); err != nil {
		t.Fatal(err)
	}
	argv := f.Argv()
	for _, esperado := range []string{"-i /ruta/llave", "-p 40022", "IdentitiesOnly=yes", "BatchMode=yes", "StrictHostKeyChecking=accept-new",
		"known_hosts/pod0001", "-- root@203.0.113.7 true"} {
		if !strings.Contains(argv, esperado) {
			t.Errorf("falta %q en %s", esperado, argv)
		}
	}
	if strings.Contains(argv, "StrictHostKeyChecking=no") || strings.Contains(argv, "ssh-keygen") {
		t.Error("no se debe desactivar la verificacion de huellas")
	}
}

func TestHuellaDistintaEsErrorFatalSinReintento(t *testing.T) {
	s, f := nuevoSSH(t)
	f.Modo("huella")
	err := s.Comprobar(t.Context(), destino)
	if !errors.Is(err, ErrHuella) {
		t.Fatalf("err=%v", err)
	}
	if err := s.Ejecutar(t.Context(), destino, "bash -s", strings.NewReader("x")); !errors.Is(err, ErrHuella) {
		t.Fatalf("ejecutar err=%v", err)
	}
}

func TestSubirUsaScpConPuertoMayusculaYSeparador(t *testing.T) {
	s, f := nuevoSSH(t)
	if err := s.Subir(t.Context(), destino, "/local/arranque.sh", "/tmp/vera-arranque.sh"); err != nil {
		t.Fatal(err)
	}
	argv := f.Argv()
	if !strings.Contains(argv, "scp") || !strings.Contains(argv, "-P 40022") || !strings.Contains(argv, "-- /local/arranque.sh root@203.0.113.7:/tmp/vera-arranque.sh") {
		t.Fatalf("argv %s", argv)
	}
}

func TestEjecutarPasaSecretosPorStdinNuncaPorArgvYRedactaLaSalida(t *testing.T) {
	s, f := nuevoSSH(t)
	f.Modo("fallo_script")
	stdin := "export KOKORO_TOKEN='" + tokenVoz + "'\nexec bash /tmp/vera-arranque.sh\n"
	err := s.Ejecutar(t.Context(), destino, "bash -s", strings.NewReader(stdin))
	if err == nil || !strings.Contains(err.Error(), "7") {
		t.Fatalf("esperaba error con codigo 7, got %v", err)
	}
	if strings.Contains(err.Error(), claveRunPod) {
		t.Fatalf("el error filtra la clave: %v", err)
	}
	if strings.Contains(f.Argv(), tokenVoz) || f.Stdin() != stdin {
		t.Fatalf("argv=%q stdin=%q", f.Argv(), f.Stdin())
	}
}

func TestElEntornoDeSshNoLlevaLaClaveDeRunPod(t *testing.T) {
	t.Setenv("RUNPOD_API_KEY", claveRunPod)
	t.Setenv("MODELOS_RUNPOD_TOKEN_X", "algo-sensible-1234567890")
	s, f := nuevoSSH(t)
	if err := s.Comprobar(t.Context(), destino); err != nil {
		t.Fatal(err)
	}
	env := f.Env()
	if strings.Contains(env, "RUNPOD") || strings.Contains(env, claveRunPod) || strings.Contains(env, "MODELOS_") {
		t.Fatalf("el entorno del subproceso filtra variables: %s", env)
	}
}

func TestDestinoInvalidoNoLlegaAlProceso(t *testing.T) {
	s, f := nuevoSSH(t)
	for _, d := range []Destino{{PodID: "pod1", Host: "-oProxyCommand=x", Puerto: 22, Usuario: "root"},
		{PodID: "pod1", Host: "1.2.3.4", Puerto: 0, Usuario: "root"}, {PodID: "../x", Host: "1.2.3.4", Puerto: 22, Usuario: "root"}} {
		if err := s.Comprobar(t.Context(), d); err == nil {
			t.Fatalf("destino %+v debio rechazarse", d)
		}
	}
	if f.Argv() != "" {
		t.Fatal("no debio ejecutarse ssh")
	}
}

func esperar(t *testing.T, cond func() bool) {
	t.Helper()
	limite := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(limite) {
			t.Fatal("tiempo agotado esperando la condicion")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var reenvios = []Reenvio{{Local: "127.0.0.1:18434", Remoto: "127.0.0.1:11434"}, {Local: "127.0.0.1:18767", Remoto: "127.0.0.1:8767"}}

func TestTunelAbreConTresReenviosYSeCierra(t *testing.T) {
	s, f := nuevoSSH(t)
	tunel := s.AbrirTunel(t.Context(), destino, reenvios, func(error) {})
	esperar(t, tunel.Vivo)
	esperar(t, func() bool { return strings.Contains(f.Argv(), "18767") }) // el script falso registra sus argumentos al arrancar
	argv := f.Argv()
	for _, esperado := range []string{"-N", "ExitOnForwardFailure=yes", "ServerAliveInterval=15", "-L 127.0.0.1:18434:127.0.0.1:11434", "-L 127.0.0.1:18767:127.0.0.1:8767"} {
		if !strings.Contains(argv, esperado) {
			t.Errorf("falta %q en %s", esperado, argv)
		}
	}
	tunel.Cerrar()
	if tunel.Vivo() {
		t.Fatal("el tunel debe quedar cerrado")
	}
}

func TestTunelSeReabreSiMuere(t *testing.T) {
	s, f := nuevoSSH(t)
	f.Modo("tunel_muere:2")
	tunel := s.AbrirTunel(t.Context(), destino, reenvios, func(error) {})
	t.Cleanup(tunel.Cerrar)
	esperar(t, func() bool { return f.Tuneles() >= 3 && tunel.Vivo() })
}

func TestTunelConHuellaDistintaAvisaYNoReintenta(t *testing.T) {
	s, f := nuevoSSH(t)
	f.Modo("huella")
	var fatal atomic.Value
	tunel := s.AbrirTunel(context.Background(), destino, reenvios, func(err error) { fatal.Store(err) })
	t.Cleanup(tunel.Cerrar)
	esperar(t, func() bool { return fatal.Load() != nil })
	if err, _ := fatal.Load().(error); !errors.Is(err, ErrHuella) {
		t.Fatalf("err=%v", err)
	}
	time.Sleep(60 * time.Millisecond)
	if strings.Count(f.Argv(), "-N") != 1 || tunel.Vivo() {
		t.Fatalf("no debio reintentar: %s", f.Argv())
	}
}
