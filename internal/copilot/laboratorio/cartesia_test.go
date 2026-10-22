package laboratorio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
)

func TestCatalinaUsaTextoLiteralYCacheSinSolicitudesGoogle(t *testing.T) {
	google := &vozConFallo{fallo: copilot.ErrLimiteIA}
	catalina := &vozPrueba{}
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 2, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", false, google, ConVozCartesia(catalina))
	if err != nil {
		t.Fatal(err)
	}
	peticionVoz(t, h, "/api/preguntar", "saldo")
	for _, ruta := range []string{"/api/voz/flujo", "/api/voz"} {
		w := peticionVoz(t, h, ruta, "Catalina")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"fuente":"cartesia"`) || !strings.Contains(w.Body.String(), `"usadas":2`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	const texto = "Tu saldo disponible es 120000.00 COP. Tienes 5000.00 COP retenidos."
	if google.llamadas != 0 || catalina.llamadas != 1 || catalina.textos[0] != texto {
		t.Fatalf("Google=%d Cartesia=%d textos=%v", google.llamadas, catalina.llamadas, catalina.textos)
	}
	// Nueva conversación limpia audio, pero conserva caracteres y cuota de Google.
	w := peticionVoz(t, h, "/api/nueva", "")
	if !strings.Contains(w.Body.String(), `"caracteres_cartesia":`+strconv.Itoa(utf8.RuneCountInString(texto))) {
		t.Fatal(w.Body.String())
	}
	if w := peticionVoz(t, h, "/api/voz", "Catalina"); w.Code != 422 || catalina.llamadas != 1 {
		t.Fatal("se leyó una respuesta anterior", w.Body.String())
	}
}

func TestCuotaOCredencialesCartesiaNoSeReintentanNiBloqueanChat(t *testing.T) {
	for nombre, tc := range map[string]struct {
		err    error
		codigo string
		status int
	}{
		"cuota": {&copilot.ErrorLimiteIA{Clase: copilot.LimiteDesconocido, ReintentarEn: time.Hour}, "CartesiaLimit", 429},
		"clave": {copilot.ErrCredencialesIA, "CartesiaCredentials", 401},
	} {
		t.Run(nombre, func(t *testing.T) {
			voz := &vozConFallo{fallo: tc.err}
			h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 10, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", false, nil, ConVozCartesia(voz))
			if err != nil {
				t.Fatal(err)
			}
			peticionVoz(t, h, "/api/preguntar", "saldo")
			for _, ruta := range []string{"/api/voz", "/api/voz/flujo"} {
				w := peticionVoz(t, h, ruta, "Catalina")
				if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.codigo) || strings.Contains(w.Body.String(), "Google") {
					t.Fatal(w.Code, w.Body.String())
				}
				if tc.status == 429 && w.Header().Get("Retry-After") == "" {
					t.Fatal("falta Retry-After")
				}
			}
			if w := peticionVoz(t, h, "/api/preguntar", "saldo"); w.Code != 200 || voz.llamadas != 1 {
				t.Fatal("chat bloqueado o voz reenviada", w.Code, voz.llamadas)
			}
		})
	}
}

func TestPresupuestoCartesiaReservaRunasAntesDeEnviar(t *testing.T) {
	v := &vozPrueba{}
	s := &servidor{vozCartesia: v, ultimoTexto: "Sí", caracteresCartesia: maxCaracteresCartesia - 2}
	if _, err := s.sintetizarCartesia(t.Context()); err != nil || s.caracteresCartesia != maxCaracteresCartesia {
		t.Fatal("no cuenta caracteres Unicode", err, s.caracteresCartesia)
	}
	if _, err := s.sintetizarCartesia(t.Context()); !errors.Is(err, errPresupuestoCartesia) || v.llamadas != 1 {
		t.Fatal("superó presupuesto", err, v.llamadas)
	}
	w := httptest.NewRecorder()
	s.errorVoz(w, errPresupuestoCartesia, "Catalina")
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "CartesiaSessionLimit") {
		t.Fatal(w.Body.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.sintetizarCartesia(ctx); !errors.Is(err, context.Canceled) || v.llamadas != 1 {
		t.Fatal(err)
	}
}
