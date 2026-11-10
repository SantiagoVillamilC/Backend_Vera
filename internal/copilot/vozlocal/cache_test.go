package vozlocal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

type sintetizadorPrueba func(context.Context, string, string) ([]byte, error)

func (f sintetizadorPrueba) Sintetizar(c context.Context, t, v string) ([]byte, error) {
	return f(c, t, v)
}

func TestCacheLimitesCaducidadYCancelacion(t *testing.T) {
	llamadas := 0
	c := NuevaCache(sintetizadorPrueba(func(context.Context, string, string) ([]byte, error) { llamadas++; return wavPrueba(), nil }))
	for i := range 17 {
		if _, e := c.Sintetizar(t.Context(), fmt.Sprint(i), ""); e != nil {
			t.Fatal(e)
		}
	}
	if len(c.entradas) != 16 {
		t.Fatal("caché no acotada")
	}
	for k, e := range c.entradas {
		e.vence = time.Now().Add(-time.Second)
		c.entradas[k] = e
	}
	if _, e := c.Sintetizar(t.Context(), "16", ""); e != nil || llamadas != 18 {
		t.Fatal("no caducó", e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e := c.Sintetizar(ctx, "16", ""); !errors.Is(e, context.Canceled) {
		t.Fatal("ignoró cancelación", e)
	}
	invalida := NuevaCache(sintetizadorPrueba(func(context.Context, string, string) ([]byte, error) { return []byte("audio inválido"), nil }))
	if _, e := invalida.Sintetizar(t.Context(), "hola", ""); !errors.Is(e, copilot.ErrRespuestaIA) {
		t.Fatal("aceptó WAV inválido", e)
	}
}

func TestCacheRepeticionConcurrente(t *testing.T) {
	var llamadas atomic.Int32
	c := NuevaCache(sintetizadorPrueba(func(context.Context, string, string) ([]byte, error) { llamadas.Add(1); return wavPrueba(), nil }))
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			a, e := c.Sintetizar(t.Context(), "Hola", "ef_dora")
			if e != nil || !WAVValido(a) {
				t.Error("audio inválido", e)
			}
			if len(a) > 0 {
				a[0] = 0
			}
		})
	}
	wg.Wait()
	if llamadas.Load() != 1 {
		t.Fatalf("síntesis: %d", llamadas.Load())
	}
}

func TestCacheNoConservaFallos(t *testing.T) {
	for _, audio := range [][]byte{nil, []byte("no es WAV")} {
		llamadas := 0
		c := NuevaCache(sintetizadorPrueba(func(context.Context, string, string) ([]byte, error) {
			llamadas++
			return audio, copilot.ErrNoDisponibleIA
		}))
		for range 2 {
			if _, e := c.Sintetizar(t.Context(), "Hola", ""); e == nil {
				t.Fatal("aceptó fallo")
			}
		}
		if llamadas != 2 {
			t.Fatal("guardó fallo")
		}
	}
}
