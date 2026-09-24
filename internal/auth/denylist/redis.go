// Package denylist implementa el puerto auth.RevocadorDeSesiones sobre Redis.
package denylist

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// prefijoDenylist separa las claves de las sesiones revocadas.
const prefijoDenylist = "denylist:sid:"

// Redis implementa auth.RevocadorDeSesiones con el cliente fail closed de fase0-3.
type Redis struct {
	cliente *redis.Client
}

// NuevoRedis envuelve el cliente de fase0-3 y rechaza un cliente nil.
func NuevoRedis(cliente *redis.Client) (*Redis, error) {
	if cliente == nil {
		return nil, errors.New("denylist: el cliente Redis es nil")
	}
	return &Redis{cliente: cliente}, nil
}

// Revocar marca el sid como revocado durante el ttl del access restante.
func (r *Redis) Revocar(ctx context.Context, sesionID uuid.UUID, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("denylist: el ttl debe ser positivo, tiene %s", ttl)
	}
	if err := r.cliente.Set(ctx, prefijoDenylist+sesionID.String(), 1, ttl).Err(); err != nil {
		return auth.ConFailClosed(err)
	}
	return nil
}

// Revocado informa si el sid está en la denylist y cierra ante un Redis que falla.
func (r *Redis) Revocado(ctx context.Context, sesionID uuid.UUID) (bool, error) {
	n, err := r.cliente.Exists(ctx, prefijoDenylist+sesionID.String()).Result()
	if err != nil {
		return false, auth.ConFailClosed(err)
	}
	return n > 0, nil
}

var _ auth.RevocadorDeSesiones = (*Redis)(nil)
