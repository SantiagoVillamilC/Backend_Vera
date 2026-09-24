//go:build integration

package auth

import (
	"context"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SembrarParteDePrueba siembra la parte y la credencial sin_login del usuario de prueba.
func SembrarParteDePrueba(tb testing.TB, pool *pgxpool.Pool) string {
	tb.Helper()
	return SembrarParteDePruebaConID(tb, pool, UsuarioDePruebaID)
}

// SembrarParteDePruebaConID siembra de forma idempotente una parte y su credencial sin_login con el id pedido.
func SembrarParteDePruebaConID(tb testing.TB, pool *pgxpool.Pool, id string) string {
	tb.Helper()
	parteID, err := uuid.Parse(id)
	if err != nil {
		tb.Fatalf("id de prueba %q no es uuid: %v", id, err)
	}
	celular := "+0" + parteID.String()

	_, err = enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if err := q.InsertarParteDePrueba(ctx, pgtype.UUID{Bytes: parteID, Valid: true}); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, q.InsertarCredencialDePrueba(ctx, repositorio.InsertarCredencialDePruebaParams{
				ID:      pgtype.UUID{Bytes: uuid.New(), Valid: true},
				ParteID: pgtype.UUID{Bytes: parteID, Valid: true},
				Correo:  id + "@centinela.test",
				Celular: celular,
			})
		})
	if err != nil {
		tb.Fatalf("sembrar la parte de prueba %s: %v", id, err)
	}
	return id
}
