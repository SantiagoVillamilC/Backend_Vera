package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTranscribirSinHerramientasNiHistorial(t *testing.T) {
	audio := &AudioIA{MIME: "audio/wav", Datos: clipPrueba(t, "tono.wav")}
	for _, tc := range []struct {
		nombre, raw string
		err         error
	}{
		{"valido", `{"transcripcion":"¿Cuál es mi saldo?"}`, nil},
		{"silencio", `{"transcripcion":""}`, ErrAudioNoEntendido},
		{"blancos", `{"transcripcion":"   "}`, ErrAudioNoEntendido},
		{"malformado", `no es JSON`, ErrRespuestaIA},
		{"controles en palabras", `{"transcripcion":"Hola, Vera, \u0019c\u0013mo est\u0015s?"}`, ErrRespuestaIA},
		{"extra", `{"transcripcion":"saldo","accion":"ejecutar"}`, ErrRespuestaIA},
		{"largo", `{"transcripcion":"` + strings.Repeat("a", 6001) + `"}`, ErrRespuestaIA},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			p := proveedorCicloPrueba{generar: func(_ context.Context, s SolicitudIA) (RespuestaIA, error) {
				if !s.SoloTranscripcion || len(s.Funciones) != 0 || len(s.Historial) != 1 || s.Historial[0].Rol != "usuario" || len(s.Historial[0].Partes) != 1 || s.Historial[0].Partes[0].Audio != audio || s.Instrucciones != instruccionesTranscripcion {
					t.Fatal("transcripción recibió funciones o contexto extra")
				}
				return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: tc.raw}}}}, nil
			}}
			r, err := transcribirAudio(t.Context(), p, audio)
			if !errors.Is(err, tc.err) || tc.err == nil && r != "¿Cuál es mi saldo?" {
				t.Fatalf("%q %v", r, err)
			}
		})
	}
	p := proveedorCicloPrueba{generar: func(context.Context, SolicitudIA) (RespuestaIA, error) {
		return RespuestaIA{Turno: TurnoIA{Rol: "asistente", Partes: []ParteIA{{Llamada: &LlamadaIA{Nombre: "preparar_transferencia"}}}}}, nil
	}}
	if _, err := transcribirAudio(t.Context(), p, audio); !errors.Is(err, ErrRespuestaIA) {
		t.Fatal("permitió función durante transcripción")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := transcribirAudio(ctx, p, audio); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
