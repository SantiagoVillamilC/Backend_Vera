package cartesia

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func wavPrueba() []byte {
	b := make([]byte, 46)
	copy(b, "RIFF")
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[4:], 38)
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 24000)
	binary.LittleEndian.PutUint32(b[28:], 48000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 2)
	return b
}

func TestVozNormalizaMarcadoresSóloTrasRecibirTodoElCuerpo(t *testing.T) {
	for _, cortar := range []bool{false, true} {
		t.Run(fmt.Sprint(cortar), func(t *testing.T) {
			b := wavPrueba()
			binary.LittleEndian.PutUint32(b[4:], ^uint32(0))
			binary.LittleEndian.PutUint32(b[40:], ^uint32(0))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if cortar {
					w.Header().Set("Content-Length", "1000")
				}
				_, _ = w.Write(b)
			}))
			defer srv.Close()
			v, _ := NuevaVoz("ficticia")
			v.endpoint = srv.URL
			v.cliente = srv.Client()
			audio, err := v.Sintetizar(t.Context(), "Hola", "Catalina")
			if cortar {
				if !errors.Is(err, copilot.ErrRespuestaIA) || len(audio) != 0 {
					t.Fatal("aceptó lectura truncada", err)
				}
			} else if err != nil || !bytes.Equal(audio, wavPrueba()) {
				t.Fatal("no normalizó", err)
			}
		})
	}
}

func TestVozEnviaTextoLiteralLocaleYContratoFijado(t *testing.T) {
	const texto = "Tu saldo disponible es 120000.00 COP."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var entrada struct {
			Modelo  string `json:"model_id"`
			Texto   string `json:"transcript"`
			Voz     string `json:"voice"`
			Locale  string `json:"locale"`
			Formato struct {
				Container string `json:"container"`
				Encoding  string `json:"encoding"`
				Rate      int    `json:"sample_rate"`
			} `json:"output_format"`
		}
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer clave-ficticia" || r.Header.Get("Cartesia-Version") != VersionAPI || json.NewDecoder(r.Body).Decode(&entrada) != nil {
			t.Error("cabeceras o JSON incorrectos")
		}
		if entrada.Modelo != ModeloVoz || entrada.Texto != texto || entrada.Voz != Candidatas()[0].ID || entrada.Locale != "es-CO" || entrada.Formato.Container != "wav" || entrada.Formato.Encoding != "pcm_s16le" || entrada.Formato.Rate != 24000 {
			t.Error("alteró el contrato o texto")
		}
		b := make([]byte, 46)
		copy(b, "RIFF")
		copy(b[8:], "WAVE")
		binary.LittleEndian.PutUint32(b[4:], 38)
		copy(b[12:], "fmt ")
		binary.LittleEndian.PutUint32(b[16:], 16)
		binary.LittleEndian.PutUint16(b[20:], 1)
		binary.LittleEndian.PutUint16(b[22:], 1)
		binary.LittleEndian.PutUint32(b[24:], 24000)
		binary.LittleEndian.PutUint32(b[28:], 48000)
		binary.LittleEndian.PutUint16(b[32:], 2)
		binary.LittleEndian.PutUint16(b[34:], 16)
		copy(b[36:], "data")
		binary.LittleEndian.PutUint32(b[40:], 2)
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	v, _ := NuevaVoz("clave-ficticia")
	v.endpoint = srv.URL
	v.cliente = srv.Client()
	b, err := v.Sintetizar(t.Context(), texto, "Catalina")
	if err != nil || len(b) != 46 {
		t.Fatal(err)
	}
}

func TestVozNoReintentaNiExponeErroresDelProveedor(t *testing.T) {
	for nombre, tc := range map[string]struct {
		status   int
		esperado error
	}{
		"cuota": {429, copilot.ErrLimiteIA}, "creditos": {402, copilot.ErrLimiteIA}, "credencial": {401, copilot.ErrCredencialesIA}, "servicio": {503, copilot.ErrNoDisponibleIA}, "wav inválido": {200, copilot.ErrRespuestaIA},
	} {
		t.Run(nombre, func(t *testing.T) {
			var llamadas atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				llamadas.Add(1)
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("cuerpo privado con credenciales"))
			}))
			defer srv.Close()
			v, _ := NuevaVoz("clave-ficticia")
			v.endpoint = srv.URL
			v.cliente = srv.Client()
			_, err := v.Sintetizar(t.Context(), "Hola", "Mariana")
			if !errors.Is(err, tc.esperado) || llamadas.Load() != 1 || err.Error() != tc.esperado.Error() {
				t.Fatal(err, llamadas.Load())
			}
			if tc.status == 429 {
				var limite *copilot.ErrorLimiteIA
				if !errors.As(err, &limite) || limite.ReintentarEn != 2*time.Minute {
					t.Fatal("perdió Retry-After")
				}
			}
			if _, err := v.Sintetizar(t.Context(), "Hola", "voz-arbitraria"); !errors.Is(err, copilot.ErrSolicitudIA) || llamadas.Load() != 1 {
				t.Fatal("consumió una llamada inválida")
			}
		})
	}
}

func TestVozNoSigueRedireccionesYRespetaCancelacion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com", http.StatusFound)
	}))
	defer srv.Close()
	v, _ := NuevaVoz("ficticia")
	v.endpoint = srv.URL
	v.cliente = srv.Client()
	if _, err := v.Sintetizar(t.Context(), "Hola", "Ximena"); !errors.Is(err, copilot.ErrNoDisponibleIA) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := v.Sintetizar(ctx, "Hola", "Catalina"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := NuevaVoz(" "); !errors.Is(err, copilot.ErrCredencialesIA) {
		t.Fatal(err)
	}
}
