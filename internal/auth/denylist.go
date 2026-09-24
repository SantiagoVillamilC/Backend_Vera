package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrSesionRevocada es el sid que la denylist marca como revocado; responde 401.
var ErrSesionRevocada = errors.New("auth: la sesión fue revocada")

// RevocadorDeSesiones publica y consulta los sid revocados con fail closed.
type RevocadorDeSesiones interface {
	Revocar(ctx context.Context, sesionID uuid.UUID, ttl time.Duration) error
	Revocado(ctx context.Context, sesionID uuid.UUID) (bool, error)
}
