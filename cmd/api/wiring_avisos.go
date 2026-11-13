package main

import (
	"context"
	"log/slog"
	"time"
)

// EmisorDeAvisos es lo que la corrida de avisos al arrancar necesita del núcleo de inteligencia.
type EmisorDeAvisos interface {
	EmitirAvisos(ctx context.Context, ahora time.Time) (int, error)
}

// correrAvisosAlArrancar emite avisos una vez con el reloj recibido cuando la bandera está
// encendida y devuelve cuántos se guardaron. Un error o un pánico del emisor se registra y no
// se propaga: una corrida perdida no puede tumbar el arranque, y la deduplicación por clave y
// período hace inocuo repetirla.
func correrAvisosAlArrancar(ctx context.Context, activo bool, emisor EmisorDeAvisos, reloj func() time.Time) (emitidos int) {
	if !activo {
		return 0
	}
	defer func() {
		if recuperado := recover(); recuperado != nil {
			slog.Error("la emisión de avisos al arrancar entró en pánico", "recuperado", recuperado)
			emitidos = 0
		}
	}()
	emitidos, err := emisor.EmitirAvisos(ctx, reloj())
	if err != nil {
		slog.Error("la emisión de avisos al arrancar falló", "error", err)
		return 0
	}
	slog.Info("avisos emitidos al arrancar", "emitidos", emitidos)
	return emitidos
}

// lanzarAvisosAlArrancar corre la emisión fuera del camino de arranque para que la API escuche
// aunque el emisor tarde. Con la bandera apagada no se lanza ninguna goroutine.
func lanzarAvisosAlArrancar(ctx context.Context, activo bool, emisor EmisorDeAvisos) {
	if !activo {
		return
	}
	go func() {
		correrAvisosAlArrancar(ctx, activo, emisor, time.Now)
	}()
}
