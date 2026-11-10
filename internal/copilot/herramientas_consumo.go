package copilot

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// LectorResumenConsumoIA sólo expone el gasto del mes agrupado por categoría.
type LectorResumenConsumoIA interface {
	Resumen(context.Context, string, string, time.Time) (inteligencia.ResumenDelMes, error)
}

// lectorConsumoAusente también descarta un puntero tipado nulo, que no es igual a nil en la interfaz.
func lectorConsumoAusente(lector LectorResumenConsumoIA) bool {
	if lector == nil {
		return true
	}
	v := reflect.ValueOf(lector)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// ResumenConsumoIA usa la identidad autenticada y el reloj del servidor, nunca el del modelo.
func ResumenConsumoIA(lector LectorResumenConsumoIA) ManejadorHerramienta {
	return func(ctx context.Context, id identidad.Identidad, a ArgumentosHerramienta) (DatosHerramienta, error) {
		if lectorConsumoAusente(lector) {
			return nil, ErrHerramientaNoDisponible
		}
		r, err := lector.Resumen(ctx, id.UsuarioID, a.Mes, time.Now().UTC())
		if err != nil {
			if errors.Is(err, inteligencia.ErrMesInvalido) {
				return nil, ErrHerramientaValidacion
			}
			return nil, err
		}
		salida := ConsumoHerramienta{
			Mes:             r.Mes,
			Alcance:         r.Alcance,
			Moneda:          r.Moneda,
			Evaluable:       r.Evaluable,
			MesesCompletos:  r.MesesCompletos,
			MesesRequeridos: r.MesesRequeridos,
			Total:           nucleo.Centavos(r.TotalCentavos),
			HayMas:          len(r.Categorias) > 10,
			Categorias:      make([]CategoriaConsumoHerramienta, 0, len(r.Categorias)),
		}
		for _, c := range r.Categorias {
			categoria := CategoriaConsumoHerramienta{Codigo: c.Codigo, Nombre: c.Nombre, Total: nucleo.Centavos(c.TotalCentavos)}
			if c.MedianaCentavos != nil {
				mediana := nucleo.Centavos(*c.MedianaCentavos)
				categoria.Mediana = &mediana
			}
			salida.Categorias = append(salida.Categorias, categoria)
		}
		return salida, nil
	}
}
