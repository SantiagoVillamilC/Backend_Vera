package laboratorio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

type vozFlujoPrueba struct {
	vozPrueba
	llamadasFlujo atomic.Int32
	continuar     <-chan struct{}
	falla         bool
}

func (v *vozFlujoPrueba) SintetizarFlujo(ctx context.Context, _, _ string, recibir func([]byte) error) ([]byte, error) {
	v.llamadasFlujo.Add(1)
	if err := recibir([]byte{0, 0, 1, 0}); err != nil {
		return nil, err
	}
	if v.falla {
		return nil, errors.New("fallo privado del proveedor")
	}
	if v.continuar != nil {
		select {
		case <-v.continuar:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return wavPrueba(), nil
}

func laboratorioConVozFlujo(t *testing.T, v *vozFlujoPrueba, maximo int) http.Handler {
	t.Helper()
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: maximo, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", false, v)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8787/api/preguntar", strings.NewReader("¿Cuánto tengo?"))
	r.Header.Set("X-Vera-Token", "token-prueba")
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	return h
}

func TestFlujoHTTPEntregaAntesDelFinYComparteCache(t *testing.T) {
	continuar := make(chan struct{})
	v := &vozFlujoPrueba{continuar: continuar}
	h := laboratorioConVozFlujo(t, v, 3)
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	peticion := func() *http.Response {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/voz/flujo", strings.NewReader("Aoede"))
		if err != nil {
			t.Fatal(err)
		}
		r.Host = "localhost:8787"
		r.Header.Set("X-Vera-Token", "token-prueba")
		r.Header.Set("Content-Type", "text/plain")
		res, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	r := peticion()
	defer func() { _ = r.Body.Close() }()
	d := json.NewDecoder(r.Body)
	var e api.Envelope[eventoVoz]
	if err := d.Decode(&e); err != nil || e.Data.Tipo != "audio" {
		close(continuar)
		t.Fatal("no entregó el primer paquete", err)
	}
	close(continuar)
	if err := d.Decode(&e); err != nil || e.Data.Tipo != "fin" || e.Data.Estado.Usadas != 3 {
		t.Fatal("no completó la voz", err)
	}
	r2 := peticion()
	defer func() { _ = r2.Body.Close() }()
	d = json.NewDecoder(r2.Body)
	if err := d.Decode(&e); err != nil || e.Data.Tipo != "wav" {
		t.Fatal("no reutilizó la caché", err)
	}
	if err := d.Decode(&e); err != nil || e.Data.Tipo != "fin" || e.Data.Estado.Usadas != 3 || v.llamadasFlujo.Load() != 1 {
		t.Fatal("consumió cuota al repetir", err)
	}
}

func TestFlujoInterrumpidoNoSeGuardaEnCache(t *testing.T) {
	v := &vozFlujoPrueba{falla: true}
	h := laboratorioConVozFlujo(t, v, 4)
	for range 2 {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8787/api/voz/flujo", strings.NewReader("Aoede"))
		r.Header.Set("X-Vera-Token", "token-prueba")
		r.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if !strings.Contains(w.Body.String(), "VoiceInterrupted") || strings.Contains(w.Body.String(), "privado") || strings.Contains(w.Body.String(), `"tipo":"fin"`) {
			t.Fatal(w.Body.String())
		}
	}
	if v.llamadasFlujo.Load() != 2 {
		t.Fatal("se reutilizó audio incompleto")
	}
}
