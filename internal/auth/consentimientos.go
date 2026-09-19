package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tipos de consentimiento que la columna acepta, en el mismo vocabulario del contrato.
const (
	TipoConsentimientoTratamiento = "tratamiento_datos"
	TipoConsentimientoWhatsApp    = "whatsapp_optin"
	TipoConsentimientoBiometricos = "biometricos"
)

// LectorOptInDeConsentimientos implementa el puerto LectorOptIn de A2-06.1 sobre auth.consentimientos.
type LectorOptInDeConsentimientos struct{ abridor abridorDeTransaccionesAuth }

// NuevoLectorOptInDeConsentimientos construye el lector sobre el pool de la API.
func NuevoLectorOptInDeConsentimientos(pool *pgxpool.Pool) *LectorOptInDeConsentimientos {
	return &LectorOptInDeConsentimientos{abridor: poolDeAuth{pool: pool}}
}

var _ LectorOptIn = (*LectorOptInDeConsentimientos)(nil)

// OptInVigente responde con el ultimo consentimiento de WhatsApp de la parte.
func (l *LectorOptInDeConsentimientos) OptInVigente(ctx context.Context, parteID uuid.UUID) (bool, error) {
	aceptado, err := enTransaccion(ctx, l.abridor,
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (bool, error) {
			vigente, err := q.OptInVigenteDeParte(ctx, uuidOPgtype(parteID))
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return vigente, err
		})
	if err != nil {
		return false, fmt.Errorf("auth: consultar el opt-in de la parte: %w", err)
	}
	return aceptado, nil
}
