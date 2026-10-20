package evaluacion

import (
	"context"
	"errors"
	"math"
	"time"
	_ "time/tzdata" // El cálculo del corte diario también funciona en contenedores sin tzdata.

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// OpcionConsola configura únicamente el laboratorio/CLI, no el evaluador por lotes.
type OpcionConsola func(*Consola)

// ConRecuperacion habilita un reintento de errores transitorios, contabilizado y cancelable.
func ConRecuperacion() OpcionConsola { return func(c *Consola) { c.recuperar = true } }

type pausaProveedor struct {
	hasta  time.Time
	limite *copilot.ErrorLimiteIA
}

func (p *pausaProveedor) comprobar(ahora time.Time) error {
	if p.limite != nil && ahora.Before(p.hasta) {
		return &copilot.ErrorLimiteIA{Clase: p.limite.Clase, ReintentarEn: p.hasta.Sub(ahora)}
	}
	return nil
}
func (p *pausaProveedor) segundos(ahora time.Time) int {
	if p.comprobar(ahora) == nil {
		return 0
	}
	return int(math.Ceil(p.hasta.Sub(ahora).Seconds()))
}
func (p *pausaProveedor) registrar(err error, ahora time.Time) {
	if !errors.Is(err, copilot.ErrLimiteIA) {
		return
	}
	e := &copilot.ErrorLimiteIA{Clase: copilot.LimiteDesconocido, ReintentarEn: time.Minute}
	var conocido *copilot.ErrorLimiteIA
	if errors.As(err, &conocido) {
		e = &copilot.ErrorLimiteIA{Clase: conocido.Clase, ReintentarEn: conocido.ReintentarEn}
	}
	if e.ReintentarEn <= 0 || e.ReintentarEn > 24*time.Hour {
		e.ReintentarEn = time.Minute
	}
	if e.ReintentarEn < time.Second {
		e.ReintentarEn = time.Second
	}
	if e.Clase == copilot.LimiteDia {
		if zona, err := time.LoadLocation("America/Los_Angeles"); err == nil {
			local := ahora.In(zona)
			corte := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, zona)
			e.ReintentarEn = corte.Sub(ahora)
		} else {
			e.ReintentarEn = 24 * time.Hour
		}
	}
	p.limite = e
	p.hasta = ahora.Add(e.ReintentarEn)
}

func transitorio(err error) bool {
	return err != nil && !errors.Is(err, copilot.ErrLimiteIA) && !errors.Is(err, copilot.ErrBloqueoIA) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && (errors.Is(err, copilot.ErrNoDisponibleIA) || errors.Is(err, copilot.ErrTimeoutIA))
}

func esperarRecuperacion(ctx context.Context) error {
	t := time.NewTimer(500 * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type proveedorConRecuperacion struct{ consola *Consola }

func (p proveedorConRecuperacion) Generar(ctx context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	c := p.consola
	if err := c.pausaChat.comprobar(c.ahora()); err != nil {
		return copilot.RespuestaIA{}, err
	}
	for intento := 0; ; intento++ {
		r, err := c.presupuesto.Generar(ctx, s)
		c.pausaChat.registrar(err, c.ahora())
		if errors.Is(err, copilot.ErrLimiteIA) {
			if actual := c.pausaChat.comprobar(c.ahora()); actual != nil {
				err = actual
			}
		}
		if intento >= 1 || !c.recuperar || !transitorio(err) {
			return r, err
		}
		if err := esperarRecuperacion(ctx); err != nil {
			return copilot.RespuestaIA{}, err
		}
	}
}

// Esperas informa los cooldowns de chat y TTS por separado, sin reiniciar la cuota.
func (c *Consola) Esperas() (int, int) {
	return c.pausaChat.segundos(c.ahora()), c.pausaVoz.segundos(c.ahora())
}

func (c *Consola) sintetizarControlado(ctx context.Context, ejecutar func() ([]byte, error), emitido *bool) ([]byte, error) {
	if err := c.pausaVoz.comprobar(c.ahora()); err != nil {
		return nil, err
	}
	for intento := 0; ; intento++ {
		if err := c.presupuesto.reservar(ctx); err != nil {
			return nil, err
		}
		b, err := ejecutar()
		c.pausaVoz.registrar(err, c.ahora())
		if errors.Is(err, copilot.ErrLimiteIA) {
			if actual := c.pausaVoz.comprobar(c.ahora()); actual != nil {
				err = actual
			}
		}
		if intento >= 1 || !c.recuperar || !transitorio(err) || (emitido != nil && *emitido) {
			return b, err
		}
		if err := esperarRecuperacion(ctx); err != nil {
			return nil, err
		}
	}
}
