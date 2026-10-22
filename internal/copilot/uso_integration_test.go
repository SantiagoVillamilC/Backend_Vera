//go:build integration

package copilot

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func redisUsoPrueba(t *testing.T) (*redis.Client, *PresupuestoRedis) {
	t.Helper()
	raw := os.Getenv("REDIS_URL")
	if raw == "" {
		t.Fatal("REDIS_URL obligatoria bajo integration")
	}
	opts, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	opts.MaxRetries = -1
	opts.DialTimeout = 100 * time.Millisecond
	opts.ReadTimeout = 100 * time.Millisecond
	opts.WriteTimeout = 100 * time.Millisecond
	cliente := redis.NewClient(opts)
	t.Cleanup(func() { _ = cliente.Close() })
	if err := cliente.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	presupuesto, err := NuevoPresupuestoRedis(cliente)
	if err != nil {
		t.Fatal(err)
	}
	return cliente, presupuesto
}

func TestUsoRedisReservaAtomicaYExpiracion(t *testing.T) {
	cliente, p := redisUsoPrueba(t)
	clave := "vera:prueba:" + uuid.NewString()
	t.Cleanup(func() { _ = cliente.Del(context.Background(), clave).Err() })
	fin := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	var aceptadas atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			ok, err := p.Reservar(t.Context(), clave, 1000, 25, fin)
			if err != nil {
				t.Error(err)
			}
			if ok {
				aceptadas.Add(1)
			}
		})
	}
	wg.Wait()
	n, err := p.Leer(t.Context(), clave)
	if err != nil || n != 1000 || aceptadas.Load() != 40 {
		t.Fatalf("reserva concurrente: %d %d %v", n, aceptadas.Load(), err)
	}
	if expira, err := cliente.PExpireTime(t.Context(), clave).Result(); err != nil || expira.Milliseconds() != fin.UnixMilli() {
		t.Fatalf("expiración: %d != %d, %v", expira.Milliseconds(), fin.UnixMilli(), err)
	}
	if err := p.Conciliar(t.Context(), clave, -15); err != nil {
		t.Fatal(err)
	}
	if n, err := p.Leer(t.Context(), clave); err != nil || n != 985 {
		t.Fatalf("conciliación: %d %v", n, err)
	}
	if err := cliente.Del(t.Context(), clave).Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Conciliar(t.Context(), clave, 10); err != nil {
		t.Fatal(err)
	}
	if n, err := cliente.Exists(t.Context(), clave).Result(); err != nil || n != 0 {
		t.Fatalf("se revivió el día expirado: %d %v", n, err)
	}
}

func TestUsoRedisCanalesPresupuestoYCaida(t *testing.T) {
	cliente, p := redisUsoPrueba(t)
	cont, err := contadores.NuevoRedis(cliente)
	if err != nil {
		t.Fatal(err)
	}
	claves, err := auth.NuevasClaves([]byte(strings.Repeat("x", 32)))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NuevoControlUso(cont, claves, p, LimitesUso{TextoHora: 2, TextoDia: 3, VozHora: 1, TokensDiarios: 100, ReservaTokens: 20})
	if err != nil {
		t.Fatal(err)
	}
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString()})
	for range 2 {
		if err := c.Admitir(ctx, CanalTexto); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Admitir(ctx, CanalTexto); !errors.Is(err, ErrLimiteUso) {
		t.Fatal(err)
	}
	if err := c.Admitir(ctx, CanalVoz); err != nil {
		t.Fatal(err)
	}
	proveedor := &proveedorTokens{respuesta: RespuestaIA{TokensEntrada: 60, TokensSalida: 40}}
	protegido := c.ProtegerProveedor(proveedor)
	if _, err := protegido.Generar(ctx, SolicitudIA{}); err != nil {
		t.Fatal(err)
	}
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrLimiteUso) {
		t.Fatal(err)
	}
	for _, canal := range []CanalAsistente{CanalTexto, CanalVoz} {
		if err := c.Admitir(ctx, canal); !errors.Is(err, ErrLimiteUso) {
			t.Fatal(err)
		}
	}
	if proveedor.llamadas != 1 {
		t.Fatal("se ignoró el consumo real")
	}
	if err := cliente.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Admitir(ctx, CanalVoz); !errors.Is(err, ErrNoDisponibleIA) {
		t.Fatal(err)
	}
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrNoDisponibleIA) {
		t.Fatal(err)
	}
	if proveedor.llamadas != 1 {
		t.Fatal("el proveedor recibió datos con Redis caído")
	}
}
