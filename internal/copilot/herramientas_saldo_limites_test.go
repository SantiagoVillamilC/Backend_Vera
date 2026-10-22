package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type lectorSaldoPrueba struct {
	cuentas []string
	err     error
}

func (l *lectorSaldoPrueba) Saldo(_ context.Context, cuenta string) (nucleo.Centavos, error) {
	l.cuentas = append(l.cuentas, cuenta)
	if strings.HasSuffix(cuenta, ":retenido") {
		return 100, l.err
	}
	return 12345, l.err
}

type lectorLimitesPrueba struct {
	usuario string
	momento time.Time
	err     error
}

func (l *lectorLimitesPrueba) DesgloseLimites(_ context.Context, usuario string, momento time.Time) (nucleo.DesgloseLimites, error) {
	l.usuario = usuario
	l.momento = momento
	return nucleo.DesgloseLimites{MaximoEnviable: 100, SaldoDespuesGMF: 150, TopeOperacion: 200, CupoMensualRestante: 100, GMF: 1, AplicaGMF: true}, l.err
}

func TestHerramientasSaldoLimitesIdentidadYProyeccion(t *testing.T) {
	saldo, limites := &lectorSaldoPrueba{}, &lectorLimitesPrueba{}
	c, err := NuevoCatalogo(map[string]ManejadorHerramienta{"consultar_saldo": ConsultaSaldoIA(saldo), "consultar_limites": ConsultaLimitesIA(limites)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := identidad.Identidad{UsuarioID: uuid.NewString(), ParteID: uuid.NewString()}
	ctx := identidad.ConIdentidad(t.Context(), id)
	inicio := time.Now()
	for _, nombre := range []string{"consultar_saldo", "consultar_limites"} {
		r, err := c.Ejecutar(ctx, LlamadaIA{ID: "lectura", Nombre: nombre, Argumentos: []byte(`{}`)})
		if err != nil || !strings.Contains(string(r.Datos), `"moneda":"COP"`) {
			t.Fatalf("resultado: %s %v", r.Datos, err)
		}
		if strings.Contains(string(r.Datos), id.UsuarioID) || strings.Contains(string(r.Datos), id.ParteID) {
			t.Fatal("identidad expuesta")
		}
		if nombre == "consultar_limites" && (!strings.Contains(string(r.Datos), `"saldo_despues_gmf":{"centavos":"150"`) || !strings.Contains(string(r.Datos), `"aplica_gmf":true`)) {
			t.Fatal(string(r.Datos))
		}
	}
	if len(saldo.cuentas) != 2 || saldo.cuentas[0] != nucleo.NombreCuentaDisponible(id.UsuarioID) || saldo.cuentas[1] != nucleo.NombreCuentaRetenido(id.UsuarioID) || limites.usuario != id.UsuarioID || limites.momento.Before(inicio) || limites.momento.After(time.Now()) {
		t.Fatal("no se usaron identidad y reloj del servidor")
	}
	antes := len(saldo.cuentas)
	r, err := c.Ejecutar(ctx, LlamadaIA{Nombre: "consultar_saldo", Argumentos: []byte(`{"usuario_id":"otra-persona"}`)})
	if err != nil || !strings.Contains(string(r.Datos), "ValidationError") || len(saldo.cuentas) != antes {
		t.Fatal("se aceptó una identidad del modelo")
	}
}

func TestHerramientasSaldoLimitesFalloCerrado(t *testing.T) {
	for _, fallo := range []error{nucleo.ErrValorUVTNoVigente, nucleo.ErrReglaLimiteNoVigente, nucleo.ErrReglaGMFNoVigente, errors.New("detalle sensible")} {
		c, err := NuevoCatalogo(map[string]ManejadorHerramienta{"consultar_limites": ConsultaLimitesIA(&lectorLimitesPrueba{err: fallo})}, nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{UsuarioID: uuid.NewString()})
		r, err := c.Ejecutar(ctx, LlamadaIA{Nombre: "consultar_limites", Argumentos: []byte(`{}`)})
		if err != nil || !strings.Contains(string(r.Datos), "FunctionUnavailable") || strings.Contains(string(r.Datos), "sensible") {
			t.Fatalf("error sin sanear: %s %v", r.Datos, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ConsultaSaldoIA(&lectorSaldoPrueba{})(ctx, identidad.Identidad{UsuarioID: uuid.NewString()}, ArgumentosHerramienta{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
