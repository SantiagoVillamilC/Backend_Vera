package evaluacion

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// ConContinuidadConversacion habilita respuestas explícitas del backend en el sandbox.
// No reenvía solicitudes bloqueadas ni altera las reglas de producción.
func ConContinuidadConversacion() OpcionConsola { return func(c *Consola) { c.continuidad = true } }

func falloRecuperable(err error) bool {
	return errors.Is(err, copilot.ErrBloqueoIA) || errors.Is(err, copilot.ErrRespuestaIA) || errors.Is(err, copilot.ErrTimeoutIA) || errors.Is(err, copilot.ErrNoDisponibleIA) || errors.Is(err, context.DeadlineExceeded)
}

func motivoRecuperacion(err error) string {
	var bloqueo *copilot.ErrorBloqueo
	if errors.As(err, &bloqueo) {
		switch bloqueo.Motivo {
		case copilot.BloqueoEntrada, copilot.BloqueoSeguridad, copilot.BloqueoRecitacion, copilot.BloqueoDatos, copilot.BloqueoOtro, copilot.BloqueoInstrucciones:
			return string(bloqueo.Motivo)
		default:
			return "ResponseBlocked"
		}
	}
	if errors.Is(err, copilot.ErrBloqueoIA) {
		return "ResponseBlocked"
	}
	if errors.Is(err, copilot.ErrTimeoutIA) || errors.Is(err, context.DeadlineExceeded) {
		return "Timeout"
	}
	if errors.Is(err, copilot.ErrRespuestaIA) {
		return "InvalidResponse"
	}
	return "Unavailable"
}

type importeRespaldo struct {
	Centavos *nucleo.Centavos `json:"centavos"`
}

func (i importeRespaldo) texto() string {
	if i.Centavos == nil {
		return ""
	}
	return i.Centavos.PresentarUnidadesMayores() + " COP"
}

// Sólo reutiliza resultados autorizados del turno actual; nunca texto parcial del modelo.
func respuestaDeLecturas(historial []copilot.TurnoIA) string {
	if len(historial) == 0 || historial[len(historial)-1].Rol != "herramienta" {
		return ""
	}
	textos := []string{}
	for _, p := range historial[len(historial)-1].Partes {
		if p.Resultado == nil {
			return ""
		}
		var s struct {
			Datos *struct {
				Disponible, Retenido    importeRespaldo
				MaximoEnviable          importeRespaldo `json:"maximo_enviable"`
				Entradas, Salidas, Neto importeRespaldo
				Cantidad                int
				Movimientos             []struct {
					Fecha, Motivo string
					Monto         importeRespaldo
				}
				HayMas bool `json:"hay_mas"`
			}
		}
		if json.Unmarshal(p.Resultado.Datos, &s) != nil || s.Datos == nil {
			return ""
		}
		d := s.Datos
		switch p.Resultado.Nombre {
		case "consultar_saldo":
			if d.Disponible.texto() == "" || d.Retenido.texto() == "" {
				return ""
			}
			textos = append(textos, "Tu saldo disponible es "+d.Disponible.texto()+" y tienes "+d.Retenido.texto()+" retenidos.")
		case "consultar_limites":
			if d.MaximoEnviable.texto() == "" {
				return ""
			}
			textos = append(textos, "El máximo enviable calculado por el backend es "+d.MaximoEnviable.texto()+". Esto no prepara ni envía dinero.")
		case "resumir_movimientos":
			if d.Entradas.texto() == "" || d.Salidas.texto() == "" || d.Neto.texto() == "" {
				return ""
			}
			textos = append(textos, "Para el rango consultado, las entradas son "+d.Entradas.texto()+", las salidas "+d.Salidas.texto()+" y el neto "+d.Neto.texto()+". Se registraron "+strconv.Itoa(d.Cantidad)+" movimientos.")
		case "listar_movimientos":
			if len(d.Movimientos) == 0 {
				textos = append(textos, "No se encontraron movimientos en el rango consultado.")
			}
			for _, m := range d.Movimientos {
				if m.Monto.texto() == "" {
					return ""
				}
				textos = append(textos, "Movimiento: "+m.Monto.texto()+".")
			}
			if d.HayMas {
				textos = append(textos, "Hay más movimientos fuera de esta página.")
			}
		default:
			return ""
		}
	}
	if len(textos) == 0 {
		return ""
	}
	return "El modelo no pudo completar la redacción. Estos son los datos verificados de tu consulta: " + strings.Join(textos, " ")
}
