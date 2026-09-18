package auth

import (
	"errors"
	"testing"
	"time"

	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
)

// TestConFailClosedTraduceErrorDeRedis comprueba que un Redis inalcanzable corta en menos de un segundo.
func TestConFailClosedTraduceErrorDeRedis(t *testing.T) {
	cliente, err := platformredis.NuevoCliente("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("construir cliente: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })

	if got := cliente.Options().MaxRetries; got != 0 {
		t.Fatalf("Options().MaxRetries = %d, quiero 0: go-redis normaliza -1 (sin reintentos) a 0", got)
	}

	inicio := time.Now()
	errPing := cliente.Ping(t.Context()).Err()
	if errPing == nil {
		t.Fatal("Ping contra 127.0.0.1:1 no devolvió error")
	}
	if transcurrido := time.Since(inicio); transcurrido >= time.Second {
		t.Errorf("Ping tardó %v, quiero menos de 1s", transcurrido)
	}
	if mapeado := ConFailClosed(errPing); !errors.Is(mapeado, ErrAuthNoDisponible) {
		t.Fatalf("ConFailClosed(%v) no envuelve ErrAuthNoDisponible", errPing)
	}
}
