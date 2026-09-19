package kyc

import (
	"context"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LectorNivelKYCDeAuth implementa nucleo.LectorNivelKYC leyendo auth.partes con rol_auth.
type LectorNivelKYCDeAuth struct{ pool *pgxpool.Pool }

// NuevoLectorNivelKYC construye el lector real sobre el pool.
func NuevoLectorNivelKYC(pool *pgxpool.Pool) LectorNivelKYCDeAuth {
	return LectorNivelKYCDeAuth{pool: pool}
}

// NivelKYC devuelve el nivel del núcleo: persona bajo monto, empresa KYC pleno requerido, resto falla cerrado.
func (l LectorNivelKYCDeAuth) NivelKYC(ctx context.Context, usuarioID string) (string, error) {
	parteID, err := uuid.Parse(usuarioID)
	if err != nil {
		return "", fmt.Errorf("kyc: el usuario %q no es uuid: %w", usuarioID, err)
	}
	return auth.EnTransaccion(ctx, l.pool, func(ctx context.Context, q *repositorio.Queries) (string, error) {
		tipo, err := q.TipoDeParte(ctx, pgtype.UUID{Bytes: parteID, Valid: true})
		if err != nil {
			return "", fmt.Errorf("kyc: leer el tipo de la parte %s: %w", usuarioID, err)
		}
		return nivelDeTipo(tipo)
	})
}

// nivelDeTipo traduce el tipo al nivel del núcleo: devolver pleno sería fail open (A11).
func nivelDeTipo(tipo string) (string, error) {
	switch tipo {
	case TipoPersona:
		return nucleo.NivelKYCDepositoBajoMonto, nil
	case TipoEmpresa:
		return "", auth.ErrKYCPlenoRequerido
	default:
		return "", fmt.Errorf("%w: %q", ErrTipoDeParteDesconocido, tipo)
	}
}

var _ nucleo.LectorNivelKYC = LectorNivelKYCDeAuth{}
