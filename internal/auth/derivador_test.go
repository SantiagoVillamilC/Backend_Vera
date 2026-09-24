package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestParametrosArgon2Produccion corre siempre, sin atajo, y afirma la cabecera del PHC de 64 MiB.
func TestParametrosArgon2Produccion(t *testing.T) {
	derivador, err := NuevoDerivadorArgon2(ParametrosArgon2Produccion())
	if err != nil {
		t.Fatalf("NuevoDerivadorArgon2: %v", err)
	}

	inicio := time.Now()
	phc, err := derivador.Derivar(context.Background(), "contrasena-de-produccion")
	transcurrido := time.Since(inicio)
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=65536,t=3,p=1$") {
		t.Fatalf("PHC = %q, quiero la cabecera $argon2id$v=19$m=65536,t=3,p=1$", phc)
	}
	if transcurrido >= 5*time.Second {
		t.Errorf("Argon2id de produccion tardo %s, muy por encima del segundo esperado", transcurrido)
	}
	t.Logf("Argon2id de produccion (64 MiB, t=3, p=1): %s", transcurrido)

	ok, err := derivador.Verificar(context.Background(), "contrasena-de-produccion", phc)
	if err != nil || !ok {
		t.Fatalf("Verificar = %v, %v; quiero true, nil", ok, err)
	}
}

func TestDerivadorArgon2DePruebaVerificaYRechaza(t *testing.T) {
	t.Parallel()

	derivador, err := NuevoDerivadorArgon2(ParametrosArgon2DePrueba())
	if err != nil {
		t.Fatalf("NuevoDerivadorArgon2: %v", err)
	}
	phc, err := derivador.Derivar(t.Context(), "clave-correcta")
	if err != nil {
		t.Fatalf("Derivar: %v", err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Errorf("PHC = %q, quiero la cabecera de prueba", phc)
	}

	if ok, err := derivador.Verificar(t.Context(), "clave-correcta", phc); err != nil || !ok {
		t.Errorf("Verificar correcta = %v, %v; quiero true, nil", ok, err)
	}
	if ok, err := derivador.Verificar(t.Context(), "clave-equivocada", phc); err != nil || ok {
		t.Errorf("Verificar equivocada = %v, %v; quiero false, nil", ok, err)
	}
	if _, err := derivador.Verificar(t.Context(), "clave", "no-es-un-phc"); err == nil {
		t.Error("un PHC mal formado debia devolver error")
	}
}

// TestDerivadorVerificarRechazaPHCMalformadoSinPanico cubre los PHC que antes derribaban argon2.IDKey.
func TestDerivadorVerificarRechazaPHCMalformadoSinPanico(t *testing.T) {
	t.Parallel()

	derivador, err := NuevoDerivadorArgon2(ParametrosArgon2DePrueba())
	if err != nil {
		t.Fatalf("NuevoDerivadorArgon2: %v", err)
	}
	casos := map[string]string{
		"sal y hash vacios": "$argon2id$v=19$m=65536,t=3,p=1$$",
		"hash vacio":        "$argon2id$v=19$m=65536,t=3,p=1$c2FsdHNhbHQ$",
		"sal vacia":         "$argon2id$v=19$m=65536,t=3,p=1$$aGFzaGhhc2hoYXNoaGFzaA",
		"sin iteraciones":   "$argon2id$v=19$m=65536,t=0,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"sin paralelismo":   "$argon2id$v=19$m=65536,t=3,p=0$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"sin memoria":       "$argon2id$v=19$m=0,t=3,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
	}
	for nombre, phc := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Verificar hizo panico con %q: %v", phc, r)
				}
			}()
			ok, err := derivador.Verificar(t.Context(), "clave", phc)
			if err == nil || ok {
				t.Errorf("Verificar(%q) = %v, %v; quiero false, error", phc, ok, err)
			}
		})
	}
}

func TestNuevoDerivadorArgon2ValidaLosParametros(t *testing.T) {
	t.Parallel()

	casos := map[string]ParametrosArgon2{
		"sin memoria":     {Iteraciones: 3, Paralelismo: 1, LongitudSal: 16, LongitudSalida: 32},
		"sin iteraciones": {MemoriaKiB: 1024, Paralelismo: 1, LongitudSal: 16, LongitudSalida: 32},
		"sin paralelismo": {MemoriaKiB: 1024, Iteraciones: 3, LongitudSal: 16, LongitudSalida: 32},
		"sal corta":       {MemoriaKiB: 1024, Iteraciones: 3, Paralelismo: 1, LongitudSal: 4, LongitudSalida: 32},
		"salida corta":    {MemoriaKiB: 1024, Iteraciones: 3, Paralelismo: 1, LongitudSal: 16, LongitudSalida: 8},
	}
	for nombre, parametros := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if _, err := NuevoDerivadorArgon2(parametros); err == nil {
				t.Errorf("parametros %+v debian fallar", parametros)
			}
		})
	}
}

// derivadorBloqueado simula un hash lento que retiene el turno del semaforo.
type derivadorBloqueado struct {
	entrado chan struct{}
	liberar chan struct{}
}

func (d *derivadorBloqueado) Derivar(context.Context, string) (string, error) {
	close(d.entrado)
	<-d.liberar
	return "$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA", nil
}

func (d *derivadorBloqueado) Verificar(context.Context, string, string) (bool, error) {
	return false, nil
}

func TestDerivadorSemaforizadoCancelaLaEspera(t *testing.T) {
	falso := &derivadorBloqueado{entrado: make(chan struct{}), liberar: make(chan struct{})}
	semaforizado, err := NuevoDerivadorSemaforizado(falso, 1)
	if err != nil {
		t.Fatalf("NuevoDerivadorSemaforizado: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = semaforizado.Derivar(context.Background(), "una")
	}()
	<-falso.entrado

	_, err = semaforizado.Derivar(context.Background(), "otra")
	if !errors.Is(err, ErrAuthNoDisponible) {
		t.Fatalf("err = %v, quiero ErrAuthNoDisponible", err)
	}
	close(falso.liberar)
	wg.Wait()
}

func TestNuevoDerivadorSemaforizadoValida(t *testing.T) {
	t.Parallel()

	if _, err := NuevoDerivadorSemaforizado(nil, 1); err == nil {
		t.Error("un derivador nulo debia fallar")
	}
	if _, err := NuevoDerivadorSemaforizado(&derivadorBloqueado{}, 0); err == nil {
		t.Error("una capacidad cero debia fallar")
	}
}
