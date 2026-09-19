package contadores

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
)

// entrada guarda el valor acumulado y su vencimiento.
type entrada struct {
	valor  int64
	expira time.Time
}

// Memoria implementa auth.Contadores en proceso y no comparte estado entre replicas.
type Memoria struct {
	mu       sync.Mutex
	reloj    func() time.Time
	valores  map[string]entrada
	bloqueos map[string]time.Time
}

// EnMemoria crea el adaptador de pruebas con el reloj real del proceso.
func EnMemoria() *Memoria {
	return &Memoria{
		reloj:    time.Now,
		valores:  make(map[string]entrada),
		bloqueos: make(map[string]time.Time),
	}
}

// EnMemoriaConReloj crea el adaptador de pruebas con un reloj inyectable para las ventanas.
func EnMemoriaConReloj(reloj func() time.Time) *Memoria {
	m := EnMemoria()
	if reloj != nil {
		m.reloj = reloj
	}
	return m
}

// Incrementar acumula sobre la clave vigente y reinicia el conteo si la ventana venció.
func (m *Memoria) Incrementar(_ context.Context, clave string, ventana time.Duration) (int64, error) {
	if ventana <= 0 {
		return 0, fmt.Errorf("contadores: la ventana debe ser positiva, tiene %s", ventana)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ahora := m.reloj()
	e, ok := m.valores[clave]
	if !ok || !e.expira.After(ahora) {
		e = entrada{expira: ahora.Add(ventana)}
	}
	e.valor++
	m.valores[clave] = e
	return e.valor, nil
}

// Bloquear marca la clave como bloqueada durante hasta.
func (m *Memoria) Bloquear(_ context.Context, clave string, hasta time.Duration) error {
	if hasta <= 0 {
		return fmt.Errorf("contadores: el bloqueo debe ser positivo, tiene %s", hasta)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bloqueos[clave] = m.reloj().Add(hasta)
	return nil
}

// Bloqueada informa si la clave sigue bloqueada segun el reloj inyectado.
func (m *Memoria) Bloqueada(_ context.Context, clave string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	expira, ok := m.bloqueos[clave]
	return ok && expira.After(m.reloj()), nil
}

var _ auth.Contadores = (*Memoria)(nil)
