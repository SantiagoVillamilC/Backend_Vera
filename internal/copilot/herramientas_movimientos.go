package copilot

import (
	"context"
	"errors"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// LectorPeriodoIA sólo permite leer movimientos propios y agregados del núcleo.
type LectorPeriodoIA interface {
	MovimientosEnRango(context.Context, string, string, string, nucleo.MotivoMovimiento, int) (nucleo.PaginaPeriodo, error)
	ResumirMovimientos(context.Context, string, string, string, string) (nucleo.ResumenPeriodo, error)
}

// LectorNombresContrapartes resuelve sólo las referencias obtenidas del historial autorizado.
type LectorNombresContrapartes interface {
	NombresDeContrapartes(context.Context, []string) (map[string]string, error)
}

func nombresDelPeriodo(ctx context.Context, lector LectorNombresContrapartes, refs []string) (map[string]string, error) {
	unicas := make([]string, 0, len(refs))
	vistas := make(map[string]bool)
	for _, ref := range refs {
		if ref != "" && !vistas[ref] {
			unicas = append(unicas, ref)
			vistas[ref] = true
		}
	}
	if len(unicas) == 0 {
		return map[string]string{}, nil
	}
	if lector == nil {
		return nil, ErrHerramientaNoDisponible
	}
	return lector.NombresDeContrapartes(ctx, unicas)
}

func errorPeriodo(err error) error {
	if errors.Is(err, nucleo.ErrRangoMovimientos) {
		return ErrHerramientaValidacion
	}
	return err
}

// ConsultaMovimientosIA muestra un asiento por fila y mantiene sus componentes sin duplicar el retenido.
func ConsultaMovimientosIA(lector LectorPeriodoIA, nombres LectorNombresContrapartes) ManejadorHerramienta {
	return func(ctx context.Context, id identidad.Identidad, a ArgumentosHerramienta) (DatosHerramienta, error) {
		if lector == nil {
			return nil, ErrHerramientaNoDisponible
		}
		p, err := lector.MovimientosEnRango(ctx, id.UsuarioID, a.Desde, a.Hasta, nucleo.MotivoMovimiento(a.Tipo), a.Limite)
		if err != nil {
			return nil, errorPeriodo(err)
		}
		refs := make([]string, 0, len(p.Movimientos))
		for _, m := range p.Movimientos {
			refs = append(refs, m.ContraparteRef)
		}
		etiquetas, err := nombresDelPeriodo(ctx, nombres, refs)
		if err != nil {
			return nil, err
		}
		r := MovimientosHerramienta{Movimientos: make([]MovimientoHerramienta, 0, len(p.Movimientos)), Total: p.Total, HayMas: p.HayMas}
		for _, m := range p.Movimientos {
			if len(m.Lineas) == 0 {
				continue
			}
			var total nucleo.Centavos
			for _, l := range m.Lineas {
				total, err = total.Sumar(l.Monto)
				if err != nil {
					return nil, err
				}
			}
			r.Movimientos = append(r.Movimientos, MovimientoHerramienta{Fecha: m.Fecha, Motivo: string(m.Lineas[0].Motivo), Monto: total, Contraparte: etiquetas[m.ContraparteRef], Componentes: m.Lineas})
		}
		return r, nil
	}
}

// ResumenMovimientosIA proyecta totales completos y grupos calculados antes de enmascarar sus etiquetas.
func ResumenMovimientosIA(lector LectorPeriodoIA, nombres LectorNombresContrapartes) ManejadorHerramienta {
	return func(ctx context.Context, id identidad.Identidad, a ArgumentosHerramienta) (DatosHerramienta, error) {
		if lector == nil {
			return nil, ErrHerramientaNoDisponible
		}
		r, err := lector.ResumirMovimientos(ctx, id.UsuarioID, a.Desde, a.Hasta, a.AgruparPor)
		if err != nil {
			return nil, errorPeriodo(err)
		}
		etiquetas := make(map[string]string)
		if a.AgruparPor == "contraparte" {
			refs := make([]string, 0, len(r.Grupos))
			for _, g := range r.Grupos {
				refs = append(refs, g.Clave)
			}
			etiquetas, err = nombresDelPeriodo(ctx, nombres, refs)
			if err != nil {
				return nil, err
			}
		}
		salida := ResumenHerramienta{Entradas: r.Entradas, Salidas: r.Salidas, Neto: r.Neto, Cantidad: r.Cantidad, HayMas: r.HayMas, Grupos: make([]GrupoHerramienta, 0, len(r.Grupos))}
		for _, g := range r.Grupos {
			nombre := g.Clave
			if a.AgruparPor == "contraparte" {
				nombre = etiquetas[g.Clave]
				if nombre == "" {
					nombre = "Contraparte no disponible"
					if g.Clave == "" {
						nombre = "Sin contraparte"
					}
				}
			}
			salida.Grupos = append(salida.Grupos, GrupoHerramienta{Nombre: nombre, Entradas: g.Entradas, Salidas: g.Salidas, Neto: g.Neto, Cantidad: g.Cantidad})
		}
		return salida, nil
	}
}
