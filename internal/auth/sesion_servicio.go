package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PrefijoContextoEmpresa marca el contexto activo de una empresa representada.
const PrefijoContextoEmpresa = "empresa:"

// ContextoDeEmpresa arma el contexto_id de una empresa representada.
func ContextoDeEmpresa(id uuid.UUID) string { return PrefijoContextoEmpresa + id.String() }

// EmpresaDeContexto extrae el uuid de un contexto_id de empresa.
func EmpresaDeContexto(contexto string) (uuid.UUID, bool) {
	if !strings.HasPrefix(contexto, PrefijoContextoEmpresa) {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(strings.TrimPrefix(contexto, PrefijoContextoEmpresa))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// DatosUsuario es lo mínimo para armar el usuario de la sesión emitida.
type DatosUsuario struct {
	ID             uuid.UUID
	TipoCuenta     string
	Correo         string
	NombreCompleto string
	RazonSocial    string
	ContextoActivo string
	RolActivo      string
}

// SesionEmitida es el par de tokens y el usuario que devuelve la verificación del OTP.
type SesionEmitida struct {
	AccessToken  string
	RefreshToken string
	Usuario      DatosUsuario
}

// TokensRotados es el par de tokens que devuelve la rotación del refresh.
type TokensRotados struct {
	AccessToken  string
	RefreshToken string
}

// ServicioDeSesion emite el par tras el segundo factor y rota el refresh.
type ServicioDeSesion struct {
	pool     abridorDeTransaccionesAuth
	desafios *GestorDesafios
	sesiones *GestorSesiones
	tokens   *TokensDeAcceso
	denylist RevocadorDeSesiones
}

// NuevoServicioDeSesion exige el pool, los gestores, el firmador y la denylist.
func NuevoServicioDeSesion(pool *pgxpool.Pool, desafios *GestorDesafios, sesiones *GestorSesiones, tokens *TokensDeAcceso, denylist RevocadorDeSesiones) (*ServicioDeSesion, error) {
	if pool == nil || desafios == nil || sesiones == nil || tokens == nil {
		return nil, errors.New("auth: el servicio de sesión necesita pool, desafíos, sesiones y firmador")
	}
	return &ServicioDeSesion{pool: poolDeAuth{pool: pool}, desafios: desafios, sesiones: sesiones, tokens: tokens, denylist: denylist}, nil
}

// VerificarOtp verifica el desafío de login, emite el par y firma el access fuera de la transacción.
func (s *ServicioDeSesion) VerificarOtp(ctx context.Context, id uuid.UUID, codigo, dispositivo string) (SesionEmitida, error) {
	var (
		refresh string
		sesion  Sesion
		rol     string
		usuario DatosUsuario
		dominio error
	)
	_, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		resultado, err := s.desafios.VerificarDesafioEn(ctx, q, id, codigo, PropositoLogin)
		if err != nil {
			dominio = err
			return struct{}{}, nil
		}
		contexto, rolResuelto, err := s.resolverContextoEn(ctx, q, resultado.ParteID, resultado.Contexto)
		if err != nil {
			return struct{}{}, err
		}
		usuario, err = s.usuarioEn(ctx, q, resultado.ParteID, contexto, rolResuelto)
		if err != nil {
			return struct{}{}, err
		}
		refresh, sesion, err = s.sesiones.emitirSesionEn(ctx, q, resultado.ParteID, contexto, dispositivo)
		if err != nil {
			return struct{}{}, err
		}
		rol = rolResuelto
		return struct{}{}, nil
	})
	if err != nil {
		return SesionEmitida{}, err
	}
	if dominio != nil {
		return SesionEmitida{}, dominio
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

// RotarRefresh rota la familia del refresh, resuelve el rol actual y publica el reuso en la denylist.
func (s *ServicioDeSesion) RotarRefresh(ctx context.Context, refreshEnClaro string) (TokensRotados, error) {
	var (
		refresh        string
		sesion         Sesion
		rol            string
		familiaReusada uuid.UUID
		dominio        error
	)
	_, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		out, err := s.sesiones.rotarRefreshEn(ctx, q, refreshEnClaro)
		if err != nil {
			return struct{}{}, err
		}
		if out.reusado {
			familiaReusada, dominio = out.familiaReusada, ErrRefreshReusado
			return struct{}{}, nil
		}
		rol, err = s.rolDeSesionEn(ctx, q, out.sesion)
		if err != nil {
			return struct{}{}, err
		}
		refresh, sesion = out.refresh, out.sesion
		return struct{}{}, nil
	})
	if err != nil {
		return TokensRotados{}, err
	}
	if dominio != nil {
		if err := s.publicarSid(ctx, familiaReusada); err != nil {
			return TokensRotados{}, err
		}
		return TokensRotados{}, dominio
	}
	access, err := s.tokens.Firmar(ClaimsAccess{
		ParteID:  sesion.ParteID,
		SesionID: sesion.FamiliaID,
		Contexto: sesion.ContextoActivo,
		Rol:      rol,
	})
	if err != nil {
		return TokensRotados{}, err
	}
	return TokensRotados{AccessToken: access, RefreshToken: refresh}, nil
}

// resolverContextoEn convierte el contexto pedido en el contexto_id concreto y su rol.
func (s *ServicioDeSesion) resolverContextoEn(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID, solicitado string) (string, string, error) {
	if solicitado == "" || solicitado == ContextoUsuario {
		return ContextoUsuario, "", nil
	}
	if solicitado != ContextoEmpresa {
		return "", "", fmt.Errorf("auth: contexto de sesión desconocido %q", solicitado)
	}
	roles, err := s.sesiones.rolesVigentesEn(ctx, q, parteID)
	if err != nil {
		return "", "", err
	}
	if len(roles) == 0 {
		return "", "", ErrSinRolDeEmpresa
	}
	return ContextoDeEmpresa(roles[0].RepresentadaID), roles[0].Rol, nil
}

// rolDeSesionEn resuelve el rol vigente de la empresa del contexto activo de la sesión.
func (s *ServicioDeSesion) rolDeSesionEn(ctx context.Context, q *repositorio.Queries, sesion Sesion) (string, error) {
	empresaID, esEmpresa := EmpresaDeContexto(sesion.ContextoActivo)
	if !esEmpresa {
		return "", nil
	}
	roles, err := s.sesiones.rolesVigentesEn(ctx, q, sesion.ParteID)
	if err != nil {
		return "", err
	}
	for _, rol := range roles {
		if rol.RepresentadaID == empresaID {
			return rol.Rol, nil
		}
	}
	return "", ErrSinRolDeEmpresa
}

// usuarioEn arma el usuario de la sesión con el tipo de cuenta y el nombre del contexto activo.
func (s *ServicioDeSesion) usuarioEn(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID, contexto, rol string) (DatosUsuario, error) {
	fila, err := q.UsuarioDeParte(ctx, pgtype.UUID{Bytes: parteID, Valid: true})
	if err != nil {
		return DatosUsuario{}, fmt.Errorf("auth: leer el usuario de la parte: %w", err)
	}
	usuario := DatosUsuario{
		ID:             parteID,
		TipoCuenta:     fila.Tipo,
		Correo:         fila.Correo.String,
		NombreCompleto: fila.NombreCompleto.String,
		ContextoActivo: contexto,
		RolActivo:      rol,
	}
	if empresaID, esEmpresa := EmpresaDeContexto(contexto); esEmpresa {
		filaEmpresa, err := q.UsuarioDeParte(ctx, pgtype.UUID{Bytes: empresaID, Valid: true})
		if err != nil {
			return DatosUsuario{}, fmt.Errorf("auth: leer el usuario de la empresa: %w", err)
		}
		if !filaEmpresa.RazonSocial.Valid {
			return DatosUsuario{}, fmt.Errorf("auth: la empresa %s no tiene razón social", empresaID)
		}
		usuario.ID = empresaID
		usuario.TipoCuenta = filaEmpresa.Tipo
		usuario.RazonSocial = filaEmpresa.RazonSocial.String
		usuario.NombreCompleto = ""
	}
	return usuario, nil
}

// publicarSid publica la familia revocada en la denylist con el ttl del access.
func (s *ServicioDeSesion) publicarSid(ctx context.Context, sesionID uuid.UUID) error {
	if s.denylist == nil {
		return nil
	}
	return s.denylist.Revocar(ctx, sesionID, duracionAccess)
}
