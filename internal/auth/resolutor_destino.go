package auth

import (
	"context"
	"errors"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ResolutorDestino resuelve la llave de destino de un envío a un usuario de la billetera.
type ResolutorDestino struct {
	pool         abridorDeTransaccionesAuth
	enfriamiento time.Duration
}

// errEnfriamientoNoPositivo reporta la construcción del resolutor sin una ventana de enfriamiento positiva.
var errEnfriamientoNoPositivo = errors.New("auth: NuevoResolutorDestino necesita un enfriamiento positivo")

// NuevoResolutorDestino construye el resolutor sobre el bloque de auth y la ventana de enfriamiento.
func NuevoResolutorDestino(pool *pgxpool.Pool, enfriamiento time.Duration) *ResolutorDestino {
	if enfriamiento <= 0 {
		panic(errEnfriamientoNoPositivo)
	}
	return &ResolutorDestino{pool: poolDeAuth{pool: pool}, enfriamiento: enfriamiento}
}

// UsuarioIDDeDestino acepta el UUID de una parte no cerrada o el celular verificado de una persona activa.
func (r *ResolutorDestino) UsuarioIDDeDestino(ctx context.Context, llave string) (string, bool, error) {
	if id, err := uuid.Parse(llave); err == nil {
		return r.usuarioPorID(ctx, id)
	}
	return r.usuarioPorCelular(ctx, llave)
}

// usuarioPorID resuelve cualquier parte no cerrada por su uuid, sin aplicar el enfriamiento.
func (r *ResolutorDestino) usuarioPorID(ctx context.Context, id uuid.UUID) (string, bool, error) {
	fila, err := enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (pgtype.UUID, error) {
		return q.ResolverDestinoPorID(ctx, pgtype.UUID{Bytes: id, Valid: true})
	})
	return destinoODesconocido(fila, err)
}

// usuarioPorCelular resuelve el celular verificado de una persona activa que no esté en enfriamiento.
func (r *ResolutorDestino) usuarioPorCelular(ctx context.Context, celular string) (string, bool, error) {
	fila, err := enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (pgtype.UUID, error) {
		return q.ResolverDestinoPorCelular(ctx, repositorio.ResolverDestinoPorCelularParams{
			Celular:              celular,
			EnfriamientoSegundos: r.enfriamiento.Seconds(),
		})
	})
	return destinoODesconocido(fila, err)
}

// destinoODesconocido traduce el no encontrado a existe falso y formatea el uuid resuelto.
func destinoODesconocido(fila pgtype.UUID, err error) (string, bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return uuid.UUID(fila.Bytes).String(), true, nil
}
