//go:build integration

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// pedirMontoMaximo consulta el máximo enviable del usuario autenticado por el router real.
func pedirMontoMaximo(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/v1/transferencias/maximo"+query, nil)
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// HU-NF-06 Criterio 14: GET autenticado devuelve el máximo enviable sin cruzar el tope exento.
func TestHU_NF_06_Criterio14_MontoMaximoDevuelveElSaldoExento(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nucleo.LectorNivelKYCFijo{}, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 100000000, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	rec := pedirMontoMaximo(t, r, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	data := datosDeSaldo(t, rec)
	if got := data["monto"]; got != "100000000" {
		t.Errorf("monto = %v, quiero %q", got, "100000000")
	}
	if got := data["moneda"]; got != "COP" {
		t.Errorf("moneda = %v, quiero COP", got)
	}
	marca, ok := data["calculado_en"].(string)
	if !ok {
		t.Fatalf("calculado_en = %v, quiero una cadena RFC3339", data["calculado_en"])
	}
	instante, err := time.Parse(time.RFC3339Nano, marca)
	if err != nil {
		t.Fatalf("calculado_en %q no es RFC3339: %v", marca, err)
	}
	if delta := time.Since(instante); delta < 0 || delta > time.Minute {
		t.Errorf("calculado_en = %s, quiero un instante reciente (delta %s)", instante, delta)
	}
}

// HU-NF-06 Criterio 14: sin credencial el máximo enviable no se responde.
func TestHU_NF_06_Criterio14_MontoMaximoSinCredencialDa401(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nucleo.LectorNivelKYCFijo{}, nil, nil, nil)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}})

	req := httptest.NewRequest(http.MethodGet, "/v1/transferencias/maximo", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
}

// HU-NF-06 Criterio 14: apuntar a otra cuenta se rechaza y no devuelve el máximo ajeno.
func TestHU_NF_06_Criterio14_MontoMaximoDeOtroUsuarioSeRechaza(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nucleo.LectorNivelKYCFijo{}, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 100000000, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	rec := pedirMontoMaximo(t, r, "?usuario_id=00000000-0000-4000-8000-000000000002")

	assertErrorEnvelope(t, rec, http.StatusBadRequest, "TargetNotAllowed")
}
