package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
)

// Los tres modos de caída que acepta el simulador.
const (
	modoNinguno  = "ninguno"
	modoRechaza  = "rechaza"
	modoSilencio = "silencio"
)

// maximoLatenciaMS acota la latencia sumada por debajo del ReadTimeout de 15s para no romper la lectura del cuerpo.
const maximoLatenciaMS int64 = 10000

// Perillas son las perillas de fidelidad del simulador, fieles por defecto y gobernables en caliente.
type Perillas struct {
	PRechazo             float64 `json:"p_rechazo"`
	LatenciaMS           int64   `json:"latencia_ms"`
	LatenciaVarMS        int64   `json:"latencia_var_ms"`
	ModoCaida            string  `json:"modo_caida"`
	PRespuestaPerdida    float64 `json:"p_respuesta_perdida"`
	PNotificacionPerdida float64 `json:"p_notificacion_perdida"`
}

// LogValue imprime el grupo completo de perillas con los mismos nombres del control y el JSON.
func (p Perillas) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Float64("p_rechazo", p.PRechazo),
		slog.Int64("latencia_ms", p.LatenciaMS),
		slog.Int64("latencia_var_ms", p.LatenciaVarMS),
		slog.String("modo_caida", p.ModoCaida),
		slog.Float64("p_respuesta_perdida", p.PRespuestaPerdida),
		slog.Float64("p_notificacion_perdida", p.PNotificacionPerdida),
	)
}

var _ slog.LogValuer = Perillas{}

// Validar reporta todos los valores fuera de rango o con un modo de caída desconocido.
func (p Perillas) Validar() error {
	var errs []error
	if !rangoProbabilidad(p.PRechazo) {
		errs = append(errs, fmt.Errorf("p_rechazo debe estar en [0,1], llegó %v", p.PRechazo))
	}
	if p.LatenciaMS < 0 {
		errs = append(errs, fmt.Errorf("latencia_ms debe ser mayor o igual que 0, llegó %d", p.LatenciaMS))
	}
	if p.LatenciaVarMS < 0 {
		errs = append(errs, fmt.Errorf("latencia_var_ms debe ser mayor o igual que 0, llegó %d", p.LatenciaVarMS))
	}
	if p.LatenciaMS > maximoLatenciaMS || p.LatenciaVarMS > maximoLatenciaMS || p.LatenciaMS+p.LatenciaVarMS > maximoLatenciaMS {
		errs = append(errs, excesoDeLatencia(p))
	}
	if !p.modoCaidaValido() {
		errs = append(errs, fmt.Errorf("modo_caida debe ser ninguno, rechaza o silencio, llegó %q", p.ModoCaida))
	}
	if !rangoProbabilidad(p.PRespuestaPerdida) {
		errs = append(errs, fmt.Errorf("p_respuesta_perdida debe estar en [0,1], llegó %v", p.PRespuestaPerdida))
	}
	if !rangoProbabilidad(p.PNotificacionPerdida) {
		errs = append(errs, fmt.Errorf("p_notificacion_perdida debe estar en [0,1], llegó %v", p.PNotificacionPerdida))
	}
	return errors.Join(errs...)
}

// excesoDeLatencia nombra solo los campos que exceden el tope, y la suma cuando ninguno lo hace por su cuenta.
func excesoDeLatencia(p Perillas) error {
	partes := make([]string, 0, 2)
	if p.LatenciaMS > maximoLatenciaMS {
		partes = append(partes, fmt.Sprintf("latencia_ms=%d", p.LatenciaMS))
	}
	if p.LatenciaVarMS > maximoLatenciaMS {
		partes = append(partes, fmt.Sprintf("latencia_var_ms=%d", p.LatenciaVarMS))
	}
	if len(partes) == 0 {
		return fmt.Errorf("latencia_ms + latencia_var_ms no debe pasar de %d, llegó %d + %d", maximoLatenciaMS, p.LatenciaMS, p.LatenciaVarMS)
	}
	return fmt.Errorf("la latencia no debe pasar de %d, llegó %s", maximoLatenciaMS, strings.Join(partes, " + "))
}

// rangoProbabilidad acepta [0,1] y rechaza NaN porque toda comparación con NaN es falsa.
func rangoProbabilidad(v float64) bool { return v >= 0 && v <= 1 }

func (p Perillas) modoCaidaValido() bool {
	return p.ModoCaida == modoNinguno || p.ModoCaida == modoRechaza || p.ModoCaida == modoSilencio
}

// perillasFieles son las perillas sin fallas con las que arranca el simulador sin configuración.
func perillasFieles() Perillas { return Perillas{ModoCaida: modoNinguno} }

// cargarPerillas lee las seis variables del simulador y nombra cada una que no parsee o no valide.
func cargarPerillas() (Perillas, []error) {
	p := perillasFieles()
	var errs []error
	p.PRechazo = flotanteDeEnv("SIMULADOR_CUSTODIA_P_RECHAZO", p.PRechazo, &errs)
	p.LatenciaMS = enteroDeEnv("SIMULADOR_CUSTODIA_LATENCIA_MS", p.LatenciaMS, &errs)
	p.LatenciaVarMS = enteroDeEnv("SIMULADOR_CUSTODIA_LATENCIA_VAR_MS", p.LatenciaVarMS, &errs)
	p.ModoCaida = envOr("SIMULADOR_CUSTODIA_MODO_CAIDA", p.ModoCaida)
	p.PRespuestaPerdida = flotanteDeEnv("SIMULADOR_CUSTODIA_P_RESPUESTA_PERDIDA", p.PRespuestaPerdida, &errs)
	p.PNotificacionPerdida = flotanteDeEnv("SIMULADOR_CUSTODIA_P_NOTIFICACION_PERDIDA", p.PNotificacionPerdida, &errs)
	if err := p.Validar(); err != nil {
		errs = append(errs, err)
	}
	return p, errs
}

// flotanteDeEnv parsea la variable y reporta la clave cuando no parsea un número.
func flotanteDeEnv(clave string, porDefecto float64, errs *[]error) float64 {
	v := os.Getenv(clave)
	if v == "" {
		return porDefecto
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a number, got %q", clave, v))
		return porDefecto
	}
	return f
}

// enteroDeEnv parsea la variable y reporta la clave cuando no es un entero.
func enteroDeEnv(clave string, porDefecto int64, errs *[]error) int64 {
	v := os.Getenv(clave)
	if v == "" {
		return porDefecto
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be an integer, got %q", clave, v))
		return porDefecto
	}
	return n
}

// ControlPerillas guarda las perillas vigentes y las reemplaza de forma atómica en caliente.
type ControlPerillas struct{ vigentes atomic.Pointer[Perillas] }

// NuevoControlPerillas deja vigente la copia que llegó del arranque.
func NuevoControlPerillas(p Perillas) *ControlPerillas {
	c := &ControlPerillas{}
	c.vigentes.Store(&p)
	return c
}

// Vigentes devuelve la copia con la que se decide la próxima petición.
func (c *ControlPerillas) Vigentes() Perillas { return *c.vigentes.Load() }

// Aplicar fusiona el cambio parcial y lo publica con compare-and-swap, reintentando si otro ganó.
func (c *ControlPerillas) Aplicar(parcial perillasParciales) (Perillas, error) {
	for {
		base := c.vigentes.Load()
		nuevas := *base
		if err := nuevas.fusionar(parcial); err != nil {
			return Perillas{}, err
		}
		if c.vigentes.CompareAndSwap(base, &nuevas) {
			return nuevas, nil
		}
	}
}

// perillasParciales distingue un campo ausente de uno con valor cero en el cuerpo del control.
type perillasParciales struct {
	PRechazo             *float64 `json:"p_rechazo"`
	LatenciaMS           *int64   `json:"latencia_ms"`
	LatenciaVarMS        *int64   `json:"latencia_var_ms"`
	ModoCaida            *string  `json:"modo_caida"`
	PRespuestaPerdida    *float64 `json:"p_respuesta_perdida"`
	PNotificacionPerdida *float64 `json:"p_notificacion_perdida"`
}

// fusionar aplica encima una copia y valida el resultado sin tocar las perillas vigentes.
func (p *Perillas) fusionar(parcial perillasParciales) error {
	if parcial.PRechazo != nil {
		p.PRechazo = *parcial.PRechazo
	}
	if parcial.LatenciaMS != nil {
		p.LatenciaMS = *parcial.LatenciaMS
	}
	if parcial.LatenciaVarMS != nil {
		p.LatenciaVarMS = *parcial.LatenciaVarMS
	}
	if parcial.ModoCaida != nil {
		p.ModoCaida = *parcial.ModoCaida
	}
	if parcial.PRespuestaPerdida != nil {
		p.PRespuestaPerdida = *parcial.PRespuestaPerdida
	}
	if parcial.PNotificacionPerdida != nil {
		p.PNotificacionPerdida = *parcial.PNotificacionPerdida
	}
	return p.Validar()
}
