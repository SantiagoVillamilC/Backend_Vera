package laboratorio

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
)

func TestLaboratorioAutorizaYTrazaSinRed(t *testing.T) {
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 5, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", true)
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

func TestAudioComparteCuotaYRechazaBytesInvalidos(t *testing.T) {
	h, err := Nuevo(evaluacion.ConfigReal{Modelo: "demo", Repeticiones: 1, MaxSolicitudes: 3, NivelGratuitoConfirmado: true}, ProveedorDemo{}, "token-prueba", "localhost:8787", true)
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
