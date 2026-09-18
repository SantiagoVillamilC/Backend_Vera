package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Evento es una acción de autenticación del trail de auditoría, sin ningún campo donde quepa un secreto.
type Evento struct {
	Actor     uuid.UUID
	Resultado string
	Fecha     time.Time
	Origen    string
	Contexto  string
}

// RegistradorEventos registra un evento de autenticación; A1-04 reemplaza el adaptador nulo por el real.
type RegistradorEventos interface {
	Registrar(ctx context.Context, evento Evento) error
}

// RegistradorNulo descarta los eventos para que medio A2 y A3 consuman la interfaz desde la fase 0.
type RegistradorNulo struct{}

// Registrar descarta el evento y nunca falla.
func (RegistradorNulo) Registrar(context.Context, Evento) error { return nil }

var _ RegistradorEventos = RegistradorNulo{}
