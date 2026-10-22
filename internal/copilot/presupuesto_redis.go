package copilot

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const scriptReserva = `local reloj = redis.call('TIME')
if tonumber(ARGV[3]) <= tonumber(reloj[1])*1000 + math.floor(tonumber(reloj[2])/1000) then
return redis.error_reply('expired token budget') end
local actual = tonumber(redis.call('GET', KEYS[1]) or '0')
local limite, reserva = tonumber(ARGV[1]), tonumber(ARGV[2])
if actual < 0 then return redis.error_reply('invalid token budget') end
if actual + reserva > limite then return 0 end
redis.call('INCRBY', KEYS[1], reserva)
redis.call('PEXPIREAT', KEYS[1], ARGV[3])
return 1`

const scriptConciliacion = `if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
local actual = tonumber(redis.call('GET', KEYS[1]))
local delta = tonumber(ARGV[1])
if actual + delta < 0 then return redis.error_reply('invalid token reconciliation') end
return redis.call('INCRBY', KEYS[1], delta)`

// PresupuestoRedis conserva consumo más reservas con scripts atómicos y TTL diario.
type PresupuestoRedis struct{ cliente *redis.Client }

// NuevoPresupuestoRedis reutiliza el cliente de auth, sin otra conexión ni fallback local.
func NuevoPresupuestoRedis(cliente *redis.Client) (*PresupuestoRedis, error) {
	if cliente == nil {
		return nil, errors.New("asistente: Redis no configurado")
	}
	return &PresupuestoRedis{cliente: cliente}, nil
}

// Leer no crea claves de usuarios que todavía no han llamado al proveedor.
func (p *PresupuestoRedis) Leer(ctx context.Context, clave string) (int64, error) {
	valor, err := p.cliente.Get(ctx, clave).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return valor, err
}

// Reservar impide que llamadas concurrentes utilicen el mismo saldo de tokens.
func (p *PresupuestoRedis) Reservar(ctx context.Context, clave string, limite, reserva int64, fin time.Time) (bool, error) {
	if limite <= 0 || reserva <= 0 || reserva > limite {
		return false, ErrSolicitudIA
	}
	n, err := p.cliente.Eval(ctx, scriptReserva, []string{clave}, limite, reserva, fin.UnixMilli()).Int64()
	return n == 1, err
}

// Conciliar ajusta una sola reserva al consumo conocido, manteniendo su vencimiento.
func (p *PresupuestoRedis) Conciliar(ctx context.Context, clave string, delta int64) error {
	return p.cliente.Eval(ctx, scriptConciliacion, []string{clave}, delta).Err()
}

var _ PresupuestoTokens = (*PresupuestoRedis)(nil)
