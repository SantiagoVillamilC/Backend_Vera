package copilot

import (
	"context"
	"errors"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

// LectorTransferenciaIA sólo permite consultar; no expone métodos que muevan dinero.
type LectorTransferenciaIA interface {
	ConsultarTransferencia(context.Context, string, uuid.UUID) (nucleo.Transferencia, error)
}

// ConsultaTransferenciaIA proyecta una transferencia propia a su vista mínima.
func ConsultaTransferenciaIA(lector LectorTransferenciaIA) ManejadorHerramienta {
	return func(ctx context.Context, id identidad.Identidad, args ArgumentosHerramienta) (DatosHerramienta, error) {
		if lector == nil {
			return nil, ErrHerramientaNoDisponible
		}
		transferenciaID, err := uuid.Parse(args.TransferenciaID)
		if err != nil {
			return nil, ErrHerramientaValidacion
		}
		tr, err := lector.ConsultarTransferencia(ctx, id.UsuarioID, transferenciaID)
		if errors.Is(err, nucleo.ErrTransferenciaNoEncontrada) {
			return nil, ErrHerramientaNoEncontrado
		}
		if err != nil {
			return nil, err
		}
		// Defensa adicional si cambia la implementación del puerto de lectura.
		if tr.UsuarioID != id.UsuarioID {
			return nil, ErrHerramientaNoEncontrado
		}
		return TransferenciaHerramienta{Estado: tr.Estado, Monto: tr.Monto, CreadaEn: tr.CreadaEn, ActualizadaEn: tr.ActualizadaEn}, nil
	}
}
