package sttlocal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestTranscribirContrato(t *testing.T) {
	for _, tc := range []struct {
		status        int
		cuerpo, texto string
		err           error
	}{
		{200, `{"transcripcion":" hola ","modelo":"small","idioma":"es"}`, "hola", nil},
		{200, `{"transcripcion":"","modelo":"small","idioma":"es"}`, "", nil},
		{200, `{"modelo":"small","idioma":"es"}`, "", copilot.ErrRespuestaIA},
		{200, `{"transcripcion":"hola","modelo":"otro","idioma":"es"}`, "", copilot.ErrRespuestaIA},
		{200, `{`, "", copilot.ErrRespuestaIA},
		{401, "secreto", "", copilot.ErrCredencialesIA}, {409, "", "", copilot.ErrLimiteIA},
		{413, "", "", copilot.ErrSolicitudIA}, {415, "", "", copilot.ErrSolicitudIA}, {422, "", "", copilot.ErrSolicitudIA}, {500, "", "", copilot.ErrNoDisponibleIA},
	} {
		t.Run(http.StatusText(tc.status)+tc.texto, func(t *testing.T) {
			const token = "token-de-prueba-local-123456789012345"
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/transcribir" || r.Header.Get("X-Vera-Token") != token || r.Header.Get("Content-Type") != "audio/wav" {
					t.Error("contrato incorrecto")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.cuerpo))
			}))
			defer s.Close()
			c, err := Nuevo(s.URL, token, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			texto, err := c.Transcribir(t.Context(), copilot.AudioIA{MIME: "audio/wav", Datos: []byte("clip")})
			if texto != tc.texto || !errors.Is(err, tc.err) {
				t.Fatalf("%q %v", texto, err)
			}
		})
	}
}

func TestClienteLimitesYPlazo(t *testing.T) {
	liberar := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-liberar:
		}
	}))
	defer s.Close()
	defer close(liberar)
	c, e := Nuevo(s.URL, strings.Repeat("a", 32), 10*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Transcribir(t.Context(), copilot.AudioIA{MIME: "audio/wav", Datos: []byte("clip")}); !errors.Is(e, copilot.ErrTimeoutIA) {
		t.Fatal(e)
	}
	for _, a := range []copilot.AudioIA{{MIME: "text/plain", Datos: []byte("a")}, {MIME: "audio/wav"}, {MIME: "audio/wav", Datos: make([]byte, 8*1024*1024+1)}} {
		if _, e = c.Transcribir(t.Context(), a); !errors.Is(e, copilot.ErrSolicitudIA) {
			t.Fatal(e)
		}
	}
}

func TestClienteRestringeDestinoYCancelacion(t *testing.T) {
	for _, u := range []string{"https://127.0.0.1", "http://example.com", "http://localhost", "http://127.0.0.1/otro", "http://user@127.0.0.1"} {
		if _, e := Nuevo(u, strings.Repeat("a", 32), time.Second); e == nil {
			t.Fatal(u)
		}
	}
	c, e := Nuevo("http://127.0.0.1:1", strings.Repeat("a", 32), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = c.Transcribir(ctx, copilot.AudioIA{MIME: "audio/wav", Datos: []byte("clip")}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
