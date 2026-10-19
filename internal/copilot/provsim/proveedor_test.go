package provsim

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestGuionDeterminista(t *testing.T) {
	respuesta := copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{
		{Llamada: &copilot.LlamadaIA{ID: "a", Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}},
		{Llamada: &copilot.LlamadaIA{ID: "b", Nombre: "consultar_limites", Argumentos: json.RawMessage(`{}`)}},
	}}, TokensEntrada: 10, TokensSalida: 5}
	guion := []Paso{{Respuesta: respuesta}, {Error: copilot.ErrNoDisponibleIA}}
	a, b := Nuevo(guion...), Nuevo(guion...)
	respuesta.Turno.Partes[0].Llamada.Nombre = "mutado"
	ctx, cancelar := context.WithCancel(t.Context())
	cancelar()
	if _, err := a.Generar(ctx, copilot.SolicitudIA{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for range 3 {
		ra, ea := a.Generar(t.Context(), copilot.SolicitudIA{})
		rb, eb := b.Generar(t.Context(), copilot.SolicitudIA{})
		if !reflect.DeepEqual(ra, rb) || !errors.Is(ea, eb) {
			t.Fatal("guion no determinista")
		}
		if len(ra.Turno.Partes) > 0 && ra.Turno.Partes[0].Llamada.Nombre != "consultar_saldo" {
			t.Fatal("guion comparte memoria del llamador")
		}
	}
	if _, err := a.Generar(t.Context(), copilot.SolicitudIA{}); !errors.Is(err, copilot.ErrGuionAgotadoIA) {
		t.Fatal(err)
	}
}

func TestConsumoConcurrenteSinDuplicados(t *testing.T) {
	pasos := make([]Paso, 20)
	p := Nuevo(pasos...)
	var exitos atomic.Int32
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			_, err := p.Generar(t.Context(), copilot.SolicitudIA{})
			if err == nil {
				exitos.Add(1)
			} else if !errors.Is(err, copilot.ErrGuionAgotadoIA) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if exitos.Load() != 20 {
		t.Fatalf("consumidos=%d", exitos.Load())
	}
}

func TestSimuladoPorDefecto(t *testing.T) {
	p := Nuevo()
	a, err := p.Generar(t.Context(), copilot.SolicitudIA{})
	if err != nil {
		t.Fatal(err)
	}
	a.Turno.Partes[0].Texto = "mutado"
	b, err := p.Generar(t.Context(), copilot.SolicitudIA{})
	if err != nil || b.Turno.Partes[0].Texto == "mutado" || b.Turno.Partes[0].Texto == "" {
		t.Fatal("respuesta compartida o vacía")
	}
}

func TestAudioPorDefectoNoInventaTranscripcion(t *testing.T) {
	r, err := Nuevo().Generar(t.Context(), copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: &copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1}}}}}}})
	if err != nil || len(r.Turno.Partes) != 1 || r.Turno.Partes[0].Texto != `{"transcripcion":""}` {
		t.Fatalf("%+v %v", r, err)
	}
}
