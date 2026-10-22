package auth

import (
	"context"
	"errors"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// NombresDeContrapartes resuelve referencias internas de una página autorizada del historial.
// No es una búsqueda pública ni resuelve teléfonos que pudieron cambiar de titular.
func (r *ResolutorDestino) NombresDeContrapartes(ctx context.Context, referencias []string) (map[string]string, error) {
	if len(referencias) > 20 {
		return nil, errors.New("auth: consulta interna de contrapartes demasiado grande")
	} // entrada interna acotada
	ids := make([]pgtype.UUID, 0, len(referencias))
	for _, ref := range referencias {
		if id, err := uuid.Parse(ref); err == nil && id != uuid.Nil {
			ids = append(ids, pgtype.UUID{Bytes: id, Valid: true})
		}
	}
	resultado := make(map[string]string)
	if len(ids) == 0 {
		return resultado, nil
	}
	filas, err := enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) ([]repositorio.NombresDeContrapartesRow, error) {
		return q.NombresDeContrapartes(ctx, ids)
	})
	if err != nil {
		return nil, err
	}
	for _, f := range filas {
		resultado[uuid.UUID(f.ID.Bytes).String()] = f.Nombre
	}
	return resultado, nil
}
