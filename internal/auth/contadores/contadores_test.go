package contadores_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/alicebob/miniredis/v2"
)

// relojFalso da una base de tiempo controlada al adaptador en memoria.
type relojFalso struct {
	mu    sync.Mutex
	ahora time.Time
}

// Ahora devuelve el instante actual del reloj falso.
func (r *relojFalso) Ahora() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ahora
}

// Avanzar mueve el reloj falso hacia adelante.
func (r *relojFalso) Avanzar(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ahora = r.ahora.Add(d)
}

// montaje empareja el puerto con el avance del tiempo del backend.
type montaje struct {
	Contadores auth.Contadores
	Avanzar    func(time.Duration)
}

// fabricas arma un montaje nuevo por implementacion, para no compartir estado entre casos.
func fabricas() map[string]func(t *testing.T) montaje {
	return map[string]func(t *testing.T) montaje{
		"memoria": func(t *testing.T) montaje {
			t.Helper()
			reloj := &relojFalso{ahora: time.Unix(0, 0)}
			return montaje{Contadores: contadores.EnMemoriaConReloj(reloj.Ahora), Avanzar: reloj.Avanzar}
		},
		"redis": func(t *testing.T) montaje {
			t.Helper()
			mr := miniredis.RunT(t)
			cliente, err := platformredis.NuevoCliente("redis://" + mr.Addr() + "/0")
			if err != nil {
				t.Fatalf("construir cliente Redis: %v", err)
			}
			t.Cleanup(func() { _ = cliente.Close() })
			r, err := contadores.NuevoRedis(cliente)
			if err != nil {
				t.Fatalf("NuevoRedis: %v", err)
			}
			return montaje{Contadores: r, Avanzar: mr.FastForward}
		},
	}
}

// TestContadoresComportamientoComun corre la misma tabla contra memoria y Redis real de miniredis.
func TestContadoresComportamientoComun(t *testing.T) {
	casos := map[string]func(t *testing.T, m montaje){
		"incrementa acumulando": func(t *testing.T, m montaje) {
			t.Helper()
			for i := int64(1); i <= 3; i++ {
				valor, err := m.Contadores.Incrementar(t.Context(), "otp:cuenta:a", time.Minute)
				if err != nil || valor != i {
					t.Fatalf("Incrementar = (%d, %v), quiero (%d, nil)", valor, err, i)
				}
			}
		},
		"reinicia al vencer la ventana": func(t *testing.T, m montaje) {
			t.Helper()
			if valor, err := m.Contadores.Incrementar(t.Context(), "otp:cuenta:b", time.Minute); err != nil || valor != 1 {
				t.Fatalf("primer Incrementar = (%d, %v), quiero (1, nil)", valor, err)
			}
			m.Avanzar(2 * time.Minute)
			if valor, err := m.Contadores.Incrementar(t.Context(), "otp:cuenta:b", time.Minute); err != nil || valor != 1 {
				t.Fatalf("Incrementar tras la ventana = (%d, %v), quiero (1, nil)", valor, err)
			}
		},
		"bloquea y desbloquea": func(t *testing.T, m montaje) {
			t.Helper()
			if err := m.Contadores.Bloquear(t.Context(), "login:cuenta:c", time.Minute); err != nil {
				t.Fatalf("Bloquear: %v", err)
			}
			if bloqueada, err := m.Contadores.Bloqueada(t.Context(), "login:cuenta:c"); err != nil || !bloqueada {
				t.Fatalf("Bloqueada = (%v, %v), quiero (true, nil)", bloqueada, err)
			}
			m.Avanzar(2 * time.Minute)
			if bloqueada, err := m.Contadores.Bloqueada(t.Context(), "login:cuenta:c"); err != nil || bloqueada {
				t.Fatalf("Bloqueada tras vencer = (%v, %v), quiero (false, nil)", bloqueada, err)
			}
		},
		"sin bloqueo previo responde falso": func(t *testing.T, m montaje) {
			t.Helper()
			if bloqueada, err := m.Contadores.Bloqueada(t.Context(), "login:cuenta:d"); err != nil || bloqueada {
				t.Fatalf("Bloqueada = (%v, %v), quiero (false, nil)", bloqueada, err)
			}
		},
		"ventana no positiva falla": func(t *testing.T, m montaje) {
			t.Helper()
			if _, err := m.Contadores.Incrementar(t.Context(), "otp:cuenta:e", 0); err == nil {
				t.Fatal("Incrementar con ventana cero devolvió nil, quiero error")
			}
			if err := m.Contadores.Bloquear(t.Context(), "login:cuenta:e", 0); err == nil {
				t.Fatal("Bloquear con duracion cero devolvió nil, quiero error")
			}
		},
	}

	for nombre, fabrica := range fabricas() {
		t.Run(nombre, func(t *testing.T) {
			for caso, correr := range casos {
				t.Run(caso, func(t *testing.T) {
					correr(t, fabrica(t))
				})
			}
		})
	}
}

// TestContadoresRedisFailClosedAlCerrarMiniredis exige ErrAuthNoDisponible cuando el backend desaparece.
func TestContadoresRedisFailClosedAlCerrarMiniredis(t *testing.T) {
	mr := miniredis.RunT(t)
	cliente, err := platformredis.NuevoCliente("redis://" + mr.Addr() + "/0")
	if err != nil {
		t.Fatalf("construir cliente Redis: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	r, err := contadores.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("NuevoRedis: %v", err)
	}

	mr.Close()

	inicio := time.Now()
	if _, err := r.Incrementar(t.Context(), "otp:cuenta:cerrada", time.Minute); !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("Incrementar tras Close = %v, quiero ErrAuthNoDisponible", err)
	}
	if transcurrido := time.Since(inicio); transcurrido >= time.Second {
		t.Errorf("el fail closed tardó %v, quiero menos de 1s", transcurrido)
	}
	if err := r.Bloquear(t.Context(), "login:cuenta:cerrada", time.Minute); !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("Bloquear tras Close = %v, quiero ErrAuthNoDisponible", err)
	}
	if _, err := r.Bloqueada(t.Context(), "login:cuenta:cerrada"); !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("Bloqueada tras Close = %v, quiero ErrAuthNoDisponible", err)
	}
}

// TestNuevasClavesExige32Bytes exige el minimo del pepper.
func TestNuevasClavesExige32Bytes(t *testing.T) {
	if _, err := auth.NuevasClaves(bytes.Repeat([]byte{0x01}, 31)); err == nil {
		t.Fatal("NuevasClaves con 31 bytes devolvió nil, quiero error")
	}
	if _, err := auth.NuevasClaves(bytes.Repeat([]byte{0x01}, 32)); err != nil {
		t.Fatalf("NuevasClaves con 32 bytes: %v", err)
	}
}

// TestClaveDeCorreoNormalizaYNoContieneElCorreo prueba que el HMAC es estable y no expone el correo.
func TestClaveDeCorreoNormalizaYNoContieneElCorreo(t *testing.T) {
	claves, err := auth.NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	a := claves.ClaveDeCorreo(strings.ToLower("Persona@Correo.com"))
	b := claves.ClaveDeCorreo(strings.ToLower("persona@correo.com"))
	if a != b {
		t.Fatalf("claves distintas para el mismo correo normalizado: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("len(clave) = %d, quiero 64 hex de SHA-256", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Fatalf("la clave no es hex: %v", err)
	}
	if strings.Contains(strings.ToLower(a), "persona") || strings.Contains(strings.ToLower(a), "correo") {
		t.Fatalf("la clave contiene el correo: %q", a)
	}

	otras, err := auth.NuevasClaves(bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	if claves.ClaveDeCorreo("persona@correo.com") == otras.ClaveDeCorreo("persona@correo.com") {
		t.Fatal("dos peppers distintos produjeron la misma clave")
	}
}

// TestClaveDeOrigenCanonicalizaLaIP prueba IPv4 sin cambios y el /64 de IPv6.
func TestClaveDeOrigenCanonicalizaLaIP(t *testing.T) {
	claves, err := auth.NuevasClaves(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	if got := claves.ClaveDeOrigen("192.168.10.7"); got != "192.168.10.7" {
		t.Errorf("ClaveDeOrigen IPv4 = %q, quiero la misma IP", got)
	}
	if a, b := claves.ClaveDeOrigen("2001:db8:1:2:3:4:5:6"), claves.ClaveDeOrigen("2001:db8:1:2::9"); a != b {
		t.Errorf("IPv6 del mismo /64 difieren: %q != %q", a, b)
	}
	if claves.ClaveDeOrigen("2001:db8:1:2::1") == claves.ClaveDeOrigen("2001:db8:1:3::1") {
		t.Error("IPv6 de /64 distintos dieron la misma clave")
	}
}
