//go:build integration

package contadores_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/redis/go-redis/v9"
)

// abrirRedisDelCarril conecta con el Redis real del carril; sin REDIS_URL el test es un hallazgo.
func abrirRedisDelCarril(tb testing.TB) *redis.Client {
	tb.Helper()
	raw := os.Getenv("REDIS_URL")
	if raw == "" {
		tb.Fatalf("REDIS_URL es obligatoria bajo el tag integration")
	}
	cliente, err := platformredis.NuevoCliente(raw)
	if err != nil {
		tb.Fatalf("construir cliente Redis: %v", err)
	}
	tb.Cleanup(func() { _ = cliente.Close() })
	if err := cliente.Ping(context.Background()).Err(); err != nil {
		tb.Fatalf("ping al Redis del carril: %v", err)
	}
	return cliente
}

// TestContadoresRedisIncrementoConcurrente exige 50 incrementos exactos y TTL positivo sobre Redis real.
func TestContadoresRedisIncrementoConcurrente(t *testing.T) {
	cliente := abrirRedisDelCarril(t)
	r, err := contadores.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("NuevoRedis: %v", err)
	}

	const goroutines = 50
	clave := "login:cuenta:concurrencia:" + time.Now().Format("150405.000000000")

	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for range goroutines {
		wg.Go(func() {
			if _, err := r.Incrementar(t.Context(), clave, time.Hour); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Incrementar concurrente: %v", err)
	}

	valor, err := cliente.Get(t.Context(), clave).Int64()
	if err != nil {
		t.Fatalf("GET de la clave: %v", err)
	}
	if valor != goroutines {
		t.Fatalf("valor final = %d, quiero %d", valor, goroutines)
	}
	ttl, err := cliente.TTL(t.Context(), clave).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("TTL = %s, quiero positivo (nunca -1)", ttl)
	}
}

// TestContadoresRedisFailClosedEnMenosDeUnSegundo mide el fail closed contra un puerto cerrado.
func TestContadoresRedisFailClosedEnMenosDeUnSegundo(t *testing.T) {
	cliente, err := platformredis.NuevoCliente("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("construir cliente: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	r, err := contadores.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("NuevoRedis: %v", err)
	}

	operaciones := map[string]func() error{
		"incrementar": func() error {
			_, err := r.Incrementar(t.Context(), "login:cuenta:failclosed", time.Minute)
			return err
		},
		"bloquear": func() error {
			return r.Bloquear(t.Context(), "login:cuenta:failclosed", time.Minute)
		},
		"bloqueada": func() error {
			_, err := r.Bloqueada(t.Context(), "login:cuenta:failclosed")
			return err
		},
	}
	for nombre, operacion := range operaciones {
		t.Run(nombre, func(t *testing.T) {
			inicio := time.Now()
			err := operacion()
			transcurrido := time.Since(inicio)
			if !errors.Is(err, auth.ErrAuthNoDisponible) {
				t.Fatalf("%s = %v, quiero ErrAuthNoDisponible", nombre, err)
			}
			if transcurrido >= time.Second {
				t.Errorf("%s tardó %v, quiero menos de 1s", nombre, transcurrido)
			}
			t.Logf("%s fail closed en %v", nombre, transcurrido)
		})
	}
}

// TestClaveDeCorreoNormalizadaNoContieneElCorreo prueba el HMAC del correo normalizado sin filtrarlo.
func TestClaveDeCorreoNormalizadaNoContieneElCorreo(t *testing.T) {
	claves, err := auth.NuevasClaves(bytes.Repeat([]byte{0x55}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	a := claves.ClaveDeCorreo(strings.ToLower("Persona@Correo.com"))
	b := claves.ClaveDeCorreo(strings.ToLower("persona@correo.com"))
	if a != b {
		t.Fatalf("claves distintas para el mismo correo normalizado: %q != %q", a, b)
	}
	if strings.Contains(strings.ToLower(a), "persona") || strings.Contains(strings.ToLower(a), "correo") {
		t.Fatalf("la clave contiene el correo: %q", a)
	}
}
