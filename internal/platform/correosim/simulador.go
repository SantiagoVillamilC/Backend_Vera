// Package correosim es el canal de correo simulado, determinista y sin red, para pruebas y demo.
package correosim

import (
	"context"
	"log/slog"
	"sync"
)

// Aviso es el correo que el simulador guardó para inspección.
type Aviso struct {
	Asunto string
	Cuerpo string
}

// Simulador guarda en memoria el último aviso entregado a cada correo.
type Simulador struct {
	mu      sync.Mutex
	ultimos map[string]Aviso
}

// Nuevo arma el simulador de correo.
func Nuevo() *Simulador {
	return &Simulador{ultimos: map[string]Aviso{}}
}

// Enviar guarda el aviso del correo y devuelve un id de mensaje determinista.
func (s *Simulador) Enviar(ctx context.Context, correo, asunto, cuerpo string) (string, error) {
	s.mu.Lock()
	s.ultimos[correo] = Aviso{Asunto: asunto, Cuerpo: cuerpo}
	s.mu.Unlock()
	id := "correo-sim:" + correo
	slog.InfoContext(ctx, "aviso de correo simulado", "id_mensaje", id)
	return id, nil
}

// UltimoAviso devuelve el último aviso guardado para el correo y si existe.
func (s *Simulador) UltimoAviso(correo string) (Aviso, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	aviso, ok := s.ultimos[correo]
	return aviso, ok
}
