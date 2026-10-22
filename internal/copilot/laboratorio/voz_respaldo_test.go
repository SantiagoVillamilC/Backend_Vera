package laboratorio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

type vozConFallo struct {
	llamadas int
	fallo    error
	parcial  bool
}

func (v *vozConFallo) Sintetizar(context.Context, string, string) ([]byte, error) {
	v.llamadas++
	return nil, v.fallo
}
func (v *vozConFallo) SintetizarFlujo(_ context.Context, _, _ string, recibir func([]byte) error) ([]byte, error) {
	v.llamadas++
	if v.parcial {
		if err := recibir([]byte{0, 0}); err != nil {
			return nil, err
		}
	}
	return nil, v.fallo
}

func peticionVoz(t *testing.T, h http.Handler, ruta, texto string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8787"+ruta, strings.NewReader(texto))
	r.Header.Set("X-Vera-Token", "token-prueba")
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestVeinteRespuestasConCuotaGeminiAgotadaMantienenVozNeural(t *testing.T) {
	google := &vozConFallo{fallo: &copilot.ErrorLimiteIA{Clase: copilot.LimiteDesconocido, ReintentarEn: 2 * time.Hour}}
	local := &vozPrueba{}
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 60, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", false, google, ConVozLocal(local))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		if w := peticionVoz(t, h, "/api/preguntar", "¿Cuánto tengo?"); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		ruta := "/api/voz"
		if i%2 == 1 {
			ruta += "/flujo"
		}
		w := peticionVoz(t, h, ruta, "Aoede")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"fuente":"kokoro"`) || !strings.Contains(w.Body.String(), `"motivo_respaldo":"GeminiQuota"`) {
			t.Fatalf("turno %d: %s", i+1, w.Body.String())
		}
	}
	if google.llamadas != 1 || local.llamadas != 20 {
		t.Fatalf("Google=%d local=%d", google.llamadas, local.llamadas)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost:8787/api/estado", nil)
	r.Header.Set("X-Vera-Token", "token-prueba")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var sobre api.Envelope[estado]
	if json.Unmarshal(w.Body.Bytes(), &sobre) != nil || sobre.Data.Voces.Local != 20 || sobre.Data.Voces.Fallos != 0 || sobre.Data.Usadas != 41 {
		t.Fatal(w.Body.String())
	}
}

func TestVozLocalNoRepiteTrasAudioParcialBloqueoOCancelacion(t *testing.T) {
	for nombre, tc := range map[string]struct {
		err     error
		parcial bool
	}{
		"parcial": {copilot.ErrNoDisponibleIA, true}, "contenido": {copilot.ErrBloqueoIA, false}, "cancelacion": {context.Canceled, false},
	} {
		t.Run(nombre, func(t *testing.T) {
			local := &vozPrueba{}
			s := &servidor{voz: &vozConFallo{fallo: tc.err, parcial: tc.parcial}, vozLocal: local, ultimoTexto: "Hola"}
			s.consola, _ = evaluacion.NuevaConsola(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 5, NivelGratuitoConfirmado: true}, ProveedorDemo{}, nil, time.Now, evaluacion.ConRecuperacion())
			_, _, _, err := s.generarVoz(t.Context(), "Aoede", func([]byte) error { return nil })
			if !errors.Is(err, tc.err) || local.llamadas != 0 {
				t.Fatalf("err=%v llamadas=%d", err, local.llamadas)
			}
		})
	}
}

func TestDoraDirectaYRepeticionNoConsumenCuotaGoogle(t *testing.T) {
	google := &vozConFallo{fallo: copilot.ErrNoDisponibleIA}
	local := &vozPrueba{}
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 2, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", false, google, ConVozLocal(local))
	if err != nil {
		t.Fatal(err)
	}
	peticionVoz(t, h, "/api/preguntar", "saldo")
	for range 2 {
		w := peticionVoz(t, h, "/api/voz", "Dora")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"usadas":2`) {
			t.Fatal(w.Body.String())
		}
	}
	if local.llamadas != 1 || google.llamadas != 0 {
		t.Fatal("consumió cuota o repitió síntesis", local.llamadas, google.llamadas)
	}
}
