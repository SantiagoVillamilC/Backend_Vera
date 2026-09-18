package main

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// ModoCaida es la forma en que el simulador deja de atender el envío.
type ModoCaida string

// Los dos modos de caída que el experimento corre por separado, más el estado sin caída.
const (
	ModoCaidaNinguna           ModoCaida = "ninguna"
	ModoCaidaConexionRechazada ModoCaida = "conexion_rechazada"
	ModoCaidaSilencio          ModoCaida = "silencio"
)

// ErrPerillasInvalidas reporta una perilla fuera de rango o con un modo desconocido.
var ErrPerillasInvalidas = errors.New("simulador-riel: perillas inválidas")

// Perillas gobierna la fidelidad del simulador durante una corrida.
type Perillas struct {
	PRechazo          float64
	Latencia          time.Duration
	Variacion         time.Duration
	ModoCaida         ModoCaida
	PRespuestaPerdida float64
}

// LogValue registra los valores de la corrida con el nombre de cada perilla.
func (p Perillas) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Float64("p_rechazo", p.PRechazo),
		slog.String("latencia", p.Latencia.String()),
		slog.String("latencia_variacion", p.Variacion.String()),
		slog.String("modo_caida", string(p.ModoCaida)),
		slog.Float64("p_respuesta_perdida", p.PRespuestaPerdida),
	)
}

// Valida rechaza probabilidades fuera de [0,1], duraciones negativas y un modo desconocido.
func (p Perillas) Valida() error {
	var errs []error
	if math.IsNaN(p.PRechazo) || p.PRechazo < 0 || p.PRechazo > 1 {
		errs = append(errs, fmt.Errorf("%w: p_rechazo debe estar en [0,1]", ErrPerillasInvalidas))
	}
	if math.IsNaN(p.PRespuestaPerdida) || p.PRespuestaPerdida < 0 || p.PRespuestaPerdida > 1 {
		errs = append(errs, fmt.Errorf("%w: p_respuesta_perdida debe estar en [0,1]", ErrPerillasInvalidas))
	}
	if p.Latencia < 0 || p.Variacion < 0 {
		errs = append(errs, fmt.Errorf("%w: las latencias no pueden ser negativas", ErrPerillasInvalidas))
	}
	switch p.ModoCaida {
	case ModoCaidaNinguna, ModoCaidaConexionRechazada, ModoCaidaSilencio:
	default:
		errs = append(errs, fmt.Errorf("%w: modo_caida desconocido %q", ErrPerillasInvalidas, p.ModoCaida))
	}
	return errors.Join(errs...)
}

// Demora devuelve la latencia de esta llamada con su variación uniforme, nunca negativa.
func (p Perillas) Demora() time.Duration {
	if p.Variacion <= 0 {
		return p.Latencia
	}
	desvio := time.Duration((rand.Float64()*2 - 1) * float64(p.Variacion))
	if demora := p.Latencia + desvio; demora > 0 {
		return demora
	}
	return 0
}

// ocurre sortea una probabilidad; los extremos son deterministas.
func ocurre(probabilidad float64) bool {
	if probabilidad <= 0 {
		return false
	}
	if probabilidad >= 1 {
		return true
	}
	return rand.Float64() < probabilidad
}

// Control guarda las perillas vigentes y las cambia en caliente bajo un candado.
type Control struct {
	mu       sync.RWMutex
	perillas Perillas
}

// NuevoControl arma el control con las perillas iniciales.
func NuevoControl(iniciales Perillas) *Control {
	return &Control{perillas: iniciales}
}

// Actuales devuelve una copia de las perillas vigentes.
func (c *Control) Actuales() Perillas {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.perillas
}

// Fijar reemplaza las perillas y devuelve las anteriores y las nuevas.
func (c *Control) Fijar(nuevas Perillas) (Perillas, Perillas) {
	c.mu.Lock()
	defer c.mu.Unlock()
	antes := c.perillas
	c.perillas = nuevas
	return antes, nuevas
}

// perillasHTTP es la forma en el cable de las perillas, con las duraciones en milisegundos.
type perillasHTTP struct {
	PRechazo          float64 `json:"p_rechazo"`
	LatenciaMs        int64   `json:"latencia_ms"`
	VariacionMs       int64   `json:"variacion_ms"`
	ModoCaida         string  `json:"modo_caida"`
	PRespuestaPerdida float64 `json:"p_respuesta_perdida"`
}

// aHTTP lleva las perillas a su forma en el cable.
func aHTTP(p Perillas) perillasHTTP {
	return perillasHTTP{
		PRechazo:          p.PRechazo,
		LatenciaMs:        p.Latencia.Milliseconds(),
		VariacionMs:       p.Variacion.Milliseconds(),
		ModoCaida:         string(p.ModoCaida),
		PRespuestaPerdida: p.PRespuestaPerdida,
	}
}

// aPerillas valida la forma en el cable y la lleva al tipo interno.
func aPerillas(h perillasHTTP) (Perillas, error) {
	if h.LatenciaMs < 0 || h.VariacionMs < 0 {
		return Perillas{}, fmt.Errorf("%w: las latencias no pueden ser negativas", ErrPerillasInvalidas)
	}
	p := Perillas{
		PRechazo:          h.PRechazo,
		Latencia:          time.Duration(h.LatenciaMs) * time.Millisecond,
		Variacion:         time.Duration(h.VariacionMs) * time.Millisecond,
		ModoCaida:         ModoCaida(h.ModoCaida),
		PRespuestaPerdida: h.PRespuestaPerdida,
	}
	if err := p.Valida(); err != nil {
		return Perillas{}, err
	}
	return p, nil
}
