package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type sintesisPrueba func(context.Context, string, string) ([]byte, error)

func (f sintesisPrueba) Sintetizar(c context.Context, t, v string) ([]byte, error) { return f(c, t, v) }

// routerSintesis monta el router con sesión válida salvo que sinSesion pida lo contrario.
// Ningún caso configura un token de voz: el endpoint no debe depender de él.
func routerSintesis(sinSesion bool, sintetizador copilot.SintetizadorVoz, consentimiento bool, uso *usoVeraPrueba) http.Handler {
	var id IdentidadDeLaPeticion = identidadDeContextoFijo{id: identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()}}
	if sinSesion {
		id = auth.UsuarioDePrueba{}
	}
	return NewRouterConVera(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: id}, DependenciasVera{Sintetizador: sintetizador, Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{consentimiento}, nil), Uso: uso})
}

func TestSintesisRouter(t *testing.T) {
	for _, tc := range []struct {
		nombre, cabecera, body string
		fallo, sinSesion       bool
		sinSintetizador        bool
		sinConsentimiento      bool
		status                 int
		esperaAudio            bool
		esperaDegradado        bool
		sintetizaciones, usos  int
	}{
		{nombre: "sin cabecera con sintetizador", body: `{"texto":"Hola"}`, status: 200, esperaAudio: true, sintetizaciones: 1, usos: 1},
		{nombre: "con cabecera cualquiera", cabecera: "cualquier-valor", body: `{"texto":"Hola"}`, status: 200, esperaAudio: true, sintetizaciones: 1, usos: 1},
		{nombre: "con cabecera corta", cabecera: "x", body: `{"texto":"Hola"}`, status: 200, esperaAudio: true, sintetizaciones: 1, usos: 1},
		{nombre: "degradado por fallo del servicio", body: `{"texto":"Hola"}`, fallo: true, status: 200, esperaDegradado: true, sintetizaciones: 1, usos: 1},
		{nombre: "sin sintetizador degrada sin descontar cuota y no es 401", body: `{"texto":"Hola"}`, sinSintetizador: true, status: 200, esperaDegradado: true, usos: 0},
		{nombre: "sin sintetizador y sin sesion", body: `{"texto":"Hola"}`, sinSintetizador: true, sinSesion: true, status: 401},
		{nombre: "sin sintetizador y sin consentimiento", body: `{"texto":"Hola"}`, sinSintetizador: true, sinConsentimiento: true, status: 403},
		{nombre: "sin sintetizador y texto vacio", body: `{"texto":"  "}`, sinSintetizador: true, status: 422},
		{nombre: "sin sesion", body: `{"texto":"Hola"}`, sinSesion: true, status: 401},
		{nombre: "sin consentimiento", body: `{"texto":"Hola"}`, sinConsentimiento: true, status: 403},
		{nombre: "texto vacio", body: `{"texto":"  "}`, status: 422},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			llamadas := 0
			var v copilot.SintetizadorVoz
			if !tc.sinSintetizador {
				v = sintesisPrueba(func(_ context.Context, txt, voz string) ([]byte, error) {
					llamadas++
					if txt != "Hola" || voz != "ef_dora" {
						t.Error("entrada incorrecta")
					}
					if tc.fallo {
						return nil, copilot.ErrNoDisponibleIA
					}
					return []byte("WAV-prueba"), nil
				})
			}
			uso := &usoVeraPrueba{}
			router := routerSintesis(tc.sinSesion, v, !tc.sinConsentimiento, uso)
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/vera/sintetizar", strings.NewReader(tc.body))
			if tc.cabecera != "" {
				r.Header.Set("X-Vera-Token", tc.cabecera)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if tc.esperaAudio && (w.Header().Get("Content-Type") != "audio/wav" || w.Body.String() != "WAV-prueba") {
				t.Fatalf("sin audio: %s %s", w.Header().Get("Content-Type"), w.Body)
			}
			if tc.esperaDegradado && (!strings.Contains(w.Body.String(), `"audio_disponible":false`) || !strings.Contains(w.Body.String(), `"voz_local":true`) || w.Header().Get("X-Vera-Audio") != "no-disponible") {
				t.Fatal(w.Body)
			}
			if llamadas != tc.sintetizaciones {
				t.Fatalf("sintetizaciones = %d, quiero %d", llamadas, tc.sintetizaciones)
			}
			if uso.llamadas != tc.usos {
				t.Fatalf("cuota descontada %d veces, quiero %d", uso.llamadas, tc.usos)
			}
			if tc.usos > 0 && uso.canal != copilot.CanalVoz {
				t.Fatal("cuota incorrecta")
			}
		})
	}
}

func TestSintesisConsentimientoYCuota(t *testing.T) {
	for _, aceptado := range []bool{false, true} {
		uso := &usoVeraPrueba{err: &copilot.ErrorLimiteUso{ReintentarEn: time.Minute}}
		// La cuota solo se descuenta cuando hay sintetizador: sin él el endpoint degrada sin gastarla.
		v := sintesisPrueba(func(context.Context, string, string) ([]byte, error) { return []byte("WAV-prueba"), nil })
		s := servidorV1{Vera: DependenciasVera{Sintetizador: v, Consentimiento: copilot.NuevoControlConsentimiento(consentimientoUsoPrueba{aceptado}, nil), Uso: uso}}
		ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
		r := httptest.NewRequestWithContext(ctx, "POST", "/", strings.NewReader(`{"texto":"Hola"}`))
		w := httptest.NewRecorder()
		s.SintetizarVera(w, r)
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

// textoDeLongitud arma un texto de n caracteres (runas) con la letra dada.
func textoDeLongitud(n int, letra string) string { return strings.Repeat(letra, n) }

func TestSintesisTopeDeTexto(t *testing.T) {
	// El valor se fija a propósito: cambiarlo exige revisar la cuenta de docs/stt-tts-operacion.md.
	const topeFijado = 400
	if maxTextoSintesis != topeFijado {
		t.Fatalf("maxTextoSintesis = %d, quiero %d", maxTextoSintesis, topeFijado)
	}
	for _, tc := range []struct {
		nombre string
		texto  string
		status int
	}{
		{"justo en el tope", textoDeLongitud(topeFijado, "a"), 200},
		{"un caracter sobre el tope", textoDeLongitud(topeFijado+1, "a"), 422},
		{"el tope se cuenta en caracteres y no en bytes", textoDeLongitud(topeFijado, "á"), 200},
		{"un caracter acentuado sobre el tope", textoDeLongitud(topeFijado+1, "á"), 422},
		{"muy por encima del tope anterior", textoDeLongitud(8192, "a"), 422},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			llamadas := 0
			v := sintesisPrueba(func(_ context.Context, txt, _ string) ([]byte, error) {
				llamadas++
				if txt != tc.texto {
					t.Error("el texto no llega intacto")
				}
				return []byte("WAV-prueba"), nil
			})
			uso := &usoVeraPrueba{}
			r := httptest.NewRequestWithContext(t.Context(), "POST", "/v1/vera/sintetizar", strings.NewReader(`{"texto":"`+tc.texto+`"}`))
			w := httptest.NewRecorder()
			routerSintesis(false, v, true, uso).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d, quiero %d", w.Code, tc.status)
			}
			if tc.status == 422 {
				if !strings.Contains(w.Body.String(), "ValidationError") || llamadas != 0 || uso.llamadas != 0 {
					t.Fatalf("un texto largo no debe sintetizar ni gastar cuota: %s %d %d", w.Body, llamadas, uso.llamadas)
				}
			}
		})
	}
}

// El tope del contrato (maxLength y la etiqueta validate generada) debe ser el mismo que usa el handler.
func TestSintesisTopeCoincideConElContrato(t *testing.T) {
	campo, ok := reflect.TypeOf(api.SintetizarVeraRequest{}).FieldByName("Texto")
	if !ok {
		t.Fatal("SintetizarVeraRequest sin campo Texto")
	}
	if esperado := "required,max=" + strconv.Itoa(maxTextoSintesis); campo.Tag.Get("validate") != esperado {
		t.Fatalf("validate = %q, quiero %q", campo.Tag.Get("validate"), esperado)
	}
}
