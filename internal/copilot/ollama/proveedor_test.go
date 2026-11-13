package ollama

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestNuevoValidaConfiguracion(t *testing.T) {
	for _, caso := range []struct {
		nombre string
		cfg    Config
	}{
		{"urlVacia", Config{Modelo: "m", Timeout: time.Second, Temperatura: 0.2}},
		{"urlSinEsquema", Config{URL: "127.0.0.1:11434", Modelo: "m", Timeout: time.Second, Temperatura: 0.2}},
		{"urlFtp", Config{URL: "ftp://127.0.0.1:11434", Modelo: "m", Timeout: time.Second, Temperatura: 0.2}},
		{"urlConRuta", Config{URL: "http://127.0.0.1:11434/ollama", Modelo: "m", Timeout: time.Second, Temperatura: 0.2}},
		{"urlConCredenciales", Config{URL: "http://usuario:clave@127.0.0.1:11434", Modelo: "m", Timeout: time.Second, Temperatura: 0.2}},
		{"urlConConsulta", Config{URL: "http://127.0.0.1:11434?x=1", Modelo: "m", Timeout: time.Second, Temperatura: 0.2}},
		{"sinModelo", Config{URL: "http://127.0.0.1:11434", Modelo: " ", Timeout: time.Second, Temperatura: 0.2}},
		{"sinPlazo", Config{URL: "http://127.0.0.1:11434", Modelo: "m", Temperatura: 0.2}},
		{"temperaturaAlta", Config{URL: "http://127.0.0.1:11434", Modelo: "m", Timeout: time.Second, Temperatura: 2}},
		{"temperaturaNegativa", Config{URL: "http://127.0.0.1:11434", Modelo: "m", Timeout: time.Second, Temperatura: -1}},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			if _, err := Nuevo(caso.cfg, nil); !errors.Is(err, copilot.ErrSolicitudIA) {
				t.Fatalf("se esperaba ErrSolicitudIA, llegó %v", err)
			}
		})
	}
	p, err := Nuevo(Config{URL: "http://127.0.0.1:11434/", Modelo: "gemma4-e4b-local", Timeout: time.Second, Temperatura: 0.2}, nil)
	if err != nil || p.origen != "http://127.0.0.1:11434" {
		t.Fatalf("origen normalizado: %v %v", p, err)
	}
}

func TestErroresPorCategoria(t *testing.T) {
	for _, caso := range []struct {
		status     int
		err        error
		intentos   int32
		acumulaSin bool
	}{
		{http.StatusBadRequest, copilot.ErrSolicitudIA, 1, false},
		{http.StatusNotFound, copilot.ErrSolicitudIA, 1, false},
		{http.StatusUnauthorized, copilot.ErrCredencialesIA, 1, false},
		{http.StatusForbidden, copilot.ErrCredencialesIA, 1, false},
		{http.StatusTooManyRequests, copilot.ErrLimiteIA, 1, false},
		{http.StatusRequestTimeout, copilot.ErrTimeoutIA, 3, true},
		{http.StatusInternalServerError, copilot.ErrNoDisponibleIA, 3, false},
		{http.StatusServiceUnavailable, copilot.ErrNoDisponibleIA, 3, false},
	} {
		t.Run(http.StatusText(caso.status), func(t *testing.T) {
			var intentos int32
			p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&intentos, 1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(caso.status)
				_, _ = w.Write([]byte(`{"error":"detalle interno del servidor"}`))
			})
			_, err := p.Generar(t.Context(), solicitudTexto())
			if !errors.Is(err, caso.err) {
				t.Fatalf("se esperaba %v, llegó %v", caso.err, err)
			}
			if caso.acumulaSin && !errors.Is(err, copilot.ErrNoDisponibleIA) {
				t.Fatalf("al agotar intentos debía acumular ErrNoDisponibleIA: %v", err)
			}
			if atomic.LoadInt32(&intentos) != caso.intentos {
				t.Fatalf("intentos: %d", atomic.LoadInt32(&intentos))
			}
			if strings.Contains(err.Error(), "detalle interno") {
				t.Fatalf("el error filtró el cuerpo del servidor: %v", err)
			}
		})
	}
}

func TestRespuestaNoJsonYDemasiadoGrande(t *testing.T) {
	for _, caso := range []struct {
		nombre string
		tipo   string
		cuerpo string
	}{
		{"tipoTextoPlano", "text/plain", respuestaTexto},
		{"cuerpoGigante", "application/json", `{"message":{"role":"assistant","content":"Hola"},"done":true,"done_reason":"stop","relleno":"` + strings.Repeat("a", limiteRespuesta) + `"}`},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			var intentos int32
			p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&intentos, 1)
				w.Header().Set("Content-Type", caso.tipo)
				_, _ = w.Write([]byte(caso.cuerpo))
			})
			if _, err := p.Generar(t.Context(), solicitudTexto()); !errors.Is(err, copilot.ErrRespuestaIA) {
				t.Fatalf("se esperaba ErrRespuestaIA, llegó %v", err)
			}
			if atomic.LoadInt32(&intentos) != 1 {
				t.Fatalf("una respuesta ilegible no se reintenta: %d", atomic.LoadInt32(&intentos))
			}
		})
	}
}

func TestReintentosConEsperaCreciente(t *testing.T) {
	var intentos int32
	esperas := make(chan time.Duration, 4)
	p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&intentos, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respuestaTexto))
	})
	p.esperar = func(_ context.Context, d time.Duration) error {
		esperas <- d
		return nil
	}
	res, err := p.Generar(t.Context(), solicitudTexto())
	if err != nil || res.TokensSalida != 4 || atomic.LoadInt32(&intentos) != 3 {
		t.Fatalf("recuperación al tercer intento: %v %d %+v", err, atomic.LoadInt32(&intentos), res)
	}
	primera, segunda := <-esperas, <-esperas
	if primera < 200*time.Millisecond || primera >= 300*time.Millisecond {
		t.Fatalf("primera espera: %v", primera)
	}
	if segunda < 400*time.Millisecond || segunda >= 600*time.Millisecond {
		t.Fatalf("segunda espera: %v", segunda)
	}
}

func TestServidorApagado(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	p, err := Nuevo(Config{URL: url, Modelo: "gemma4-e4b-local", Timeout: time.Second, Temperatura: 0.2}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	p.esperar = func(context.Context, time.Duration) error { return nil }
	if _, err := p.Generar(t.Context(), solicitudTexto()); !errors.Is(err, copilot.ErrNoDisponibleIA) {
		t.Fatalf("servidor apagado: %v", err)
	}
}

func TestTimeoutPorIntento(t *testing.T) {
	liberar := make(chan struct{})
	var intentos int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&intentos, 1)
		select {
		case <-r.Context().Done():
		case <-liberar:
		}
	}))
	defer srv.Close()
	defer close(liberar)
	p, err := Nuevo(Config{URL: srv.URL, Modelo: "gemma4-e4b-local", Timeout: 20 * time.Millisecond, Temperatura: 0.2}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	p.esperar = func(context.Context, time.Duration) error { return nil }
	_, err = p.Generar(t.Context(), solicitudTexto())
	if !errors.Is(err, copilot.ErrTimeoutIA) || !errors.Is(err, copilot.ErrNoDisponibleIA) {
		t.Fatalf("timeout por intento: %v", err)
	}
	if atomic.LoadInt32(&intentos) != 3 {
		t.Fatalf("intentos tras vencer el plazo: %d", atomic.LoadInt32(&intentos))
	}
}

func TestCancelacion(t *testing.T) {
	t.Run("antes", func(t *testing.T) {
		var intentos int32
		p := proveedorPrueba(t, func(http.ResponseWriter, *http.Request) { atomic.AddInt32(&intentos, 1) })
		ctx, cancelar := context.WithCancel(t.Context())
		cancelar()
		if _, err := p.Generar(ctx, solicitudTexto()); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelación previa: %v", err)
		}
		if atomic.LoadInt32(&intentos) != 0 {
			t.Fatalf("no debía llamarse a la red: %d", atomic.LoadInt32(&intentos))
		}
	})
	t.Run("durante", func(t *testing.T) {
		iniciado := make(chan struct{}, 1)
		liberar := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case iniciado <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-liberar:
			}
		}))
		defer srv.Close()
		defer close(liberar)
		p, err := Nuevo(Config{URL: srv.URL, Modelo: "gemma4-e4b-local", Timeout: 5 * time.Second, Temperatura: 0.2}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
		if err != nil {
			t.Fatal(err)
		}
		p.esperar = func(context.Context, time.Duration) error { return nil }
		ctx, cancelar := context.WithCancel(t.Context())
		go func() {
			<-iniciado
			cancelar()
		}()
		if _, err := p.Generar(ctx, solicitudTexto()); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelación en curso: %v", err)
		}
	})
}

func TestRegistroSinContenido(t *testing.T) {
	var registro bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respuestaTexto))
	}))
	defer srv.Close()
	p, err := Nuevo(Config{URL: srv.URL, Modelo: "gemma4-e4b-local", Timeout: time.Second, Temperatura: 0.2}, slog.New(slog.NewJSONHandler(&registro, nil)))
	if err != nil {
		t.Fatal(err)
	}
	p.esperar = func(context.Context, time.Duration) error { return nil }
	if _, err := p.Generar(t.Context(), solicitudTexto()); err != nil {
		t.Fatal(err)
	}
	lineas := registro.String()
	for _, privado := range []string{"texto privado", "instrucciones privadas", "Hola"} {
		if strings.Contains(lineas, privado) {
			t.Fatalf("el registro filtró contenido: %s", lineas)
		}
	}
	if !strings.Contains(lineas, `"modelo":"gemma4-e4b-local"`) || !strings.Contains(lineas, `"resultado":"ok"`) || !strings.Contains(lineas, `"tokens_salida":4`) {
		t.Fatalf("el registro no describe el intento: %s", lineas)
	}
}
