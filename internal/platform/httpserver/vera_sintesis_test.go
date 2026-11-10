package httpserver

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type sintesisPrueba func(context.Context, string, string) ([]byte, error)

func (f sintesisPrueba) Sintetizar(c context.Context, t, v string) ([]byte, error) { return f(c, t, v) }

func TestSintesisRouter(t *testing.T) {
	const token = "token-ficticio-de-pruebas-1234567890"
	for _, tc := range []struct {
		nombre, token, body string
		fallo               bool
		status              int
	}{
		{"voz", token, `{"texto":"Hola"}`, false, 200},
		{"degradado", token, `{"texto":"Hola"}`, true, 200},
		{"token", "incorrecto", `{"texto":"Hola"}`, false, 401},
		{"vacio", token, `{"texto":"  "}`, false, 422},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			llamadas := 0
			v := sintesisPrueba(func(_ context.Context, txt, voz string) ([]byte, error) {
				llamadas++
				if txt != "Hola" || voz != "ef_dora" {
					t.Error("entrada incorrecta")
				}
				if tc.fallo {
					return nil, copilot.ErrNoDisponibleIA
				}
				return []byte("WAV-prueba"), nil
			})
			router := NewRouterConVera(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()}}}, DependenciasVera{Sintetizador: v, TokenVoz: token, Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{true}, nil), Uso: &usoVeraPrueba{}})
			r := httptest.NewRequest("POST", "/v1/vera/sintetizar", strings.NewReader(tc.body))
			r.Header.Set("X-Vera-Token", tc.token)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if tc.fallo && (!strings.Contains(w.Body.String(), `"voz_local":true`) || w.Header().Get("X-Vera-Audio") != "no-disponible") {
				t.Fatal(w.Body)
			}
			if tc.status != 200 && llamadas != 0 {
				t.Fatal("sintetizó solicitud inválida")
			}
		})
	}
}

func TestSintesisConsentimientoYCuota(t *testing.T) {
	const token = "token-ficticio-de-pruebas-1234567890"
	for _, aceptado := range []bool{false, true} {
		uso := &usoVeraPrueba{err: &copilot.ErrorLimiteUso{ReintentarEn: time.Minute}}
		s := servidorV1{Vera: DependenciasVera{TokenVoz: token, Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{aceptado}, nil), Uso: uso}}
		ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
		r := httptest.NewRequestWithContext(ctx, "POST", "/", strings.NewReader(`{"texto":"Hola"}`))
		w := httptest.NewRecorder()
		s.SintetizarVera(w, r, api.SintetizarVeraParams{XVeraToken: token})
		esperado := 403
		if aceptado {
			esperado = 429
			if uso.canal != copilot.CanalVoz {
				t.Fatal("cuota incorrecta")
			}
		}
		if w.Code != esperado {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
}
