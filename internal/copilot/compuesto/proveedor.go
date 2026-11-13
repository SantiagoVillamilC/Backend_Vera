// Package compuesto combina STT local y un proveedor de texto bajo un solo
// puerto del asistente: la transcripción nunca sale del equipo.
package compuesto

import (
	"context"
	"encoding/json"
	"reflect"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// Transcriptor convierte audio en texto sin conservar bytes ni transcripciones.
type Transcriptor interface {
	Transcribir(context.Context, copilot.AudioIA) (string, error)
}

// Proveedor atiende la transcripción local y delega el resto al proveedor de texto.
type Proveedor struct {
	siguiente    copilot.ProveedorIA
	transcriptor Transcriptor
}

var _ copilot.ProveedorIA = (*Proveedor)(nil)

// Nuevo rechaza dependencias ausentes, incluidos los punteros tipados nulos.
func Nuevo(siguiente copilot.ProveedorIA, transcriptor Transcriptor) (*Proveedor, error) {
	if ausente(siguiente) || ausente(transcriptor) {
		return nil, copilot.ErrSolicitudIA
	}
	return &Proveedor{siguiente: siguiente, transcriptor: transcriptor}, nil
}

// Generar transcribe audio local cuando la solicitud lo pide y, en cualquier otro
// caso, entrega el turno completo al proveedor siguiente sin alterarlo.
func (p *Proveedor) Generar(ctx context.Context, solicitud copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	if p == nil || ausente(p.siguiente) || ausente(p.transcriptor) {
		return copilot.RespuestaIA{}, copilot.ErrNoDisponibleIA
	}
	if !solicitud.SoloTranscripcion {
		return p.siguiente.Generar(ctx, solicitud)
	}
	audio, err := audioUnico(solicitud)
	if err != nil {
		return copilot.RespuestaIA{}, err
	}
	texto, err := p.transcriptor.Transcribir(ctx, audio)
	if err != nil {
		return copilot.RespuestaIA{}, err
	}
	if err := ctx.Err(); err != nil {
		return copilot.RespuestaIA{}, err
	}
	if !utf8.ValidString(texto) {
		return copilot.RespuestaIA{}, copilot.ErrRespuestaIA
	}
	b, err := json.Marshal(struct {
		Transcripcion string `json:"transcripcion"`
	}{texto})
	if err != nil {
		return copilot.RespuestaIA{}, copilot.ErrRespuestaIA
	}
	return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: string(b)}}}}, nil
}

// audioUnico exige un turno de usuario con una única parte de audio y sin herramientas.
func audioUnico(s copilot.SolicitudIA) (copilot.AudioIA, error) {
	if len(s.Funciones) != 0 || len(s.Historial) != 1 {
		return copilot.AudioIA{}, copilot.ErrSolicitudIA
	}
	turno := s.Historial[0]
	if turno.Rol != "usuario" || len(turno.Partes) != 1 {
		return copilot.AudioIA{}, copilot.ErrSolicitudIA
	}
	parte := turno.Partes[0]
	if parte.Audio == nil || parte.Texto != "" || parte.Llamada != nil || parte.Resultado != nil || len(parte.Firma) != 0 {
		return copilot.AudioIA{}, copilot.ErrSolicitudIA
	}
	return *parte.Audio, nil
}

// ausente descarta el nil explícito y el puntero tipado nulo, que no es igual a nil
// cuando viaja dentro de una interfaz.
func ausente(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}
