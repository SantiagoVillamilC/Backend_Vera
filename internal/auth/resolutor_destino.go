package auth

import (
	"context"
	"errors"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ResolutorDestino resuelve la llave de destino de un envío a un usuario de la billetera.
type ResolutorDestino struct{ q repositorio.Querier }

// NuevoResolutorDestino construye el resolutor sobre el repositorio de auth.
func NuevoResolutorDestino(q repositorio.Querier) *ResolutorDestino {
	return &ResolutorDestino{q: q}
}

// UsuarioIDDeDestino acepta el UUID o el correo del destinatario y reporta si existe.
func (r *ResolutorDestino) UsuarioIDDeDestino(ctx context.Context, llave string) (string, bool, error) {
	if id, err := uuid.Parse(llave); err == nil {
		return r.usuarioPorID(ctx, id)
	}
	fila, err := r.q.GetUserByEmail(ctx, llave)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return uuid.UUID(fila.ID.Bytes).String(), true, nil
}

// usuarioPorID busca por UUID y distingue el no encontrado del error de lectura.
func (r *ResolutorDestino) usuarioPorID(ctx context.Context, id uuid.UUID) (string, bool, error) {
	fila, err := r.q.GetUserByID(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return uuid.UUID(fila.ID.Bytes).String(), true, nil
}
