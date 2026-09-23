package auth

import (
	"context"
	"errors"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// ContextoUsuario es el contexto de login por defecto: la sesión opera como la persona.
	ContextoUsuario = "usuario"
	// ContextoEmpresa es el contexto de login que exige un rol de empresa vigente.
	ContextoEmpresa = "empresa"
)

// AutenticadorDeCredenciales valida celular y contraseña entregando la credencial autenticada.
type AutenticadorDeCredenciales interface {
	Autenticar(ctx context.Context, celular, contrasena, origen string) (CredencialAutenticada, error)
}

// ServicioDeLogin autentica por celular y emite el par de tokens de la credencial activa.
type ServicioDeLogin struct {
	autenticador AutenticadorDeCredenciales
	pool         abridorDeTransaccionesAuth
	sesiones     *GestorSesiones
	tokens       *TokensDeAcceso
}

// NuevoServicioDeLogin exige el autenticador, el pool, el gestor de sesiones y el firmador.
func NuevoServicioDeLogin(autenticador AutenticadorDeCredenciales, pool *pgxpool.Pool, sesiones *GestorSesiones, tokens *TokensDeAcceso) (*ServicioDeLogin, error) {
	if autenticador == nil || pool == nil || sesiones == nil || tokens == nil {
		return nil, errors.New("auth: el servicio de login necesita autenticador, pool, sesiones y firmador")
	}
	return &ServicioDeLogin{autenticador: autenticador, pool: poolDeAuth{pool: pool}, sesiones: sesiones, tokens: tokens}, nil
}

// IniciarSesion autentica por celular y emite el par; la cuenta pendiente responde PhoneUnverified.
func (s *ServicioDeLogin) IniciarSesion(ctx context.Context, celular, contrasena, contexto, origen, dispositivo string) (SesionEmitida, error) {
	credencial, err := s.autenticador.Autenticar(ctx, celular, contrasena, origen)
	if err != nil {
		return SesionEmitida{}, err
	}
	if credencial.Estado == EstadoPendienteVerificacion {
		return SesionEmitida{}, ErrCelularSinVerificar
	}
	var (
		refresh string
		sesion  Sesion
		rol     string
		usuario DatosUsuario
	)
	_, err = enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		contextoID, rolResuelto, err := s.sesiones.resolverContextoEn(ctx, q, credencial.ParteID, contexto)
		if err != nil {
			return struct{}{}, err
		}
		usuario, err = s.sesiones.usuarioEn(ctx, q, credencial.ParteID, contextoID, rolResuelto)
		if err != nil {
			return struct{}{}, err
		}
		refresh, sesion, err = s.sesiones.emitirSesionEn(ctx, q, credencial.ParteID, contextoID, dispositivo)
		if err != nil {
			return struct{}{}, err
		}
		rol = rolResuelto
		return struct{}{}, nil
	})
	if err != nil {
		return SesionEmitida{}, err
	}
	access, err := s.tokens.Firmar(ClaimsAccess{
		ParteID:  sesion.ParteID,
		SesionID: sesion.FamiliaID,
		Contexto: sesion.ContextoActivo,
		Rol:      rol,
	})
	if err != nil {
		return SesionEmitida{}, err
	}
	return SesionEmitida{AccessToken: access, RefreshToken: refresh, Usuario: usuario}, nil
}
