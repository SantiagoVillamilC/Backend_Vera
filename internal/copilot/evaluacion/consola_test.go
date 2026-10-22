package evaluacion

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestConsolaContextoTrazaYCuota(t *testing.T) {
	llamadas := 0
	p := proveedorEvaluacionPrueba(func(_ context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		llamadas++
		if llamadas == 3 && len(s.Historial) != 5 {
			t.Fatalf("perdió contexto: %d turnos", len(s.Historial))
		}
		if llamadas%2 == 1 {
			return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{ID: "saldo", Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}}}}}, nil
		}
		return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "Disponible 120000.00 COP."}}}}, nil
	})
	var eventos []EventoConsola
	s, err := NuevaConsola(configConsolaPrueba(), p, func(e EventoConsola) { eventos = append(eventos, e) }, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		r, err := s.Preguntar(t.Context(), "¿Cuánto tengo?")
		if err != nil || r.Texto != "Disponible 120000.00 COP." {
			t.Fatalf("respuesta: %+v %v", r, err)
		}
	}
	if len(eventos) != 4 || eventos[0].Tipo != "funcion" || eventos[1].Tipo != "resultado" || eventos[1].Nombre != "consultar_saldo" || !json.Valid(eventos[1].Datos) {
		t.Fatalf("traza: %+v", eventos)
	}
	s.NuevaConversacion()
	if _, err := s.Preguntar(t.Context(), "Otra"); !errors.Is(err, ErrCuotaEvaluacion) || llamadas != 4 {
		t.Fatalf("reinició la cuota: %d %v", llamadas, err)
	}
}

func configConsolaPrueba() ConfigReal {
	return ConfigReal{Modelo: "prueba", Repeticiones: 1, MaxSolicitudes: 4, NivelGratuitoConfirmado: true}
}
