package denylist_test

import (
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/denylist"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
)

func TestDenylistMarcaYConsultaElSid(t *testing.T) {
	mr := miniredis.RunT(t)
	cliente, err := platformredis.NuevoCliente("redis://" + mr.Addr() + "/0")
	if err != nil {
		t.Fatalf("cliente Redis: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	d, err := denylist.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("NuevoRedis: %v", err)
	}

	sid := uuid.New()
	if revocado, err := d.Revocado(t.Context(), sid); err != nil || revocado {
		t.Fatalf("Revocado antes de revocar = (%v, %v), quiero (false, nil)", revocado, err)
	}
	if err := d.Revocar(t.Context(), sid, 5*time.Minute); err != nil {
		t.Fatalf("Revocar: %v", err)
	}
	if revocado, err := d.Revocado(t.Context(), sid); err != nil || !revocado {
		t.Fatalf("Revocado tras revocar = (%v, %v), quiero (true, nil)", revocado, err)
	}

	mr.FastForward(6 * time.Minute)
	if revocado, err := d.Revocado(t.Context(), sid); err != nil || revocado {
		t.Fatalf("Revocado tras el ttl = (%v, %v), quiero (false, nil)", revocado, err)
	}
}

func TestDenylistRechazaClienteNilYTtlNoPositivo(t *testing.T) {
	if _, err := denylist.NuevoRedis(nil); err == nil {
		t.Fatal("NuevoRedis(nil) devolvió nil, quiero error")
	}

	mr := miniredis.RunT(t)
	cliente, err := platformredis.NuevoCliente("redis://" + mr.Addr() + "/0")
	if err != nil {
		t.Fatalf("cliente Redis: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	d, err := denylist.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("NuevoRedis: %v", err)
	}
	if err := d.Revocar(t.Context(), uuid.New(), 0); err == nil {
		t.Fatal("Revocar con ttl cero devolvió nil, quiero error")
	}
}

func TestDenylistCierraCuandoRedisFalla(t *testing.T) {
	cliente, err := platformredis.NuevoCliente("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("cliente Redis: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	d, err := denylist.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("NuevoRedis: %v", err)
	}

	if err := d.Revocar(t.Context(), uuid.New(), time.Minute); !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("Revocar = %v, quiero ErrAuthNoDisponible", err)
	}
	if _, err := d.Revocado(t.Context(), uuid.New()); !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("Revocado = %v, quiero ErrAuthNoDisponible", err)
	}
}
