package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const tokenControl = "token-de-control-de-prueba-0123456789abcdef"

type controladorFalso struct {
	vista    Vista
	iniciado bool
	err      error
	apagadas int
}

func (c *controladorFalso) Encender() (Vista, bool, error) { return c.vista, c.iniciado, c.err }
func (c *controladorFalso) Apagar() Vista                  { c.apagadas++; return c.vista }
func (c *controladorFalso) Vista() Vista                   { return c.vista }

type respuestaHTTP struct {
	StatusCode int
	Header     http.Header
}

func peticion(t *testing.T, h http.Handler, metodo, ruta, token string) (respuestaHTTP, map[string]any) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), metodo, ruta, strings.NewReader(""))
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var cuerpo map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &cuerpo)
	return respuestaHTTP{StatusCode: rec.Code, Header: rec.Header()}, cuerpo
}

func nuevoManejador(c Controlador) http.Handler {
	return ManejadorControl(c, tokenControl, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestControlExigeTokenBearerCorrecto(t *testing.T) {
	h := nuevoManejador(&controladorFalso{vista: Vista{Estado: "apagado"}})
	for _, aut := range []string{"", "Bearer", "Bearer incorrecto", "Basic " + tokenControl, tokenControl, "Bearer " + tokenControl + "x", "bearer " + tokenControl} {
		res, cuerpo := peticion(t, h, http.MethodGet, "/estado", aut)
		if res.StatusCode != 401 || cuerpo["success"] != false || res.Header.Get("WWW-Authenticate") == "" {
			t.Errorf("autorizacion %q: status=%d cuerpo=%v", aut, res.StatusCode, cuerpo)
		}
		if aut != "" && strings.Contains(fmt.Sprint(cuerpo), tokenControl) {
			t.Error("la respuesta no debe repetir el token")
		}
	}
	res, cuerpo := peticion(t, h, http.MethodGet, "/estado", "Bearer "+tokenControl)
	if res.StatusCode != 200 || cuerpo["success"] != true {
		t.Fatalf("status=%d cuerpo=%v", res.StatusCode, cuerpo)
	}
}

func TestEstadoDevuelveLosCamposDelSobre(t *testing.T) {
	minutos := 7.5
	h := nuevoManejador(&controladorFalso{vista: Vista{Estado: "listo", GPU: "NVIDIA RTX A5000", TarifaUSDH: 0.27, MinutosEncendido: 12, MinutosHastaApagadoPorInactividad: &minutos, CostoEstimadoUSD: 0.05}})
	_, cuerpo := peticion(t, h, http.MethodGet, "/estado", "Bearer "+tokenControl)
	datos, ok := cuerpo["data"].(map[string]any)
	if !ok {
		t.Fatalf("cuerpo %v", cuerpo)
	}
	for _, clave := range []string{"estado", "gpu", "tarifa_usd_h", "minutos_encendido", "minutos_hasta_apagado_por_inactividad", "costo_estimado_usd"} {
		if _, existe := datos[clave]; !existe {
			t.Errorf("falta %s en %v", clave, datos)
		}
	}
	if _, existe := cuerpo["meta"]; !existe || cuerpo["error"] != nil {
		t.Fatalf("el sobre debe traer meta y error nulo: %v", cuerpo)
	}
}

func TestEncenderMapeaCodigos(t *testing.T) {
	casos := []struct {
		nombre   string
		ctrl     controladorFalso
		esperado int
	}{
		{"inicia", controladorFalso{iniciado: true}, 202},
		{"idempotente", controladorFalso{}, 200},
		{"apagando", controladorFalso{err: ErrApagando}, 409},
		{"requiere apagar", controladorFalso{err: ErrRequiereApagar}, 409},
		{"configuracion", controladorFalso{err: fmt.Errorf("%w: falta el script", ErrConfiguracion)}, 412},
		{"otro", controladorFalso{err: errors.New("fallo interno con detalle sensible rpa_SECRETO")}, 500},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			res, cuerpo := peticion(t, nuevoManejador(&c.ctrl), http.MethodPost, "/encender", "Bearer "+tokenControl)
			if res.StatusCode != c.esperado {
				t.Fatalf("status=%d cuerpo=%v", res.StatusCode, cuerpo)
			}
			if strings.Contains(fmt.Sprint(cuerpo), "rpa_SECRETO") {
				t.Fatal("un error interno no debe filtrar detalles")
			}
		})
	}
}

func TestApagarYMetodosNoPermitidos(t *testing.T) {
	ctrl := &controladorFalso{vista: Vista{Estado: "apagando"}}
	h := nuevoManejador(ctrl)
	if res, _ := peticion(t, h, http.MethodPost, "/apagar", "Bearer "+tokenControl); res.StatusCode != 202 || ctrl.apagadas != 1 {
		t.Fatalf("status=%d apagadas=%d", res.StatusCode, ctrl.apagadas)
	}
	for _, c := range []struct{ metodo, ruta string }{{http.MethodGet, "/encender"}, {http.MethodGet, "/apagar"}, {http.MethodPost, "/estado"}, {http.MethodDelete, "/estado"}} {
		if res, _ := peticion(t, h, c.metodo, c.ruta, "Bearer "+tokenControl); res.StatusCode != 405 || res.Header.Get("Allow") == "" {
			t.Errorf("%s %s: status=%d", c.metodo, c.ruta, res.StatusCode)
		}
	}
	if res, _ := peticion(t, h, http.MethodGet, "/otra", "Bearer "+tokenControl); res.StatusCode != 404 {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestControlConElServicioReal(t *testing.T) {
	a := nuevoArnes(t)
	h := nuevoManejador(a.svc)
	if res, _ := peticion(t, h, http.MethodPost, "/encender", "Bearer "+tokenControl); res.StatusCode != 202 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	a.esperarFase("listo")
	if res, _ := peticion(t, h, http.MethodPost, "/encender", "Bearer "+tokenControl); res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	if res, _ := peticion(t, h, http.MethodPost, "/apagar", "Bearer "+tokenControl); res.StatusCode != 202 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	a.esperarFase("apagado")
}
