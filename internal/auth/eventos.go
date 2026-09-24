package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const (
	// TipoEventoRegistro marca el alta de una credencial.
	TipoEventoRegistro = "registro"
	// TipoEventoInicioSesion marca un intento de inicio de sesión.
	TipoEventoInicioSesion = "inicio_sesion"
	// TipoEventoVerificacion marca la verificación de un factor.
	TipoEventoVerificacion = "verificacion"
	// TipoEventoRecuperacion marca el reinicio de acceso.
	TipoEventoRecuperacion = "recuperacion"
	// TipoEventoCambioNumero marca el cambio de celular verificado.
	TipoEventoCambioNumero = "cambio_numero"
	// TipoEventoEmisionSesion marca la emisión de un par de tokens.
	TipoEventoEmisionSesion = "emision_sesion"
	// TipoEventoRotacion marca la rotación de un refresh.
	TipoEventoRotacion = "rotacion"
	// TipoEventoReusoDetectado marca el reuso de un refresh.
	TipoEventoReusoDetectado = "reuso_detectado"
	// TipoEventoRevocacion marca la revocación de una sesión o credencial.
	TipoEventoRevocacion = "revocacion"
	// TipoEventoDesafio marca la creación y la entrega de un desafío OTP.
	TipoEventoDesafio = "desafio"
)

// Evento es una acción de autenticación del trail de auditoría, sin ningún campo donde quepa un secreto.
type Evento struct {
	Tipo         string
	Actor        uuid.UUID
	CredencialID uuid.UUID
	SesionID     uuid.UUID
	Resultado    string
	Fecha        time.Time
	Origen       string
	Contexto     string
	Detalle      map[string]string
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
