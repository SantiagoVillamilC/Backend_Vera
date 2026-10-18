package copilot

import (
	"context"
	"errors"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type lectorConsentimientoPrueba struct {
	vigente  bool
	err      error
	parte    uuid.UUID
	llamadas int
}

func (l *lectorConsentimientoPrueba) ConsentimientoAsistenteVigente(_ context.Context, id uuid.UUID) (bool, error) {
	l.parte = id
	l.llamadas++
	return l.vigente, l.err
}

type canceladorConsentimientoPrueba struct {
	llamadas      int
	parte, accion uuid.UUID
	err           error
}

func (c *canceladorConsentimientoPrueba) CancelarPorConsentimiento(_ context.Context, p, a uuid.UUID) error {
	c.llamadas++
	c.parte = p
	c.accion = a
	return c.err
}

type proveedorConsentimientoPrueba struct{ llamadas int }

func (p *proveedorConsentimientoPrueba) Generar(context.Context, SolicitudIA) (RespuestaIA, error) {
	p.llamadas++
	return RespuestaIA{}, nil
}

func TestConsentimientoProtegeCadaLlamada(t *testing.T) {
	parte := uuid.New()
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{UsuarioID: uuid.NewString(), ParteID: parte.String()})
	lector := &lectorConsentimientoPrueba{}
	proveedor := &proveedorConsentimientoPrueba{}
	control := NuevoControlConsentimiento(lector, nil)
	protegido := control.ProtegerProveedor(proveedor)
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrConsentimientoAsistente) {
		t.Fatal(err)
	}
	lector.vigente = true
	if _, err := protegido.Generar(ctx, SolicitudIA{}); err != nil {
		t.Fatal(err)
	}
	lector.vigente = false
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrConsentimientoAsistente) {
		t.Fatal(err)
	}
	if proveedor.llamadas != 1 || lector.llamadas != 3 || lector.parte != parte {
		t.Fatal("se omitió autorización o se usó usuario en lugar de parte")
	}
	lector.err = errors.New("error privado")
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, ErrNoDisponibleIA) {
		t.Fatal(err)
	}
	if proveedor.llamadas != 1 {
		t.Fatal("fallo abierto ante caída de auth")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := protegido.Generar(ctx, SolicitudIA{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestConsentimientoCancelaSoloAlDenegar(t *testing.T) {
	parte, accion := uuid.New(), uuid.New()
	ctx := identidad.ConIdentidad(t.Context(), identidad.Identidad{ParteID: parte.String()})
	for nombre, caso := range map[string]struct {
		vigente              bool
		lectura, cancelacion error
		esperado             error
		canceladas           int
	}{
		"revocado":         {false, nil, nil, ErrConsentimientoAsistente, 1},
		"aceptado":         {true, nil, nil, nil, 0},
		"caidaAuth":        {false, errors.New("privado"), nil, ErrNoDisponibleIA, 0},
		"caidaCancelacion": {false, nil, errors.New("privado"), ErrNoDisponibleIA, 1},
	} {
		t.Run(nombre, func(t *testing.T) {
			cancelador := &canceladorConsentimientoPrueba{err: caso.cancelacion}
			c := NuevoControlConsentimiento(&lectorConsentimientoPrueba{vigente: caso.vigente, err: caso.lectura}, cancelador)
			err := c.ExigirConfirmacion(ctx, accion)
			if !errors.Is(err, caso.esperado) || cancelador.llamadas != caso.canceladas {
				t.Fatalf("err=%v canceladas=%d", err, cancelador.llamadas)
			}
			if cancelador.llamadas > 0 && (cancelador.parte != parte || cancelador.accion != accion) {
				t.Fatal("identidad o acción incorrecta")
			}
		})
	}
	c := NuevoControlConsentimiento(nil, nil)
	if err := c.Exigir(t.Context()); !errors.Is(err, ErrConsentimientoAsistente) {
		t.Fatal(err)
	}
	if err := c.Exigir(ctx); !errors.Is(err, ErrNoDisponibleIA) {
		t.Fatal(err)
	}
}
