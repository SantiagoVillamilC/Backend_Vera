package copilot

import (
	"context"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// LectorSaldoIA sólo expone la lectura del ledger con el signo público del usuario.
type LectorSaldoIA interface {
	Saldo(context.Context, string) (nucleo.Centavos, error)
}

// LectorLimitesIA obtiene el desglose del motor de límites, sin operaciones de dinero.
type LectorLimitesIA interface {
	DesgloseLimites(context.Context, string, time.Time) (nucleo.DesgloseLimites, error)
}

// ConsultaSaldoIA deriva ambas cuentas de la identidad autenticada, nunca de argumentos IA.
func ConsultaSaldoIA(lector LectorSaldoIA) ManejadorHerramienta {
	return func(ctx context.Context, id identidad.Identidad, _ ArgumentosHerramienta) (DatosHerramienta, error) {
		if lector == nil {
			return nil, ErrHerramientaNoDisponible
		}
		disponible, err := lector.Saldo(ctx, nucleo.NombreCuentaDisponible(id.UsuarioID))
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		retenido, err := lector.Saldo(ctx, nucleo.NombreCuentaRetenido(id.UsuarioID))
		if err != nil {
			return nil, err
		}
		return SaldoHerramienta{Disponible: disponible, Retenido: retenido}, nil
	}
}

// ConsultaLimitesIA usa el reloj del servidor y el mismo usuario que el endpoint del núcleo.
func ConsultaLimitesIA(lector LectorLimitesIA) ManejadorHerramienta {
	return func(ctx context.Context, id identidad.Identidad, _ ArgumentosHerramienta) (DatosHerramienta, error) {
		if lector == nil {
			return nil, ErrHerramientaNoDisponible
		}
		d, err := lector.DesgloseLimites(ctx, id.UsuarioID, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		return LimitesHerramienta{MaximoEnviable: d.MaximoEnviable, SaldoDespuesGMF: d.SaldoDespuesGMF, TopeOperacion: d.TopeOperacion, CupoMensualRestante: d.CupoMensualRestante, GMF: d.GMF, AplicaGMF: d.AplicaGMF}, nil
	}
}
