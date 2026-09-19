package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ContextoDeParte es un contexto disponible para la parte, personal o de empresa representada.
type ContextoDeParte struct {
	ID     string
	Tipo   string
	Nombre string
	NIT    string
	Rol    string
}

// ContextoActivo es el access emitido al cambiar el contexto de la sesión.
type ContextoActivo struct {
	AccessToken string
	Contexto    ContextoDeParte
}

// ListarDispositivos devuelve las sesiones activas de la parte.
func (s *ServicioDeSesion) ListarDispositivos(ctx context.Context, parteID uuid.UUID) ([]Sesion, error) {
	return s.sesiones.ListarActivas(ctx, parteID)
}

// CerrarSesionPorRefresh revoca la familia del refresh presentado y publica su sid.
func (s *ServicioDeSesion) CerrarSesionPorRefresh(ctx context.Context, parteID uuid.UUID, refreshToken string) error {
	var familiaID uuid.UUID
	_, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		familia, err := familiaVigenteDeParte(ctx, q, parteID, refreshToken)
		if err != nil {
			return struct{}{}, err
		}
		familiaID = familia
		if _, err := q.RevocarFamiliaDeParte(ctx, repositorio.RevocarFamiliaDeParteParams{
			FamiliaID: pgtype.UUID{Bytes: familia, Valid: true},
			ParteID:   pgtype.UUID{Bytes: parteID, Valid: true},
		}); err != nil {
			return struct{}{}, fmt.Errorf("auth: revocar la familia de la parte: %w", err)
		}
		return struct{}{}, s.sesiones.publicarRevocacion(ctx, q, parteID, familiaID, "")
	})
	if err != nil {
		return err
	}
	return s.publicarSid(ctx, familiaID)
}

// familiaVigenteDeParte resuelve el refresh y exige que esté vigente y sea de la parte.
func familiaVigenteDeParte(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID, refreshToken string) (uuid.UUID, error) {
	fila, err := q.SesionPorRefreshHash(ctx, hashDeRefresh(refreshToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrRefreshInvalido
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("auth: buscar la sesión del refresh: %w", err)
	}
	vencida := !fila.VenceEn.Valid || !fila.VenceEn.Time.After(time.Now().UTC())
	if uuidDePgtype(fila.ParteID) != parteID || fila.RevocadaEn.Valid || vencida {
		return uuid.Nil, ErrRefreshInvalido
	}
	return uuidDePgtype(fila.FamiliaID), nil
}

// CerrarTodasLasSesiones publica el sid de cada familia vigente y revoca todas las sesiones de la parte.
func (s *ServicioDeSesion) CerrarTodasLasSesiones(ctx context.Context, parteID uuid.UUID) error {
	sesiones, err := s.sesiones.ListarActivas(ctx, parteID)
	if err != nil {
		return err
	}
	for _, sesion := range sesiones {
		if err := s.publicarSid(ctx, sesion.FamiliaID); err != nil {
			return err
		}
	}
	return s.sesiones.RevocarTodas(ctx, parteID)
}

// RevocarDispositivo revoca la familia si pertenece a la parte y publica su sid.
func (s *ServicioDeSesion) RevocarDispositivo(ctx context.Context, parteID, familiaID uuid.UUID) error {
	if err := s.sesiones.RevocarFamiliaDeParte(ctx, parteID, familiaID); err != nil {
		return err
	}
	return s.publicarSid(ctx, familiaID)
}

// ListarContextos devuelve el contexto personal y una entrada por empresa con rol vigente.
func (s *ServicioDeSesion) ListarContextos(ctx context.Context, parteID uuid.UUID) ([]ContextoDeParte, error) {
	return enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) ([]ContextoDeParte, error) {
		personal, err := q.UsuarioDeParte(ctx, pgtype.UUID{Bytes: parteID, Valid: true})
		if err != nil {
			return nil, fmt.Errorf("auth: leer la parte: %w", err)
		}
		contextos := make([]ContextoDeParte, 0, 1)
		contextos = append(contextos, ContextoDeParte{ID: ContextoUsuario, Tipo: ContextoUsuario, Nombre: nombreDeParte(personal)})
		roles, err := s.sesiones.rolesVigentesEn(ctx, q, parteID)
		if err != nil {
			return nil, err
		}
		for _, rol := range roles {
			empresa, err := q.EmpresaDeParte(ctx, pgtype.UUID{Bytes: rol.RepresentadaID, Valid: true})
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("auth: leer la empresa: %w", err)
			}
			contextos = append(contextos, ContextoDeParte{
				ID:     ContextoDeEmpresa(rol.RepresentadaID),
				Tipo:   ContextoEmpresa,
				Nombre: empresa.RazonSocial,
				NIT:    empresa.Nit,
				Rol:    rol.Rol,
			})
		}
		return contextos, nil
	})
}

// ActivarContexto valida el contexto, actualiza el de la sesión y emite un access con el rol resuelto.
func (s *ServicioDeSesion) ActivarContexto(ctx context.Context, parteID, familiaID uuid.UUID, contextoID string) (ContextoActivo, error) {
	var contexto ContextoDeParte
	_, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		resuelto, err := s.resolverContextoDeParte(ctx, q, parteID, contextoID)
		if err != nil {
			return struct{}{}, err
		}
		filas, err := q.ActualizarContextoDeFamilia(ctx, repositorio.ActualizarContextoDeFamiliaParams{
			FamiliaID:      pgtype.UUID{Bytes: familiaID, Valid: true},
			ContextoActivo: resuelto.ID,
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: actualizar el contexto activo: %w", err)
		}
		if filas == 0 {
			return struct{}{}, ErrSesionRevocada
		}
		contexto = resuelto
		return struct{}{}, nil
	})
	if err != nil {
		return ContextoActivo{}, err
	}
	access, err := s.tokens.Firmar(ClaimsAccess{
		ParteID:  parteID,
		SesionID: familiaID,
		Contexto: contexto.ID,
		Rol:      contexto.Rol,
	})
	if err != nil {
		return ContextoActivo{}, err
	}
	return ContextoActivo{AccessToken: access, Contexto: contexto}, nil
}

// RevocarFamiliaDeParte revoca la familia solo si pertenece a la parte y deja el evento.
func (g *GestorSesiones) RevocarFamiliaDeParte(ctx context.Context, parteID, familiaID uuid.UUID) error {
	_, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		filas, err := q.RevocarFamiliaDeParte(ctx, repositorio.RevocarFamiliaDeParteParams{
			FamiliaID: pgtype.UUID{Bytes: familiaID, Valid: true},
			ParteID:   pgtype.UUID{Bytes: parteID, Valid: true},
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: revocar la familia de la parte: %w", err)
		}
		if filas == 0 {
			return struct{}{}, ErrSesionNoEncontrada
		}
		return struct{}{}, g.publicarRevocacion(ctx, q, parteID, familiaID, "")
	})
	return err
}

// resolverContextoDeParte valida el contexto pedido contra los roles vigentes y devuelve su nombre y rol.
func (s *ServicioDeSesion) resolverContextoDeParte(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID, contextoID string) (ContextoDeParte, error) {
	if contextoID == "" || contextoID == ContextoUsuario {
		personal, err := q.UsuarioDeParte(ctx, pgtype.UUID{Bytes: parteID, Valid: true})
		if err != nil {
			return ContextoDeParte{}, fmt.Errorf("auth: leer la parte: %w", err)
		}
		return ContextoDeParte{ID: ContextoUsuario, Tipo: ContextoUsuario, Nombre: nombreDeParte(personal)}, nil
	}
	empresaID, esEmpresa := EmpresaDeContexto(contextoID)
	if !esEmpresa {
		return ContextoDeParte{}, ErrContextoInvalido
	}
	roles, err := s.sesiones.rolesVigentesEn(ctx, q, parteID)
	if err != nil {
		return ContextoDeParte{}, err
	}
	for _, rol := range roles {
		if rol.RepresentadaID != empresaID {
			continue
		}
		empresa, err := q.EmpresaDeParte(ctx, pgtype.UUID{Bytes: empresaID, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return ContextoDeParte{}, ErrContextoInvalido
		}
		if err != nil {
			return ContextoDeParte{}, fmt.Errorf("auth: leer la empresa: %w", err)
		}
		return ContextoDeParte{ID: ContextoDeEmpresa(empresaID), Tipo: ContextoEmpresa, Nombre: empresa.RazonSocial, NIT: empresa.Nit, Rol: rol.Rol}, nil
	}
	return ContextoDeParte{}, ErrContextoInvalido
}

// nombreDeParte elige el nombre visible de la parte: persona, empresa o correo.
func nombreDeParte(fila repositorio.UsuarioDeParteRow) string {
	switch {
	case fila.NombreCompleto.Valid && fila.NombreCompleto.String != "":
		return fila.NombreCompleto.String
	case fila.RazonSocial.Valid && fila.RazonSocial.String != "":
		return fila.RazonSocial.String
	default:
		return fila.Correo.String
	}
}
