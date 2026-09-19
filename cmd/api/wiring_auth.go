package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/correosim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/otpsim"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/whatsapp"
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

// contadoresDeAuth construye el adaptador Redis del puerto de contadores.
func contadoresDeAuth(cliente *redis.Client) (auth.Contadores, error) {
	return contadores.NuevoRedis(cliente)
}

// opcionLimpiezaDeAuth registra el cierre de pendientes vencidas en la cola auth de River.
func opcionLimpiezaDeAuth(pool *pgxpool.Pool, cfg *Config) jobs.OpcionDeAuth {
	gestor := auth.NuevoGestorDeEstados(auth.NuevoRegistradorEventos(pool))
	return jobs.ConTrabajoDeAuth(
		auth.NuevoCerrarPendientesWorker(pool, gestor, cfg.Auth.VentanaPendiente),
		auth.TrabajoPeriodicoDeLimpieza(cfg.Auth.LimpiezaInterval),
	)
}

// opcionOutboxDeAuth registra el barrido del outbox de avisos en la cola auth de River.
func opcionOutboxDeAuth(pool *pgxpool.Pool, canal auth.CanalOTP, cfg *Config) jobs.OpcionDeAuth {
	notificador := auth.NuevoNotificadorDeAvisos(correosim.Nuevo(), canal)
	despachador := auth.NuevoDespachadorDeAvisos(pool, notificador, auth.PoliticaReintento{
		Base:   cfg.Auth.OutboxBackoffBase,
		Factor: cfg.Auth.OutboxBackoffFactor,
		Tope:   cfg.Auth.OutboxBackoffMax,
	})
	return jobs.ConTrabajoDeAuth(
		auth.NuevoDespacharAvisosWorker(despachador),
		auth.TrabajoPeriodicoDeOutbox(cfg.Auth.OutboxInterval),
	)
}

// canalOTP elige el canal de OTP por OTP_CANAL; el adaptador de demo queda fuera del harness.
func canalOTP(cfg *Config) (auth.CanalOTP, error) {
	switch cfg.Auth.OTPCanal {
	case "simulador":
		simulador, err := otpsim.Nuevo(cfg.Auth.OTPSimuladorCodigoFijo)
		if err != nil {
			return nil, err
		}
		return simulador, nil
	case "cloudapi":
		return whatsapp.Nuevo(whatsapp.BaseGraphAPI, cfg.Auth.WhatsAppToken, cfg.Auth.WhatsAppPhoneID, cfg.Auth.WhatsAppTemplate, nil), nil
	case "demo":
		return nil, errors.New("OTP_CANAL=demo: el adaptador de WhatsApp de demostracion no esta implementado")
	default:
		return nil, fmt.Errorf("OTP_CANAL desconocido %q", cfg.Auth.OTPCanal)
	}
}
