package vozlocal

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestFlujoLocalEntregaAntesDelFinYNoAceptaCortes(t *testing.T) {
	for _, completar := range []bool{true, false} {
		t.Run(fmt.Sprint(completar), func(t *testing.T) {
			primero := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/flujo" {
					t.Error("ruta incorrecta")
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				_, _ = fmt.Fprint(w, "{\"tipo\":\"audio\",\"pcm\":\"AAABAA==\"}\n")
				_ = http.NewResponseController(w).Flush()
				select {
				case <-primero:
				case <-r.Context().Done():
					return
				}
				if completar {
					_, _ = fmt.Fprint(w, "{\"tipo\":\"fin\"}\n")
				}
			}))
			defer srv.Close()
			v, _ := Nueva(srv.URL, strings.Repeat("a", 32))
			b, err := v.SintetizarFlujo(t.Context(), "Hola", "Dora", func(pcm []byte) error {
				if len(pcm) != 4 {
					t.Error("PCM inválido")
				}
				close(primero)
				return nil
			})
			if completar && (err != nil || !WAVValido(b)) {
				t.Fatal(err)
			}
			if !completar && (err == nil || len(b) != 0) {
				t.Fatal("aceptó audio parcial")
			}
		})
	}
}

func wavPrueba() []byte {
	b := make([]byte, 48)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 40)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 24000)
	binary.LittleEndian.PutUint32(b[28:], 48000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 4)
	return b
}

func TestVozLocalPreservaTextoAutenticaYValidaAudio(t *testing.T) {
	const token = "token-local-ficticio-de-prueba-123456"
	const texto = "Tienes 120000.00 COP disponibles."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var entrada struct {
			Texto string `json:"texto"`
		}
		if r.URL.Path != "/sintetizar" || r.Header.Get("X-Vera-Token") != token || json.NewDecoder(r.Body).Decode(&entrada) != nil || entrada.Texto != texto {
			t.Error("contrato inválido")
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wavPrueba())
	}))
	defer srv.Close()
	v, err := Nueva(srv.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	b, err := v.Sintetizar(t.Context(), texto, "Dora")
	if err != nil || !WAVValido(b) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := v.Sintetizar(ctx, texto, "Dora"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestVozLocalNoSaleDeLoopbackNiSigueRedirecciones(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:8766", "http://example.com", "http://127.0.0.1:8766?clave=privada", "http://usuario@127.0.0.1:8766"} {
		if _, err := Nueva(endpoint, strings.Repeat("a", 32)); err == nil {
			t.Fatal("aceptó endpoint", endpoint)
		}
	}
	llamadas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		llamadas++
		http.Redirect(w, r, "http://example.com", http.StatusFound)
	}))
	defer srv.Close()
	v, _ := Nueva(srv.URL, strings.Repeat("a", 32))
	if _, err := v.Sintetizar(t.Context(), "Hola", "Dora"); !errors.Is(err, copilot.ErrNoDisponibleIA) || llamadas != 1 {
		t.Fatal(err, llamadas)
	}
}

func TestVozLocalRechazaWAVTruncadoODeOtraFrecuencia(t *testing.T) {
	for _, cambio := range []func([]byte) []byte{
		func(b []byte) []byte { return b[:46] },
		func(b []byte) []byte { binary.LittleEndian.PutUint32(b[24:], 16000); return b },
		func(b []byte) []byte { b[40] = 2; return b },
	} {
		if WAVValido(cambio(wavPrueba())) {
			t.Fatal("aceptó WAV inválido")
		}
	}
	if WAVValido(nil) {
		t.Fatal("aceptó audio vacío")
	}
}

func TestWAVConMetadatosNoSuponeCabeceraDe44Bytes(t *testing.T) {
	original := wavPrueba()
	b := append([]byte{}, original[:36]...)
	b = append(b, []byte("LIST")...)
	b = append(b, 4, 0, 0, 0, 'I', 'N', 'F', 'O')
	b = append(b, original[36:]...)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	if !WAVValido(b) {
		t.Fatal("rechazó metadatos RIFF válidos")
	}
}

func TestVozLocalEsperaUnaFraseCanceladaSinConsumirGoogle(t *testing.T) {
	llamadas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		llamadas++
		if llamadas == 1 {
			w.WriteHeader(http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wavPrueba())
	}))
	defer srv.Close()
	v, _ := Nueva(srv.URL, strings.Repeat("a", 32))
	if b, err := v.Sintetizar(t.Context(), "Hola", "Dora"); err != nil || !WAVValido(b) || llamadas != 2 {
		t.Fatal(err, llamadas)
	}
}
