package laboratorio

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
)

func TestLaboratorioAutorizaYTrazaSinRed(t *testing.T) {
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 5, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for nombre, tc := range map[string]struct {
		host, token, origin string
		status              int
	}{
		"sin token":    {"localhost:8787", "", "", 401},
		"host ajeno":   {"otro.example:8787", "token-prueba", "", 403},
		"origen ajeno": {"localhost:8787", "token-prueba", "https://otro.example", 403},
		"válida":       {"localhost:8787", "token-prueba", "http://localhost:8787", 200},
	} {
		t.Run(nombre, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8787/api/preguntar", strings.NewReader("¿Cuánto tengo?"))
			r.Host = tc.host
			r.Header.Set("X-Vera-Token", tc.token)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", "text/plain")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d cuerpo=%s", w.Code, w.Body.String())
			}
			if w.Code == 200 && (!bytes.Contains(w.Body.Bytes(), []byte(`"consultar_saldo"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"12000000"`))) {
				t.Fatal(w.Body.String())
			}
		})
	}
}

type vozPrueba struct {
	llamadas int
	textos   []string
}

func (v *vozPrueba) Sintetizar(_ context.Context, texto, _ string) ([]byte, error) {
	v.llamadas++
	v.textos = append(v.textos, texto)
	return wavPrueba(), nil
}

func TestVozUsaUltimaRespuestaCacheYCuotaComun(t *testing.T) {
	v := &vozPrueba{}
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 3, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", false, v)
	if err != nil {
		t.Fatal(err)
	}
	peticion := func(ruta, texto string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8787"+ruta, strings.NewReader(texto))
		r.Header.Set("X-Vera-Token", "token-prueba")
		r.Header.Set("Content-Type", "text/plain")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := peticion("/api/voz", "Aoede"); w.Code != 422 || v.llamadas != 0 {
		t.Fatal("generó voz sin respuesta")
	}
	if w := peticion("/api/preguntar", "¿Cuánto tengo?"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := peticion("/api/voz", "texto arbitrario"); w.Code != 422 || v.llamadas != 0 {
		t.Fatal("aceptó texto arbitrario")
	}
	for range 2 {
		if w := peticion("/api/voz", "Aoede"); w.Code != 200 || !strings.Contains(w.Body.String(), `"usadas":3`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if v.llamadas != 1 || !strings.Contains(v.textos[0], "120000.00 COP") {
		t.Fatal("no reutilizó la voz o alteró la respuesta", v)
	}
	if w := peticion("/api/voz", "Puck"); w.Code != 429 || v.llamadas != 1 {
		t.Fatal("no respetó el presupuesto")
	}
	if w := peticion("/api/nueva", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := peticion("/api/voz", "Aoede"); w.Code != 422 || v.llamadas != 1 {
		t.Fatal("conservó audio de la conversación anterior")
	}
}

func TestAudioComparteCuotaYRechazaBytesInvalidos(t *testing.T) {
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 3, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	peticion := func(ruta, tipo string, b []byte) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:8787"+ruta, bytes.NewReader(b))
		r.Header.Set("X-Vera-Token", "token-prueba")
		r.Header.Set("Content-Type", tipo)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := peticion("/api/transcribir", "audio/wav", []byte("no es audio")); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := peticion("/api/transcribir", "audio/wav", wavPrueba())
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"usadas":1`) || !strings.Contains(w.Body.String(), "transcripcion") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = peticion("/api/preguntar", "text/plain", []byte("¿Cuánto tengo?"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"usadas":3`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = peticion("/api/nueva", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"usadas":3`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = peticion("/api/transcribir", "audio/wav", wavPrueba())
	if w.Code != 429 {
		t.Fatal(w.Code, w.Body.String())
	}
	var sobre map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &sobre); err != nil || sobre["success"] != false {
		t.Fatal(w.Body.String(), err)
	}
}

func wavPrueba() []byte {
	b := make([]byte, 44+32000)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 32000)
	return b
}
