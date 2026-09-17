package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// El tope de intentos de cada pata y la base del backoff exponencial.
const (
	maxIntentosPata = 5
	baseBackoff     = time.Second
)

// Enviador entrega una pata a su destino por HTTP.
type Enviador interface {
	Enviar(ctx context.Context, envio Envio) error
}

// Despachador procesa las dos patas del evento, cada una con su propio reintento.
type Despachador struct {
	almacen     *Almacen
	nucleo      Enviador
	custodio    Enviador
	ahora       func() time.Time
	maxIntentos int
	base        time.Duration
	mu          sync.Mutex
	despertar   chan struct{}
}

// NuevoDespachador arma el despachador con sus dos destinos y el tope de intentos.
func NuevoDespachador(almacen *Almacen, nucleo, custodio Enviador) *Despachador {
	return &Despachador{
		almacen:     almacen,
		nucleo:      nucleo,
		custodio:    custodio,
		ahora:       time.Now,
		maxIntentos: maxIntentosPata,
		base:        baseBackoff,
		despertar:   make(chan struct{}, 1),
	}
}

// Despachar corre una pasada de las dos patas en paralelo; una no espera a la otra.
func (d *Despachador) Despachar(ctx context.Context) {
	if !d.mu.TryLock() {
		return
	}
	defer d.mu.Unlock()

	var wg sync.WaitGroup
	wg.Go(func() { d.despacharPata(ctx, pataNucleo, d.nucleo) })
	wg.Go(func() { d.despacharPata(ctx, pataCustodio, d.custodio) })
	wg.Wait()
}

// Despertar pide una pasada sin bloquear al llamador; si ya hay un aviso pendiente, lo deja.
func (d *Despachador) Despertar() {
	select {
	case d.despertar <- struct{}{}:
	default:
	}
}

// Correr despacha en cada tic y en cada aviso hasta que el contexto se cancele.
func (d *Despachador) Correr(ctx context.Context, cada time.Duration) {
	ticker := time.NewTicker(cada)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.Despachar(ctx)
		case <-d.despertar:
			d.Despachar(ctx)
		}
	}
}

// despacharPata entrega las pendientes de un destino y reprograma las que fallan.
func (d *Despachador) despacharPata(ctx context.Context, nombre string, enviador Enviador) {
	pendientes, err := d.almacen.Pendientes(ctx, nombre, d.ahora())
	if err != nil {
		slog.ErrorContext(ctx, "leer patas pendientes", "pata", nombre, "err", err)
		return
	}
	for _, p := range pendientes {
		if err := enviador.Enviar(ctx, p.Envio); err != nil {
			d.reintentar(ctx, p, err)
			continue
		}
		if err := d.almacen.MarcarEntregada(ctx, p.ID, d.ahora()); err != nil {
			slog.ErrorContext(ctx, "marcar pata entregada", "pata", nombre, "id", p.ID, "err", err)
		}
	}
}

// reintentar suma un intento y, agotado el tope, deja la pata en fallo terminal.
func (d *Despachador) reintentar(ctx context.Context, p Pata, causa error) {
	intentos := p.Intentos + 1
	if intentos >= d.maxIntentos {
		if err := d.almacen.MarcarFallida(ctx, p.ID, intentos); err != nil {
			slog.ErrorContext(ctx, "marcar pata fallida", "pata", p.Nombre, "id", p.ID, "err", err)
		}
		slog.ErrorContext(ctx, "pata en fallo terminal", "pata", p.Nombre, "id", p.ID,
			"referencia", p.Envio.Referencia, "intentos", intentos, "err", causa)
		return
	}
	disponible := d.ahora().Add(d.base * time.Duration(1<<uint(intentos-1)))
	if err := d.almacen.Reprogramar(ctx, p.ID, intentos, disponible); err != nil {
		slog.ErrorContext(ctx, "reprogramar pata", "pata", p.Nombre, "id", p.ID, "err", err)
	}
}
