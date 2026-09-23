package auth_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/alicebob/miniredis/v2"
)

// contadoresQueFallan devuelve el error configurado en todas las operaciones del puerto.
type contadoresQueFallan struct{ err error }

func (c contadoresQueFallan) Incrementar(context.Context, string, time.Duration) (int64, error) {
	return 0, c.err
}

func (c contadoresQueFallan) Bloquear(context.Context, string, time.Duration) error { return c.err }

func (c contadoresQueFallan) Bloqueada(context.Context, string) (bool, error) { return false, c.err }

// montajeRacionador empareja el racionador con el avance del reloj del backend.
type montajeRacionador struct {
	Racionador *auth.RacionadorOTP
	Avanzar    func(time.Duration)
}

// clavesDeRacionamiento reúne el pepper de contadores de las pruebas.
func clavesDeRacionamiento(t *testing.T) *auth.Claves {
	t.Helper()

	claves, err := auth.NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	return claves
}

// fabricasRacionador arma un racionador nuevo por implementación del puerto.
func fabricasRacionador(t *testing.T) map[string]func(t *testing.T) montajeRacionador {
	t.Helper()

	return map[string]func(t *testing.T) montajeRacionador{
		"memoria": func(t *testing.T) montajeRacionador {
			t.Helper()
			reloj := &relojFalso{ahora: time.Unix(0, 0)}
			racionador, err := auth.NuevoRacionadorOTP(contadores.EnMemoriaConReloj(reloj.Ahora), clavesDeRacionamiento(t))
			if err != nil {
				t.Fatalf("NuevoRacionadorOTP memoria: %v", err)
			}
			return montajeRacionador{Racionador: racionador, Avanzar: reloj.Avanzar}
		},
		"redis": func(t *testing.T) montajeRacionador {
			t.Helper()
			mr := miniredis.RunT(t)
			cliente, err := platformredis.NuevoCliente("redis://" + mr.Addr() + "/0")
			if err != nil {
				t.Fatalf("construir cliente Redis: %v", err)
			}
			t.Cleanup(func() { _ = cliente.Close() })
			puerto, err := contadores.NuevoRedis(cliente)
			if err != nil {
				t.Fatalf("NuevoRedis: %v", err)
			}
			racionador, err := auth.NuevoRacionadorOTP(puerto, clavesDeRacionamiento(t))
			if err != nil {
				t.Fatalf("NuevoRacionadorOTP redis: %v", err)
			}
			return montajeRacionador{Racionador: racionador, Avanzar: mr.FastForward}
		},
	}
}

// relojFalso da una base de tiempo controlada al adaptador en memoria.
type relojFalso struct {
	ahora time.Time
}

// Ahora devuelve el instante actual del reloj falso.
func (r *relojFalso) Ahora() time.Time { return r.ahora }

// Avanzar mueve el reloj falso hacia adelante.
func (r *relojFalso) Avanzar(d time.Duration) { r.ahora = r.ahora.Add(d) }

func TestRacionadorOTPTopesComunes(t *testing.T) {
	casos := map[string]func(t *testing.T, m montajeRacionador){
		"cinco envios de la cuenta pasan y el sexto bloquea": func(t *testing.T, m montajeRacionador) {
			t.Helper()
			for i := 0; i < 5; i++ {
				if err := m.Racionador.Exigir(t.Context(), "persona@correo.com", "10.0.0.1"); err != nil {
					t.Fatalf("envío %d: %v", i+1, err)
				}
			}
			if err := m.Racionador.Exigir(t.Context(), "persona@correo.com", "10.0.0.1"); !errors.Is(err, auth.ErrDemasiadosEnvios) {
				t.Fatalf("sexto envío = %v, quiero ErrDemasiadosEnvios", err)
			}
		},
		"veinte envios del origen pasan y el vigesimo primero bloquea": func(t *testing.T, m montajeRacionador) {
			t.Helper()
			for i := 0; i < 20; i++ {
				if err := m.Racionador.ExigirOrigen(t.Context(), "10.0.0.2"); err != nil {
					t.Fatalf("envío %d: %v", i+1, err)
				}
			}
			if err := m.Racionador.ExigirOrigen(t.Context(), "10.0.0.2"); !errors.Is(err, auth.ErrDemasiadosEnvios) {
				t.Fatalf("vigésimo primero = %v, quiero ErrDemasiadosEnvios", err)
			}
		},
		"otra cuenta no hereda el bloqueo": func(t *testing.T, m montajeRacionador) {
			t.Helper()
			for i := 0; i < 6; i++ {
				_ = m.Racionador.ExigirCuenta(t.Context(), "bloqueada@correo.com")
			}
			if err := m.Racionador.ExigirCuenta(t.Context(), "nueva@correo.com"); err != nil {
				t.Fatalf("cuenta nueva = %v, quiero nil", err)
			}
		},
		"el bloqueo de la cuenta vence con la ventana": func(t *testing.T, m montajeRacionador) {
			t.Helper()
			for i := 0; i < 6; i++ {
				_ = m.Racionador.ExigirCuenta(t.Context(), "vence@correo.com")
			}
			m.Avanzar(2 * time.Hour)
			if err := m.Racionador.ExigirCuenta(t.Context(), "vence@correo.com"); err != nil {
				t.Fatalf("tras vencer la ventana = %v, quiero nil", err)
			}
		},
		"diez fallos pasan y el undecimo bloquea el envio": func(t *testing.T, m montajeRacionador) {
			t.Helper()
			for i := 0; i < 10; i++ {
				if err := m.Racionador.RegistrarFallo(t.Context(), "fallona@correo.com"); err != nil {
					t.Fatalf("fallo %d: %v", i+1, err)
				}
			}
			if err := m.Racionador.RegistrarFallo(t.Context(), "fallona@correo.com"); !errors.Is(err, auth.ErrDemasiadosEnvios) {
				t.Fatalf("undécimo fallo = %v, quiero ErrDemasiadosEnvios", err)
			}
			if err := m.Racionador.ExigirCuenta(t.Context(), "fallona@correo.com"); !errors.Is(err, auth.ErrDemasiadosEnvios) {
				t.Fatalf("envío con fallos bloqueados = %v, quiero ErrDemasiadosEnvios", err)
			}
		},
	}

	for nombreFabrica, fabrica := range fabricasRacionador(t) {
		t.Run(nombreFabrica, func(t *testing.T) {
			for nombreCaso, correr := range casos {
				t.Run(nombreCaso, func(t *testing.T) {
					correr(t, fabrica(t))
				})
			}
		})
	}
}

func TestRacionadorOTPFailClosed(t *testing.T) {
	t.Parallel()

	racionador, err := auth.NuevoRacionadorOTP(contadoresQueFallan{err: errors.New("dial tcp 127.0.0.1:1: connect: connection refused")}, clavesDeRacionamiento(t))
	if err != nil {
		t.Fatalf("NuevoRacionadorOTP: %v", err)
	}

	if err := racionador.Exigir(t.Context(), "persona@correo.com", "10.0.0.3"); !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("Exigir con el puerto caído = %v, quiero ErrAuthNoDisponible", err)
	}
	if err := racionador.RegistrarFallo(t.Context(), "persona@correo.com"); !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("RegistrarFallo con el puerto caído = %v, quiero ErrAuthNoDisponible", err)
	}
}

func TestNuevoRacionadorOTPExigePuertoYClaves(t *testing.T) {
	t.Parallel()

	if _, err := auth.NuevoRacionadorOTP(nil, clavesDeRacionamiento(t)); err == nil {
		t.Fatal("NuevoRacionadorOTP sin contadores devolvió nil, quiero error")
	}
	if _, err := auth.NuevoRacionadorOTP(contadores.EnMemoria(), nil); err == nil {
		t.Fatal("NuevoRacionadorOTP sin claves devolvió nil, quiero error")
	}
}
