package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEnmascararCelular(t *testing.T) {
	casos := map[string]string{
		"+573001112233": "****2233",
		"123":           "****",
	}
	for entrada, quiero := range casos {
		if got := enmascararCelular(entrada); got != quiero {
			t.Errorf("enmascararCelular(%q) = %q, quiero %q", entrada, got, quiero)
		}
	}
}

func TestExigirFueraDeEnfriamiento(t *testing.T) {
	servicio := &ServicioDeCambioDeNumero{enfriamiento: 24 * time.Hour}
	casos := map[string]struct {
		cambiado time.Time
		valido   bool
		quiero   error
	}{
		"sin cambio previo":   {valido: false, quiero: nil},
		"cambio reciente":     {cambiado: time.Now().Add(-time.Hour), valido: true, quiero: ErrCambioNumeroEnEnfriamiento},
		"fuera de la ventana": {cambiado: time.Now().Add(-48 * time.Hour), valido: true, quiero: nil},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			credencial := repositorio.Credencial{CelularCambiadoEn: pgtype.Timestamptz{Time: caso.cambiado, Valid: caso.valido}}
			if err := servicio.exigirFueraDeEnfriamiento(credencial); !errors.Is(err, caso.quiero) {
				t.Fatalf("err = %v, quiero %v", err, caso.quiero)
			}
		})
	}
}

func TestExigirCredencialUsable(t *testing.T) {
	servicio := &ServicioDeCambioDeNumero{}
	derivado := pgtype.Text{String: "$argon2id$...", Valid: true}
	casos := map[string]struct {
		credencial repositorio.Credencial
		quiero     error
	}{
		"activa con contrasena": {credencial: repositorio.Credencial{Estado: string(EstadoActivo), ValorDerivado: derivado}, quiero: nil},
		"suspendida":            {credencial: repositorio.Credencial{Estado: string(EstadoSuspendido), ValorDerivado: derivado}, quiero: ErrCredencialesInvalidas},
		"sin contrasena":        {credencial: repositorio.Credencial{Estado: string(EstadoActivo)}, quiero: ErrCredencialesInvalidas},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			if err := servicio.exigirCredencialUsable(caso.credencial); !errors.Is(err, caso.quiero) {
				t.Fatalf("err = %v, quiero %v", err, caso.quiero)
			}
		})
	}
}

func TestNuevoServicioDeCambioDeNumeroValidaSuEntrada(t *testing.T) {
	pepper := make([]byte, 32)
	if _, err := NuevoServicioDeCambioDeNumero(nil, nil, nil, nil, nil, nil, nil, nil, pepper, time.Hour); err == nil {
		t.Fatal("quiero error con dependencias nulas")
	}
	pool := &pgxpool.Pool{}
	desafios := &GestorDesafios{}
	sesiones := &GestorSesiones{}
	derivador := &DerivadorArgon2{}
	claves, err := NuevasClaves(pepper)
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	if _, err := NuevoServicioDeCambioDeNumero(pool, desafios, sesiones, derivador, nil, nil, &contadoresDeCambio{}, claves, pepper[:8], time.Hour); err == nil {
		t.Fatal("quiero error con un pepper corto")
	}
	if _, err := NuevoServicioDeCambioDeNumero(pool, desafios, sesiones, derivador, nil, nil, &contadoresDeCambio{}, claves, pepper, 0); err == nil {
		t.Fatal("quiero error con una ventana de enfriamiento no positiva")
	}
}

// contadoresDeCambio satisface el puerto de contadores sin tocar Redis.
type contadoresDeCambio struct{}

// Incrementar devuelve uno.
func (contadoresDeCambio) Incrementar(context.Context, string, time.Duration) (int64, error) {
	return 1, nil
}

// Bloquear no hace nada.
func (contadoresDeCambio) Bloquear(context.Context, string, time.Duration) error { return nil }

// Bloqueada responde que no.
func (contadoresDeCambio) Bloqueada(context.Context, string) (bool, error) { return false, nil }
