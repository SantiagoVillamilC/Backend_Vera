package httpserver

import (
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModelosSinConfigYEstados(t *testing.T) {
	for _, codigo := range []int{200, 401, 404, 500} {
		t.Run(http.StatusText(codigo), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(codigo) }))
			defer srv.Close()
			h := NewHealthResource(nil, ConModelos(DependenciasDeModelos{Voz: srv.URL}))
			w := httptest.NewRecorder()
			h.Modelos(w, httptest.NewRequestWithContext(t.Context(), "GET", "/readyz/modelos", nil))
			esperado := 200
			if codigo >= 500 {
				esperado = 503
			}
			if w.Code != esperado || !strings.Contains(w.Body.String(), "no configurada") || strings.Contains(w.Body.String(), srv.URL) {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
	w := httptest.NewRecorder()
	NewHealthResource(nil).Modelos(w, httptest.NewRequestWithContext(t.Context(), "GET", "/readyz/modelos", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestModelosRutaPublicaSinRateLimitNiPostgres(t *testing.T) {
	router := NewRouterConVera(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{}, DependenciasVera{})
	for range 105 {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/readyz/modelos", nil))
		if w.Code != 200 {
			t.Fatalf("sonda sin credencial: %d %s", w.Code, w.Body)
		}
	}
}

func TestModelosPresupuestoParalelo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	h := NewHealthResource(nil, ConModelos(DependenciasDeModelos{Voz: srv.URL, STT: srv.URL, Ollama: srv.URL}))
	w := httptest.NewRecorder()
	inicio := time.Now()
	h.Modelos(w, httptest.NewRequestWithContext(t.Context(), "GET", "/readyz/modelos", nil))
	if w.Code != 503 || time.Since(inicio) > 3500*time.Millisecond {
		t.Fatal("los presupuestos se acumulan", w.Code, time.Since(inicio))
	}
}
