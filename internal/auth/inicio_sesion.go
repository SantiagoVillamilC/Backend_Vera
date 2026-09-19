package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const (
	// ContextoUsuario es el contexto de login por defecto: la sesión opera como la persona.
	ContextoUsuario = "usuario"
	// ContextoEmpresa es el contexto de login que exige un rol de empresa vigente.
	ContextoEmpresa = "empresa"
	// MensajeDesafioLogin es el texto del 200 cuando el segundo factor es el inicio de sesión.
	MensajeDesafioLogin = "Codigo enviado por WhatsApp"
	// MensajeDesafioRegistro es el texto del 200 cuando la cuenta pendiente reanuda la verificación.
	MensajeDesafioRegistro = "Verifica tu celular para activar la cuenta"
)

// LoginData es el desenlace del inicio de sesión: el desafío y su propósito, nunca tokens.
type LoginData struct {
	Proposito             Proposito
	ChallengeID           uuid.UUID
	ExpiraEnSegundos      int
	Mensaje               string
	RequiereSegundoFactor bool
}

// AutenticadorDeCredenciales valida correo y contraseña entregando la credencial autenticada.
type AutenticadorDeCredenciales interface {
	Autenticar(ctx context.Context, correo, contrasena, origen string) (CredencialAutenticada, error)
}

// CreadorDeDesafiosRacionados crea el desafío OTP aplicando los topes agregados de cuenta y origen.
type CreadorDeDesafiosRacionados interface {
	CrearDesafioRacionado(ctx context.Context, solicitud Solicitud) (Desafio, error)
}

// LectorDeRolesVigentes resuelve los roles vigentes de la parte para el contexto empresa.
type LectorDeRolesVigentes interface {
	RolesVigentesDeParte(ctx context.Context, parteID uuid.UUID) ([]RolVigente, error)
}

// ServicioDeLogin resuelve el desenlace del login y crea el desafío del segundo factor.
type ServicioDeLogin struct {
	autenticador AutenticadorDeCredenciales
	desafios     CreadorDeDesafiosRacionados
	roles        LectorDeRolesVigentes
}

// NuevoServicioDeLogin exige los tres colaboradores del login.
func NuevoServicioDeLogin(autenticador AutenticadorDeCredenciales, desafios CreadorDeDesafiosRacionados, roles LectorDeRolesVigentes) (*ServicioDeLogin, error) {
	if autenticador == nil || desafios == nil || roles == nil {
		return nil, errors.New("auth: el servicio de login necesita autenticador, desafíos y roles")
	}
	return &ServicioDeLogin{autenticador: autenticador, desafios: desafios, roles: roles}, nil
}

// IniciarSesion autentica y crea el desafío del segundo factor; no emite access ni refresh.
func (s *ServicioDeLogin) IniciarSesion(ctx context.Context, correo, contrasena, contexto, origen string) (LoginData, error) {
	credencial, err := s.autenticador.Autenticar(ctx, correo, contrasena, origen)
	if err != nil {
		return LoginData{}, err
	}
	if credencial.Estado == EstadoPendienteVerificacion {
		return s.desafioDeRegistro(ctx, credencial, origen)
	}
	return s.desafioDeLogin(ctx, credencial, contexto, origen)
}

// desafioDeRegistro reanuda la verificación del celular de la cuenta pendiente sin emitir tokens.
func (s *ServicioDeLogin) desafioDeRegistro(ctx context.Context, credencial CredencialAutenticada, origen string) (LoginData, error) {
	desafio, err := s.crearDesafio(ctx, credencial, PropositoRegistro, "", origen)
	if err != nil {
		return LoginData{}, err
	}
	return resultadoDeLogin(desafio, MensajeDesafioRegistro), nil
}

// desafioDeLogin resuelve el contexto y crea el desafío del segundo factor de la cuenta activa.
func (s *ServicioDeLogin) desafioDeLogin(ctx context.Context, credencial CredencialAutenticada, contexto, origen string) (LoginData, error) {
	contexto, err := s.resolverContexto(ctx, credencial.ParteID, contexto)
	if err != nil {
		return LoginData{}, err
	}
	desafio, err := s.crearDesafio(ctx, credencial, PropositoLogin, contexto, origen)
	if err != nil {
		return LoginData{}, err
	}
	return resultadoDeLogin(desafio, MensajeDesafioLogin), nil
}

// resolverContexto aplica el default usuario y exige al menos un rol vigente para empresa.
func (s *ServicioDeLogin) resolverContexto(ctx context.Context, parteID uuid.UUID, contexto string) (string, error) {
	if contexto == "" || contexto == ContextoUsuario {
		return ContextoUsuario, nil
	}
	if contexto != ContextoEmpresa {
		return "", fmt.Errorf("auth: contexto de login desconocido %q", contexto)
	}
	roles, err := s.roles.RolesVigentesDeParte(ctx, parteID)
	if err != nil {
		return "", err
	}
	if len(roles) == 0 {
		return "", ErrSinRolDeEmpresa
	}
	return ContextoEmpresa, nil
}

// crearDesafio arma la solicitud con el celular registrado y el correo normalizado.
func (s *ServicioDeLogin) crearDesafio(ctx context.Context, credencial CredencialAutenticada, proposito Proposito, contexto, origen string) (Desafio, error) {
	return s.desafios.CrearDesafioRacionado(ctx, Solicitud{
		ParteID:           credencial.ParteID,
		CredencialID:      credencial.CredencialID,
		Proposito:         proposito,
		Celular:           credencial.Celular,
		Contexto:          contexto,
		Origen:            origen,
		CorreoNormalizado: NormalizarCorreo(credencial.Correo),
	})
}

// resultadoDeLogin arma la respuesta 200 con el desafío y nunca con tokens.
func resultadoDeLogin(desafio Desafio, mensaje string) LoginData {
	return LoginData{
		Proposito:             desafio.Proposito,
		ChallengeID:           desafio.ID,
		ExpiraEnSegundos:      desafio.ExpiraEnSegundos,
		Mensaje:               mensaje,
		RequiereSegundoFactor: true,
	}
}
