//go:build integration

package auth

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/redis/go-redis/v9"
)

// abrirRedisDePrueba conecta con el Redis del carril; sin REDIS_URL el test es un hallazgo, no un skip.
func abrirRedisDePrueba(tb testing.TB) *redis.Client {
	tb.Helper()

	raw := os.Getenv("REDIS_URL")
	if raw == "" {
		tb.Fatalf("REDIS_URL es obligatoria bajo el tag integration: sin Redis real el test es un hallazgo, no un skip")
	}
	cliente, err := platformredis.NuevoCliente(raw)
	if err != nil {
		tb.Fatalf("construir cliente Redis: %v", err)
	}
	tb.Cleanup(func() {
		if esRedisDelSandbox(raw) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := cliente.FlushDB(ctx).Err(); err != nil {
				tb.Errorf("FLUSHDB del Redis de pruebas: %v", err)
			}
		}
		_ = cliente.Close()
	})
	if err := cliente.Ping(context.Background()).Err(); err != nil {
		tb.Fatalf("ping al Redis del carril: %v", err)
	}
	return cliente
}

// esRedisDelSandbox deja vaciar solo el Redis efímero del carril (127.0.0.1:1638x), nunca uno ajeno.
func esRedisDelSandbox(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != "127.0.0.1" {
		return false
	}
	puerto := u.Port()
	return len(puerto) == 5 && strings.HasPrefix(puerto, "1638")
}

func TestRedisDelCarrilHacePingYSetGet(t *testing.T) {
	cliente := abrirRedisDePrueba(t)
	ctx := t.Context()

	if err := cliente.Set(ctx, "fase0-3:ping", "pong", time.Minute).Err(); err != nil {
		t.Fatalf("SET: %v", err)
	}
	got, err := cliente.Get(ctx, "fase0-3:ping").Result()
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if got != "pong" {
		t.Fatalf("GET = %q, quiero %q", got, "pong")
	}
}
