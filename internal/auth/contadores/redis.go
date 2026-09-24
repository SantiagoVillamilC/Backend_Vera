// Package contadores implementa el puerto auth.Contadores sobre Redis y en memoria.
package contadores

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/redis/go-redis/v9"
)

// scriptIncremento incrementa y solo fija el TTL la primera vez, en un solo comando atomico.
const scriptIncremento = `local v = redis.call('INCR', KEYS[1])
if v == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return v`

// prefijoBloqueo separa las claves de bloqueo de las claves de conteo.
const prefijoBloqueo = "bloqueo:"

// Redis implementa auth.Contadores con el cliente fail closed de fase0-3.
type Redis struct {
	cliente *redis.Client
}

// NuevoRedis envuelve el cliente de fase0-3 y rechaza un cliente nil.
func NuevoRedis(cliente *redis.Client) (*Redis, error) {
	if cliente == nil {
		return nil, errors.New("contadores: el cliente Redis es nil")
	}
	return &Redis{cliente: cliente}, nil
}

// Incrementar llama al script atomico y envuelve cualquier error en ErrAuthNoDisponible.
func (r *Redis) Incrementar(ctx context.Context, clave string, ventana time.Duration) (int64, error) {
	if ventana <= 0 {
		return 0, fmt.Errorf("contadores: la ventana debe ser positiva, tiene %s", ventana)
	}
	milisegundos := ventana.Milliseconds()
	if milisegundos < 1 {
		milisegundos = 1
	}
	valor, err := r.cliente.Eval(ctx, scriptIncremento, []string{clave}, milisegundos).Int64()
	if err != nil {
		return 0, auth.ConFailClosed(err)
	}
	return valor, nil
}

// Bloquear marca la clave como bloqueada durante hasta y envuelve cualquier error en ErrAuthNoDisponible.
func (r *Redis) Bloquear(ctx context.Context, clave string, hasta time.Duration) error {
	if hasta <= 0 {
		return fmt.Errorf("contadores: el bloqueo debe ser positivo, tiene %s", hasta)
	}
	if err := r.cliente.Set(ctx, prefijoBloqueo+clave, 1, hasta).Err(); err != nil {
		return auth.ConFailClosed(err)
	}
	return nil
}

// Bloqueada informa si la clave sigue bloqueada y envuelve cualquier error en ErrAuthNoDisponible.
func (r *Redis) Bloqueada(ctx context.Context, clave string) (bool, error) {
	n, err := r.cliente.Exists(ctx, prefijoBloqueo+clave).Result()
	if err != nil {
		return false, auth.ConFailClosed(err)
	}
	return n > 0, nil
}

var _ auth.Contadores = (*Redis)(nil)
