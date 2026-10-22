package copilot

import (
	"context"
	"errors"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// HistorialDestinatariosIA sólo lee receptores de transferencias propias.
type HistorialDestinatariosIA interface {
	DestinatariosPrevios(context.Context, string, string) ([]string, error)
}

// BuscadorDestinatariosIA mantiene las consultas de identidad dentro de auth.
type BuscadorDestinatariosIA interface {
	DestinatarioPorCelularIA(context.Context, string) (auth.DestinatarioIA, bool, error)
	BuscarDestinatariosPrevios(context.Context, []string, string) ([]auth.DestinatarioIA, error)
}

// ConsultaDestinatarioIA no ejecuta ni prepara dinero, sólo devuelve candidatos y referencias.
func ConsultaDestinatarioIA(historial HistorialDestinatariosIA, buscador BuscadorDestinatariosIA, limite *auth.LimiteDestinatarios, refs *ReferenciasDestinatario) ManejadorHerramienta {
	return func(ctx context.Context, id identidad.Identidad, a ArgumentosHerramienta) (DatosHerramienta, error) {
		conv, ok := ConversacionDelContexto(ctx)
		if !ok || id.UsuarioID == "" {
			return nil, ErrHerramientaValidacion
		}
		if historial == nil || buscador == nil || limite == nil || refs == nil {
			return nil, ErrHerramientaNoDisponible
		}
		candidatos := make([]auth.DestinatarioIA, 0, 6)
		if a.Celular != "" {
			if err := limite.Exigir(ctx, id.UsuarioID); err != nil {
				if errors.Is(err, auth.ErrConsultaDestinatariosLimitada) {
					return nil, ErrHerramientaLimite
				}
				return nil, ErrHerramientaNoDisponible
			}
			d, existe, err := buscador.DestinatarioPorCelularIA(ctx, a.Celular)
			if err != nil {
				return nil, err
			}
			if !existe {
				return nil, ErrHerramientaDestinatarioNoEncontrado
			}
			if d.ID == id.UsuarioID {
				return nil, ErrHerramientaDestinoPropio
			}
			candidatos = append(candidatos, d)
		} else {
			cursor := ""
			for {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				ids, err := historial.DestinatariosPrevios(ctx, id.UsuarioID, cursor)
				if err != nil {
					return nil, err
				}
				if len(ids) == 0 {
					break
				}
				filas, err := buscador.BuscarDestinatariosPrevios(ctx, ids, a.Nombre)
				if err != nil {
					return nil, err
				}
				candidatos = append(candidatos, filas...)
				if len(candidatos) > 5 || len(ids) < 100 {
					break
				}
				siguiente := ids[len(ids)-1]
				if siguiente <= cursor {
					return nil, ErrHerramientaNoDisponible
				}
				cursor = siguiente
			}
		}
		salida := DestinatariosHerramienta{Destinatarios: make([]DestinatarioHerramienta, 0, 5), HayMas: len(candidatos) > 5}
		for _, d := range candidatos[:min(5, len(candidatos))] {
			token, err := refs.Emitir(id.UsuarioID, conv, d.ID)
			if err != nil {
				return nil, err
			}
			salida.Destinatarios = append(salida.Destinatarios, DestinatarioHerramienta{Nombre: d.Nombre, Celular: d.Celular, Referencia: token})
		}
		return salida, nil
	}
}
