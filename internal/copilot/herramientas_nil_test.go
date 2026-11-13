package copilot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

// Punteros tipados nulos: cada uno satisface su interfaz y conserva el tipo, que es justo el nil que
// no se detecta con == nil.

type saldoNulo struct{}

func (*saldoNulo) Saldo(context.Context, string) (nucleo.Centavos, error) {
	return 0, nil
}

type limitesNulo struct{}

func (*limitesNulo) DesgloseLimites(context.Context, string, time.Time) (nucleo.DesgloseLimites, error) {
	return nucleo.DesgloseLimites{}, nil
}

type periodoNulo struct{}

func (*periodoNulo) MovimientosEnRango(context.Context, string, string, string, nucleo.MotivoMovimiento, int) (nucleo.PaginaPeriodo, error) {
	return nucleo.PaginaPeriodo{}, nil
}

func (*periodoNulo) ResumirMovimientos(context.Context, string, string, string, string) (nucleo.ResumenPeriodo, error) {
	return nucleo.ResumenPeriodo{}, nil
}

type nombresNulo struct{}

func (*nombresNulo) NombresDeContrapartes(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

type consumoNulo struct{}

func (*consumoNulo) Resumen(context.Context, string, string, time.Time) (inteligencia.ResumenDelMes, error) {
	return inteligencia.ResumenDelMes{}, nil
}

type transferenciaNula struct{}

func (*transferenciaNula) ConsultarTransferencia(context.Context, string, uuid.UUID) (nucleo.Transferencia, error) {
	return nucleo.Transferencia{}, nil
}

// Lectores que devuelven error: la herramienta tiene que propagarlo o mapearlo, nunca devolver ok vacío.

var errFallaDeLaBase = errors.New("la base no responde")

type saldoConFalla struct{}

func (*saldoConFalla) Saldo(context.Context, string) (nucleo.Centavos, error) {
	return 0, errFallaDeLaBase
}

type limitesConFalla struct{}

func (*limitesConFalla) DesgloseLimites(context.Context, string, time.Time) (nucleo.DesgloseLimites, error) {
	return nucleo.DesgloseLimites{}, errFallaDeLaBase
}

type consumoConFalla struct{}

func (*consumoConFalla) Resumen(context.Context, string, string, time.Time) (inteligencia.ResumenDelMes, error) {
	return inteligencia.ResumenDelMes{}, errFallaDeLaBase
}

type transferenciaSinRegistro struct{}

func (*transferenciaSinRegistro) ConsultarTransferencia(context.Context, string, uuid.UUID) (nucleo.Transferencia, error) {
	return nucleo.Transferencia{}, nucleo.ErrTransferenciaNoEncontrada
}

func TestHerramientasRespondenIgualAlPunteroNuloTipado(t *testing.T) {
	t.Parallel()

	id := identidad.Identidad{UsuarioID: uuid.NewString()}
	casos := []struct {
		nombre  string
		handler ManejadorHerramienta
	}{
		{"saldo", ConsultaSaldoIA((*saldoNulo)(nil))},
		{"limites", ConsultaLimitesIA((*limitesNulo)(nil))},
		{"movimientos", ConsultaMovimientosIA((*periodoNulo)(nil), (*nombresNulo)(nil))},
		{"resumen de movimientos", ResumenMovimientosIA((*periodoNulo)(nil), (*nombresNulo)(nil))},
		{"transferencia", ConsultaTransferenciaIA((*transferenciaNula)(nil))},
		{"consumo", ResumenConsumoIA((*consumoNulo)(nil))},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			datos, err := caso.handler(t.Context(), id, ArgumentosHerramienta{})
			if !errors.Is(err, ErrHerramientaNoDisponible) {
				t.Fatalf("err = %v, quiero ErrHerramientaNoDisponible", err)
			}
			if datos != nil {
				t.Fatalf("datos = %v, quiero nil", datos)
			}
		})
	}
}

func TestHerramientasPropaganElErrorTipadoDelLector(t *testing.T) {
	t.Parallel()

	id := identidad.Identidad{UsuarioID: uuid.NewString()}
	casos := []struct {
		nombre   string
		handler  ManejadorHerramienta
		args     ArgumentosHerramienta
		esperado error
	}{
		{"saldo", ConsultaSaldoIA(&saldoConFalla{}), ArgumentosHerramienta{}, errFallaDeLaBase},
		{"limites", ConsultaLimitesIA(&limitesConFalla{}), ArgumentosHerramienta{}, errFallaDeLaBase},
		{"consumo", ResumenConsumoIA(&consumoConFalla{}), ArgumentosHerramienta{}, errFallaDeLaBase},
		{"transferencia no encontrada", ConsultaTransferenciaIA(&transferenciaSinRegistro{}), ArgumentosHerramienta{TransferenciaID: uuid.NewString()}, ErrHerramientaNoEncontrado},
	}

	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			datos, err := caso.handler(t.Context(), id, caso.args)
			if !errors.Is(err, caso.esperado) {
				t.Fatalf("err = %v, quiero %v", err, caso.esperado)
			}
			if datos != nil {
				t.Fatalf("datos = %v, quiero nil", datos)
			}
		})
	}
}

func TestNombresDelPeriodoDescartaElPunteroNulo(t *testing.T) {
	t.Parallel()

	if _, err := nombresDelPeriodo(t.Context(), (*nombresNulo)(nil), []string{"ref-1"}); !errors.Is(err, ErrHerramientaNoDisponible) {
		t.Fatalf("err = %v, quiero ErrHerramientaNoDisponible", err)
	}

	if etiquetas, err := nombresDelPeriodo(t.Context(), (*nombresNulo)(nil), nil); err != nil || len(etiquetas) != 0 {
		t.Fatalf("sin referencias: etiquetas = %v, err = %v, quiero mapa vacío y sin error", etiquetas, err)
	}
}
