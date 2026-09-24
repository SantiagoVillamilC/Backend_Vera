package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
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

// SesionEmitida es el par de tokens y el usuario que devuelve el login.
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

// ServicioDeSesion rota el refresh y publica la revocación en la denylist.
type ServicioDeSesion struct {
	pool     abridorDeTransaccionesAuth
	sesiones *GestorSesiones
	tokens   *TokensDeAcceso
	denylist RevocadorDeSesiones
}

// NuevoServicioDeSesion exige el pool, el gestor, el firmador y la denylist.
func NuevoServicioDeSesion(pool *pgxpool.Pool, sesiones *GestorSesiones, tokens *TokensDeAcceso, denylist RevocadorDeSesiones) (*ServicioDeSesion, error) {
	if pool == nil || sesiones == nil || tokens == nil {
		return nil, errors.New("auth: el servicio de sesión necesita pool, sesiones y firmador")
	}
	return &ServicioDeSesion{pool: poolDeAuth{pool: pool}, sesiones: sesiones, tokens: tokens, denylist: denylist}, nil
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

// publicarSid publica la familia revocada en la denylist con el ttl del access.
func (s *ServicioDeSesion) publicarSid(ctx context.Context, sesionID uuid.UUID) error {
	if s.denylist == nil {
		return nil
	}
	return s.denylist.Revocar(ctx, sesionID, duracionAccess)
}
