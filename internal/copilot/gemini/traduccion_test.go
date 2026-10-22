package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestSolicitudesInvalidasNoLlamanRed(t *testing.T) {
	for _, caso := range []struct {
		nombre    string
		modificar func(*copilot.SolicitudIA)
	}{
		{"sinTurnos", func(s *copilot.SolicitudIA) { s.Historial = nil }},
		{"rol", func(s *copilot.SolicitudIA) { s.Historial[0].Rol = "system" }},
		{"sinPartes", func(s *copilot.SolicitudIA) { s.Historial[0].Partes = nil }},
		{"parteVacia", func(s *copilot.SolicitudIA) { s.Historial[0].Partes[0] = copilot.ParteIA{} }},
		{"mezcla", func(s *copilot.SolicitudIA) { s.Historial[0].Partes[0].Audio = &copilot.AudioIA{} }},
		{"audioURL", func(s *copilot.SolicitudIA) {
			s.Historial[0].Partes[0] = copilot.ParteIA{Audio: &copilot.AudioIA{MIME: "text/plain", Datos: []byte("x")}}
		}},
		{"llamadaUsuario", func(s *copilot.SolicitudIA) {
			s.Historial[0].Partes[0] = copilot.ParteIA{Llamada: &copilot.LlamadaIA{Nombre: "saldo", Argumentos: json.RawMessage(`{}`)}}
		}},
		{"resultadoInvalido", func(s *copilot.SolicitudIA) {
			s.Historial[0].Rol = "herramienta"
			s.Historial[0].Partes[0] = copilot.ParteIA{Resultado: &copilot.ResultadoIA{Nombre: "saldo", Datos: json.RawMessage(`[]`)}}
		}},
		{"esquemaArray", func(s *copilot.SolicitudIA) {
			s.Funciones = []copilot.FuncionIA{{Nombre: "f", Parametros: json.RawMessage(`[]`)}}
		}},
		{"esquemaNulo", func(s *copilot.SolicitudIA) {
			s.Funciones = []copilot.FuncionIA{{Nombre: "f", Parametros: json.RawMessage(`null`)}}
		}},
		{"esquemaRoto", func(s *copilot.SolicitudIA) {
			s.Funciones = []copilot.FuncionIA{{Nombre: "f", Parametros: json.RawMessage(`{`)}}
		}},
		{"duplicada", func(s *copilot.SolicitudIA) {
			f := copilot.FuncionIA{Nombre: "f", Parametros: json.RawMessage(`{"type":"object"}`)}
			s.Funciones = []copilot.FuncionIA{f, f}
		}},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			var n atomic.Int32
			p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) { n.Add(1); fmt.Fprint(w, respuestaTexto) })
			s := solicitudTexto()
			caso.modificar(&s)
			_, err := p.Generar(t.Context(), s)
			if !errors.Is(err, copilot.ErrSolicitudIA) || n.Load() != 0 {
				t.Fatalf("err=%v llamadas=%d", err, n.Load())
			}
		})
	}
}

func TestNoEntregaArgumentosRedondeados(t *testing.T) {
	p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"f","args":{"anidado":[9223372036854775807]}}}]},"finishReason":"STOP"}]}`)
	})
	r, err := p.Generar(t.Context(), solicitudTexto())
	if !errors.Is(err, copilot.ErrRespuestaIA) || len(r.Turno.Partes) != 0 {
		t.Fatalf("se entregaron argumentos inseguros: %+v %v", r, err)
	}
}

func TestConsumoIncluyePensamientoSinExponerlo(t *testing.T) {
	p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"interno","thought":true},{"text":"Hola"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3,"thoughtsTokenCount":7}}`)
	})
	r, err := p.Generar(t.Context(), solicitudTexto())
	if err != nil || r.TokensSalida != 10 || len(r.Turno.Partes) != 1 || r.Turno.Partes[0].Texto != "Hola" {
		t.Fatalf("respuesta=%+v error=%v", r, err)
	}
}
