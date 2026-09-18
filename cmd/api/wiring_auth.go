package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// clienteRedis construye el cliente de auth y avisa si no responde, sin detener el arranque.
func clienteRedis(cfg *Config) *redis.Client {
	cliente, err := platformredis.NuevoCliente(cfg.Auth.RedisURL)
	if err != nil {
		slog.Error("no se pudo construir el cliente Redis", "err", err)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := cliente.Ping(ctx).Err(); err != nil {
		slog.Warn("Redis no responde al arrancar, auth fallará cerrado", "err", err)
	}
	return cliente
}

// opcionLimpiezaDeAuth registra el cierre de pendientes vencidas en la cola auth de River.
func opcionLimpiezaDeAuth(pool *pgxpool.Pool, cfg *Config) jobs.OpcionDeAuth {
	gestor := auth.NuevoGestorDeEstados(auth.NuevoRegistradorEventos(pool))
	return jobs.ConTrabajoDeAuth(
		auth.NuevoCerrarPendientesWorker(pool, gestor, cfg.Auth.VentanaPendiente),
		auth.TrabajoPeriodicoDeLimpieza(cfg.Auth.LimpiezaInterval),
	)
}
