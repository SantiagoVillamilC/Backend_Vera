package vozlocal

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

type entrada struct {
	audio []byte
	vence time.Time
}

// Cache conserva hasta 16 WAV durante cinco minutos, sin guardar el texto.
// Serializa las síntesis, incluido el mismo texto solicitado concurrentemente.
type Cache struct {
	voz      copilot.SintetizadorVoz
	turno    chan struct{}
	entradas map[[32]byte]entrada
}

// NuevaCache envuelve el sintetizador local con memoria acotada y cancelación.
func NuevaCache(voz copilot.SintetizadorVoz) *Cache {
	return &Cache{voz: voz, turno: make(chan struct{}, 1), entradas: make(map[[32]byte]entrada)}
}

// Sintetizar devuelve únicamente WAV válidos de hasta aproximadamente 87 segundos.
func (c *Cache) Sintetizar(ctx context.Context, texto, voz string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case c.turno <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.turno }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	clave := sha256.Sum256([]byte(texto))
	ahora := time.Now()
	for k, e := range c.entradas {
		if !ahora.Before(e.vence) {
			delete(c.entradas, k)
		}
	}
	if e, ok := c.entradas[clave]; ok {
		return append([]byte(nil), e.audio...), nil
	}
	audio, err := c.voz.Sintetizar(ctx, texto, voz)
	if err != nil {
		return nil, err
	}
	if !WAVValido(audio) {
		return nil, copilot.ErrRespuestaIA
	}
	if len(c.entradas) >= 16 {
		for k := range c.entradas {
			delete(c.entradas, k)
			break
		}
	}
	c.entradas[clave] = entrada{append([]byte(nil), audio...), time.Now().Add(5 * time.Minute)}
	return audio, nil
}
