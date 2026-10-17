// Package provsim ofrece un proveedor determinista sin red ni credenciales.
package provsim

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// Paso es una respuesta o un error predefinido por el escenario de prueba.
type Paso struct {
	Respuesta copilot.RespuestaIA
	Error     error
}

// Proveedor consume un guion en orden. Crear uno por escenario/conversación.
// Sin guion responde siempre el mismo aviso de simulación, sin inventar datos.
type Proveedor struct {
	mu     sync.Mutex
	pasos  []Paso
	indice int
}

// Nuevo copia las respuestas para que el guion no comparta memoria con el cliente.
func Nuevo(pasos ...Paso) *Proveedor {
	p := &Proveedor{pasos: make([]Paso, len(pasos))}
	for i, paso := range pasos {
		p.pasos[i] = Paso{Respuesta: copiar(paso.Respuesta), Error: paso.Error}
	}
	return p
}

// Generar consume como máximo un paso y no consume ninguno si el contexto venció.
func (p *Proveedor) Generar(ctx context.Context, _ copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return copilot.RespuestaIA{}, err
	}
	if len(p.pasos) == 0 {
		return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "Modo simulado: no se ha consultado información financiera."}}}}, nil
	}
	if p.indice >= len(p.pasos) {
		return copilot.RespuestaIA{}, copilot.ErrGuionAgotadoIA
	}
	paso := p.pasos[p.indice]
	p.indice++
	return copiar(paso.Respuesta), paso.Error
}

func copiar(r copilot.RespuestaIA) copilot.RespuestaIA {
	// Los tipos del puerto sólo contienen datos JSON. Un guion inválido es un error
	// de programación del test, no una respuesta del proveedor.
	b, err := json.Marshal(r)
	if err != nil {
		panic("provsim: guion JSON inválido")
	}
	var copia copilot.RespuestaIA
	if err := json.Unmarshal(b, &copia); err != nil {
		panic("provsim: copia inválida")
	}
	return copia
}

var _ copilot.ProveedorIA = (*Proveedor)(nil)
