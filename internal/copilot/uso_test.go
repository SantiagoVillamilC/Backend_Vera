package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type presupuestoPrueba struct {
	valor int64
	err   error
	clave string
}

func (p *presupuestoPrueba) Leer(_ context.Context, k string) (int64, error) {
	p.clave = k
	return p.valor, p.err
}
func (p *presupuestoPrueba) Reservar(_ context.Context, k string, limite, cantidad int64, _ time.Time) (bool, error) {
	p.clave = k
	if p.err != nil {
		return false, p.err
	}
	if p.valor+cantidad > limite {
		return false, nil
	}
	p.valor += cantidad
	return true, nil
}
func (p *presupuestoPrueba) Conciliar(_ context.Context, _ string, delta int64) error {
	if p.err != nil {
		return p.err
	}
	p.valor += delta
	return nil
}

func controlUsoPrueba(t *testing.T) (*ControlUso, *presupuestoPrueba, context.Context) {
	t.Helper()
	claves, err := auth.NuevasClaves([]byte(strings.Repeat("p", 32)))
	if err != nil {
		t.Fatal(err)
	}
	presupuesto := &presupuestoPrueba{}
	c, err := NuevoControlUso(contadores.EnMemoria(), claves, presupuesto, LimitesUso{TextoHora: 2, TextoDia: 3, VozHora: 1, TokensDiarios: 100, ReservaTokens: 20})
	if err != nil {
		t.Fatal(err)
	}
	c.ahora = func() time.Time { return time.Date(2026, 10, 18, 4, 59, 30, 0, time.UTC) } // 23:59:30 Colombia
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: uuid.NewString(), UsuarioID: uuid.NewString()})
	return c, presupuesto, ctx
}

func TestUsoVentanasCanalesEIdentidad(t *testing.T) {
	c, p, ctx := controlUsoPrueba(t)
	for range 2 {
		if err := c.Admitir(ctx, CanalTexto); err != nil {
			t.Fatal(err)
		}
	}
	var limite *ErrorLimiteUso
	if err := c.Admitir(ctx, CanalTexto); !errors.As(err, &limite) || limite.ReintentarEn != 30*time.Second {
		t.Fatalf("límite: %v", err)
	}
	if err := c.Admitir(ctx, CanalVoz); err != nil {
		t.Fatal(err)
	}
	if err := c.Admitir(ctx, CanalVoz); !errors.Is(err, ErrLimiteUso) {
		t.Fatal(err)
	}
	id, _ := identidad.DeContexto(ctx)
	if strings.Contains(p.clave, id.ParteID) || strings.Contains(p.clave, id.UsuarioID) {
		t.Fatal("Redis expone identidad")
	}
	otro := identidad.ConIdentidad(ctx, identidad.Identidad{ParteID: uuid.NewString()})
	if err := c.Admitir(otro, CanalTexto); err != nil {
		t.Fatal(err)
	}
	c.ahora = func() time.Time { return time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC) }
	if err := c.Admitir(ctx, CanalTexto); err != nil {
		t.Fatal(err)
	}
}

func TestUsoPresupuestoYFalloCerrado(t *testing.T) {
	c, p, ctx := controlUsoPrueba(t)
	p.valor = 100
	if err := c.Admitir(ctx, CanalTexto); !errors.Is(err, ErrLimiteUso) {
		t.Fatal(err)
	}
	proveedor := &proveedorConsentimientoPrueba{}
	protegido := c.ProtegerProveedor(proveedor)
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrLimiteUso) {
		t.Fatal(err)
	}
	p.err = errors.New("detalle privado")
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrNoDisponibleIA) {
		t.Fatal(err)
	}
	if err := c.Admitir(ctx, CanalTexto); !errors.Is(err, ErrNoDisponibleIA) {
		t.Fatal(err)
	}
	if proveedor.llamadas != 0 {
		t.Fatal("se llamó al proveedor sin presupuesto")
	}
	if err := c.Admitir(t.Context(), CanalTexto); !errors.Is(err, ErrConsentimientoAsistente) {
		t.Fatal(err)
	}
}

type proveedorTokens struct {
	respuesta RespuestaIA
	err       error
	llamadas  int
}

func (p *proveedorTokens) Generar(context.Context, SolicitudIA) (RespuestaIA, error) {
	p.llamadas++
	return p.respuesta, p.err
}

func TestUsoConciliaCadaVueltaDelProveedor(t *testing.T) {
	c, p, ctx := controlUsoPrueba(t)
	proveedor := &proveedorTokens{respuesta: RespuestaIA{TokensEntrada: 8, TokensSalida: 2}}
	protegido := c.ProtegerProveedor(proveedor)
	for range 9 {
		if _, err := protegido.Generar(ctx, SolicitudIA{}); err != nil {
			t.Fatal(err)
		}
	}
	if p.valor != 90 {
		t.Fatalf("consumo: %d", p.valor)
	}
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrLimiteUso) {
		t.Fatal(err)
	}
	if proveedor.llamadas != 9 {
		t.Fatal("el límite no bloqueó al proveedor")
	}
	// Sin metadata de consumo (o con error) la reserva se conserva.
	p.valor = 0
	proveedor.respuesta = RespuestaIA{}
	proveedor.err = ErrTimeoutIA
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrTimeoutIA) {
		t.Fatal(err)
	}
	if p.valor != 20 {
		t.Fatalf("se perdió la reserva: %d", p.valor)
	}
}

func TestUsoLimiteDiarioComparteParteEntreSesiones(t *testing.T) {
	c, _, ctx := controlUsoPrueba(t)
	c.ahora = func() time.Time { return time.Date(2026, 10, 18, 15, 0, 0, 0, time.UTC) }
	for range 2 {
		if err := c.Admitir(ctx, CanalTexto); err != nil {
			t.Fatal(err)
		}
	}
	id, _ := identidad.DeContexto(ctx)
	id.UsuarioID = uuid.NewString()
	otraSesion := identidad.ConIdentidad(ctx, id)
	if err := c.Admitir(otraSesion, CanalTexto); !errors.Is(err, ErrLimiteUso) {
		t.Fatal(err)
	}
	// El intento horario rechazado ya consumió la tercera posición del día.
	c.ahora = func() time.Time { return time.Date(2026, 10, 18, 16, 0, 0, 0, time.UTC) }
	var limite *ErrorLimiteUso
	if err := c.Admitir(ctx, CanalTexto); !errors.As(err, &limite) || limite.ReintentarEn != 13*time.Hour {
		t.Fatalf("día: %v", err)
	}
	c.ahora = func() time.Time { return time.Date(2026, 10, 19, 5, 0, 0, 0, time.UTC) }
	if err := c.Admitir(ctx, CanalTexto); err != nil {
		t.Fatal(err)
	}
}

type contadorUsoCaido struct{ auth.Contadores }

func (contadorUsoCaido) Incrementar(context.Context, string, time.Duration) (int64, error) {
	return 0, errors.New("privado")
}

func TestUsoCierraAnteFalloDelContador(t *testing.T) {
	c, _, ctx := controlUsoPrueba(t)
	c.contadores = contadorUsoCaido{}
	if err := c.Admitir(ctx, CanalTexto); !errors.Is(err, ErrNoDisponibleIA) {
		t.Fatal(err)
	}
	cancelado, cancelar := context.WithCancel(ctx)
	cancelar()
	if err := c.Admitir(cancelado, CanalTexto); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestUsoNoOcultaExcesoReal(t *testing.T) {
	c, p, ctx := controlUsoPrueba(t)
	proveedor := &proveedorTokens{respuesta: RespuestaIA{TokensEntrada: 100, TokensSalida: 50}}
	protegido := c.ProtegerProveedor(proveedor)
	if _, err := protegido.Generar(ctx, SolicitudIA{}); err != nil {
		t.Fatal(err)
	}
	if p.valor != 150 {
		t.Fatalf("consumo real truncado: %d", p.valor)
	}
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrLimiteUso) {
		t.Fatal(err)
	}
	if proveedor.llamadas != 1 {
		t.Fatal("se ignoró el presupuesto")
	}
}
