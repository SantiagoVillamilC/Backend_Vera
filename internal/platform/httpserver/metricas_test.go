package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

const tokenMetricasDePrueba = "token-de-metricas-de-prueba-0123456789"

// exposicionDePrueba imita el cuerpo de Prometheus sin arrastrar la biblioteca a este paquete.
func exposicionDePrueba() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte("# TYPE vera_inteligencia_lote_corridas_total counter\n" +
			"vera_inteligencia_lote_corridas_total{desenlace=\"ok\"} 1\n"))
	})
}

func routerConMetricas(t *testing.T, metricas MetricasDelRouter) http.Handler {
	t.Helper()
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba,
		DependenciasAuth{Identidad: auth.UsuarioDePrueba{}, Metricas: metricas})
}

func pedirMetricas(t *testing.T, router http.Handler, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestMetricasSinTokenDevuelve401 cierra la superficie cuando la credencial falta o no coincide.
func TestMetricasSinTokenDevuelve401(t *testing.T) {
	t.Parallel()
	router := routerConMetricas(t, MetricasDelRouter{
		Handler:      exposicionDePrueba(),
		Autenticador: autenticadorDePrueba{token: tokenMetricasDePrueba},
	})

	casos := map[string]string{
		"sin token":   "",
		"token ajeno": "otro-token-cualquiera",
	}
	for nombre, token := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			rec := pedirMetricas(t, router, token)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, quiero 401 (cuerpo %q)", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatalf("WWW-Authenticate = %q, quiero Bearer", rec.Header().Get("WWW-Authenticate"))
			}
			if strings.Contains(rec.Body.String(), "vera_inteligencia") {
				t.Fatalf("el 401 no debe exponer métricas: %q", rec.Body.String())
			}
			assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
		})
	}
}

// TestMetricasConTokenDevuelve200 sirve la exposición con el token correcto.
func TestMetricasConTokenDevuelve200(t *testing.T) {
	t.Parallel()
	router := routerConMetricas(t, MetricasDelRouter{
		Handler:      exposicionDePrueba(),
		Autenticador: autenticadorDePrueba{token: tokenMetricasDePrueba},
	})

	rec := pedirMetricas(t, router, tokenMetricasDePrueba)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, quiero text/plain", ct)
	}
	if !strings.Contains(rec.Body.String(), "vera_inteligencia_lote_corridas_total") {
		t.Fatalf("el cuerpo no trae la métrica: %q", rec.Body.String())
	}
}

// TestMetricasApagadasDevuelve404 mantiene el contrato del envelope cuando la bandera está apagada.
func TestMetricasApagadasDevuelve404(t *testing.T) {
	t.Parallel()
	router := routerConMetricas(t, MetricasDelRouter{})

	rec := pedirMetricas(t, router, tokenMetricasDePrueba)

	assertErrorEnvelope(t, rec, http.StatusNotFound, "NotFound")
}

// TestRouterPanicaSinAutenticadorDeMetricas falla cerrado en la construcción, no en la petición.
func TestRouterPanicaSinAutenticadorDeMetricas(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("NewRouter debía entrar en pánico con un handler de métricas sin autenticador")
		}
	}()
	routerConMetricas(t, MetricasDelRouter{Handler: exposicionDePrueba()})
}

// TestRequireMetricasSinAutenticadorPanica cubre el mismo contrato en el middleware suelto.
func TestRequireMetricasSinAutenticadorPanica(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("RequireMetricas debía entrar en pánico sin autenticador")
		}
	}()
	RequireMetricas(nil)
}

// TestTokensNoSeCruzan impide que una credencial abra dos superficies distintas.
func TestTokensNoSeCruzan(t *testing.T) {
	t.Parallel()
	router := routerConMetricas(t, MetricasDelRouter{
		Handler:      exposicionDePrueba(),
		Autenticador: autenticadorDePrueba{token: tokenMetricasDePrueba},
	})

	rec := pedirMetricas(t, router, tokenCustodioDePrueba)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("el token del custodio abrió /metrics: status = %d", rec.Code)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/depositos/confirmaciones",
		strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+tokenMetricasDePrueba)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("el token de métricas abrió las confirmaciones: status = %d", rec.Code)
	}
}

// TestMetricasNoAceptaElMetodoDeEscritura deja la ruta en solo lectura.
func TestMetricasNoAceptaElMetodoDeEscritura(t *testing.T) {
	t.Parallel()
	router := routerConMetricas(t, MetricasDelRouter{
		Handler:      exposicionDePrueba(),
		Autenticador: autenticadorDePrueba{token: tokenMetricasDePrueba},
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+tokenMetricasDePrueba)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("POST /metrics devolvió 200, quiero un rechazo (cuerpo %q)", rec.Body.String())
	}
}
