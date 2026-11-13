package main

import (
	"context"
	"log/slog"
	"sync"
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

// EmisorDelUsuario es lo que la emisión tras la liquidación necesita del servicio de inteligencia.
type EmisorDelUsuario interface {
	EmitirAvisosDelUsuario(ctx context.Context, usuarioID string, ahora time.Time) (int, error)
}

// frenoDeEmisiones frena a una emisión por minuto y usuario y registra lo que pasó. El mapa es de
// memoria y se poda solo: si nadie vuelve a confirmar, la entrada vieja sale de la próxima pasada.
type frenoDeEmisiones struct {
	mu         sync.Mutex
	ultima     map[string]time.Time
	intervalo  time.Duration
	reloj      func() time.Time
	emisor     EmisorDelUsuario
	activo     bool
	cuandoPoda int
}

// nuevoFrenoDeEmisiones arma el freno con el reloj y el emisor pedidos. Apagado no emite nada,
// igual que la bandera del motor.
func nuevoFrenoDeEmisiones(activo bool, emisor EmisorDelUsuario, reloj func() time.Time) *frenoDeEmisiones {
	if reloj == nil {
		reloj = time.Now
	}
	return &frenoDeEmisiones{
		ultima:     map[string]time.Time{},
		intervalo:  time.Minute,
		reloj:      reloj,
		emisor:     emisor,
		activo:     activo,
		cuandoPoda: 1024,
	}
}

// disparar es lo que el núcleo llama después del commit. Toma el turno en el momento de la
// confirmación y suelta la emisión en una goroutine para no demorar la respuesta al usuario.
func (f *frenoDeEmisiones) disparar(ctx context.Context, usuarioID string, hora time.Time) {
	if !f.activo || f.emisor == nil || !f.tomarTurno(usuarioID, hora) {
		return
	}
	base := context.WithoutCancel(ctx)
	go f.emitirYRegistrar(base, usuarioID, hora)
}

// tomarTurno devuelve si este usuario puede emitir ahora y, si puede, lo da por atendido.
func (f *frenoDeEmisiones) tomarTurno(usuarioID string, hora time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ultima, ya := f.ultima[usuarioID]; ya && hora.Sub(ultima) < f.intervalo {
		return false
	}
	f.ultima[usuarioID] = hora
	f.podarLocked(hora)
	return true
}

// podarLocked descarta las entradas que ya pasaron el intervalo para que el mapa no crezca sin fin.
func (f *frenoDeEmisiones) podarLocked(hora time.Time) {
	if len(f.ultima) < f.cuandoPoda {
		return
	}
	for usuario, ultima := range f.ultima {
		if hora.Sub(ultima) >= f.intervalo {
			delete(f.ultima, usuario)
		}
	}
}

// emitirYRegistrar corre la emisión real con recuperación de pánico y registra usuario, conteo y
// duración, sin montos. Un fallo del emisor nunca toca la confirmación que ya quedó asentada.
func (f *frenoDeEmisiones) emitirYRegistrar(ctx context.Context, usuarioID string, hora time.Time) (emitidos int) {
	inicio := f.reloj()
	defer func() {
		if recuperado := recover(); recuperado != nil {
			slog.Error("la emisión tras la liquidación entró en pánico", "usuario_id", usuarioID, "recuperado", recuperado)
			emitidos = 0
		}
	}()
	emitidos, err := f.emisor.EmitirAvisosDelUsuario(ctx, usuarioID, hora)
	if err != nil {
		slog.Error("la emisión tras la liquidación falló", "usuario_id", usuarioID, "error", err)
		return 0
	}
	slog.Info("avisos emitidos tras la liquidación", "usuario_id", usuarioID, "emitidos", emitidos, "duracion", time.Since(inicio))
	return emitidos
}
