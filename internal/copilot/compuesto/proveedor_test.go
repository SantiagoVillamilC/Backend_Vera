package compuesto

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

type transcriptorFake struct {
	llamadas int
	mime     string
	datos    []byte
	texto    string
	err      error
}

func (f *transcriptorFake) Transcribir(_ context.Context, a copilot.AudioIA) (string, error) {
	f.llamadas++
	f.mime = a.MIME
	f.datos = append([]byte(nil), a.Datos...)
	return f.texto, f.err
}

type proveedorFake struct {
	llamadas  int
	solicitud copilot.SolicitudIA
	respuesta copilot.RespuestaIA
	err       error
}

func (f *proveedorFake) Generar(_ context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	f.llamadas++
	f.solicitud = s
	return f.respuesta, f.err
}

func solicitudAudio(audio *copilot.AudioIA) copilot.SolicitudIA {
	return copilot.SolicitudIA{
		SoloTranscripcion: true,
		Instrucciones:     "Sólo transcripción JSON.",
		Historial:         []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: audio}}}},
	}
}

func textoUnico(t *testing.T, r copilot.RespuestaIA) string {
	t.Helper()
	if r.Turno.Rol != "asistente" || len(r.Turno.Partes) != 1 {
		t.Fatalf("turno inesperado: %+v", r.Turno)
	}
	parte := r.Turno.Partes[0]
	if parte.Audio != nil || parte.Llamada != nil || parte.Resultado != nil || len(parte.Firma) != 0 {
		t.Fatalf("parte con datos sobrantes: %+v", parte)
	}
	return parte.Texto
}

func transcripcionDe(t *testing.T, salida string) string {
	t.Helper()
	var v map[string]string
	if err := json.Unmarshal([]byte(salida), &v); err != nil {
		t.Fatalf("salida no es JSON: %q %v", salida, err)
	}
	if len(v) != 1 {
		t.Fatalf("claves inesperadas: %v", v)
	}
	texto, ok := v["transcripcion"]
	if !ok {
		t.Fatalf("falta la clave transcripcion: %v", v)
	}
	return texto
}

func TestGenerarTranscripcion(t *testing.T) {
	audio := &copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1, 2, 3}}
	tr := &transcriptorFake{texto: `Dijo "hola" ¿Cuánto tengo?`}
	sig := &proveedorFake{}
	p, err := Nuevo(sig, tr)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Generar(t.Context(), solicitudAudio(audio))
	if err != nil {
		t.Fatal(err)
	}
	parte := textoUnico(t, r)
	if got := transcripcionDe(t, parte); got != `Dijo "hola" ¿Cuánto tengo?` {
		t.Fatalf("transcripción alterada: %q", got)
	}
	if tr.llamadas != 1 || tr.mime != audio.MIME || len(tr.datos) != len(audio.Datos) {
		t.Fatalf("audio no llegó intacto: %+v", tr)
	}
	if sig.llamadas != 0 {
		t.Fatal("se delegó una transcripción")
	}
}

func TestGenerarTranscripcionVacia(t *testing.T) {
	tr := &transcriptorFake{}
	p, err := Nuevo(&proveedorFake{}, tr)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Generar(t.Context(), solicitudAudio(&copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1}}))
	if err != nil {
		t.Fatal(err)
	}
	if parte := textoUnico(t, r); parte != `{"transcripcion":""}` {
		t.Fatalf("se inventó texto para un audio vacío: %q", parte)
	}
}

func TestGenerarErrorTranscriptor(t *testing.T) {
	for _, categoría := range []error{copilot.ErrTimeoutIA, copilot.ErrNoDisponibleIA, copilot.ErrAudioNoEntendido, context.Canceled} {
		t.Run(categoría.Error(), func(t *testing.T) {
			p, err := Nuevo(&proveedorFake{}, &transcriptorFake{err: categoría})
			if err != nil {
				t.Fatal(err)
			}
			r, err := p.Generar(t.Context(), solicitudAudio(&copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1}}))
			if !errors.Is(err, categoría) {
				t.Fatalf("error transformado: %v", err)
			}
			if r.Turno.Rol != "" || len(r.Turno.Partes) != 0 {
				t.Fatalf("respuesta inventada tras error: %+v", r)
			}
		})
	}
}

func TestGenerarRechazaUTF8Invalido(t *testing.T) {
	p, err := Nuevo(&proveedorFake{}, &transcriptorFake{texto: string([]byte{0xff})})
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Generar(t.Context(), solicitudAudio(&copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1}}))
	if !errors.Is(err, copilot.ErrRespuestaIA) {
		t.Fatalf("error inesperado: %v", err)
	}
	if r.Turno.Rol != "" || len(r.Turno.Partes) != 0 {
		t.Fatalf("respuesta inventada tras error: %+v", r)
	}
}

func TestGenerarContextoCanceladoDespuesDeTranscribir(t *testing.T) {
	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()
	p, err := Nuevo(&proveedorFake{}, &transcriptorFake{texto: "hola"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Generar(ctx, solicitudAudio(&copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1}})); !errors.Is(err, context.Canceled) {
		t.Fatalf("error inesperado: %v", err)
	}
}

func TestGenerarNilReceiver(t *testing.T) {
	var p *Proveedor
	r, err := p.Generar(t.Context(), solicitudAudio(&copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1}}))
	if !errors.Is(err, copilot.ErrNoDisponibleIA) {
		t.Fatalf("error inesperado: %v", err)
	}
	if r.Turno.Rol != "" || len(r.Turno.Partes) != 0 {
		t.Fatalf("respuesta inventada: %+v", r)
	}
}

func TestGenerarDelegaSinTranscripcion(t *testing.T) {
	s := copilot.SolicitudIA{
		Instrucciones: "Responde breve.",
		Historial:     []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: "¿Cuánto tengo?", Firma: []byte{9}}}}},
		Funciones:     []copilot.FuncionIA{{Nombre: "saldo"}},
	}
	respuesta := copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "Cien"}}}, TokensEntrada: 7, TokensSalida: 2}
	sig := &proveedorFake{respuesta: respuesta}
	tr := &transcriptorFake{}
	p, err := Nuevo(sig, tr)
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Generar(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if r.Turno.Rol != respuesta.Turno.Rol || r.Turno.Partes[0].Texto != "Cien" || r.TokensEntrada != 7 || r.TokensSalida != 2 {
		t.Fatalf("respuesta delegada alterada: %+v", r)
	}
	if sig.llamadas != 1 || !reflect.DeepEqual(sig.solicitud, s) {
		t.Fatalf("solicitud delegada alterada: %+v", sig.solicitud)
	}
	if tr.llamadas != 0 {
		t.Fatal("se transcribió una solicitud de texto")
	}
}

func TestGenerarRechazaSolicitudInvalida(t *testing.T) {
	audio := &copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1}}
	otro := &copilot.AudioIA{MIME: "audio/wav", Datos: []byte{2}}
	parteAudio := func(a *copilot.AudioIA) copilot.TurnoIA {
		return copilot.TurnoIA{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: a}}}
	}
	casos := []struct {
		nombre    string
		solicitud copilot.SolicitudIA
	}{
		{"sin turnos", copilot.SolicitudIA{SoloTranscripcion: true}},
		{"sin turno de usuario", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "hola"}}}}}},
		{"dos turnos", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{parteAudio(audio), parteAudio(otro)}}},
		{"rol asistente", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "asistente", Partes: []copilot.ParteIA{{Audio: audio}}}}}},
		{"dos partes", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: audio}, {Audio: otro}}}}}},
		{"sin audio", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: "hola"}}}}}},
		{"audio nulo", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: make([]copilot.ParteIA, 1)}}}},
		{"audio y texto", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: audio, Texto: "hola"}}}}}},
		{"llamada", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: audio, Llamada: &copilot.LlamadaIA{ID: "1"}}}}}}},
		{"resultado", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: audio, Resultado: &copilot.ResultadoIA{ID: "1"}}}}}}},
		{"firma", copilot.SolicitudIA{SoloTranscripcion: true, Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Audio: audio, Firma: []byte{1}}}}}}},
		{"con funciones", copilot.SolicitudIA{SoloTranscripcion: true, Funciones: []copilot.FuncionIA{{Nombre: "saldo"}}, Historial: []copilot.TurnoIA{parteAudio(audio)}}},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			tr := &transcriptorFake{texto: "inventado"}
			p, err := Nuevo(&proveedorFake{}, tr)
			if err != nil {
				t.Fatal(err)
			}
			r, err := p.Generar(t.Context(), caso.solicitud)
			if !errors.Is(err, copilot.ErrSolicitudIA) {
				t.Fatalf("error inesperado: %v", err)
			}
			if r.Turno.Rol != "" || len(r.Turno.Partes) != 0 || tr.llamadas != 0 {
				t.Fatalf("se transcribió una solicitud inválida: %+v %+v", r, tr)
			}
		})
	}
}

func TestNuevoRechazaDependenciasAusentes(t *testing.T) {
	sig := &proveedorFake{}
	tr := &transcriptorFake{}
	casos := []struct {
		nombre       string
		siguiente    copilot.ProveedorIA
		transcriptor Transcriptor
	}{
		{"sin siguiente", nil, tr},
		{"sin transcriptor", sig, nil},
		{"puntero tipado nulo de transcriptor", sig, (*transcriptorFake)(nil)},
		{"puntero tipado nulo de siguiente", (*proveedorFake)(nil), tr},
		{"ambos nulos", nil, nil},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			if p, err := Nuevo(caso.siguiente, caso.transcriptor); !errors.Is(err, copilot.ErrSolicitudIA) || p != nil {
				t.Fatalf("se aceptó una dependencia ausente: %+v %v", p, err)
			}
		})
	}
	if p, err := Nuevo(sig, tr); err != nil || p == nil {
		t.Fatalf("no se construyó el proveedor válido: %+v %v", p, err)
	}
}
