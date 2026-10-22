package copilot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type contadorDestinatarioPrueba struct {
	n   int64
	err error
}

func (c *contadorDestinatarioPrueba) Incrementar(context.Context, string, time.Duration) (int64, error) {
	c.n++
	return c.n, c.err
}
func (*contadorDestinatarioPrueba) Bloquear(context.Context, string, time.Duration) error { return nil }
func (*contadorDestinatarioPrueba) Bloqueada(context.Context, string) (bool, error) {
	return false, nil
}

type buscadorDestinatarioPrueba struct {
	d             auth.DestinatarioIA
	existe        bool
	llamadas      int
	usuario       string
	paginas       int
	coincidencias []auth.DestinatarioIA
}

func (b *buscadorDestinatarioPrueba) DestinatarioPorCelularIA(_ context.Context, celular string) (auth.DestinatarioIA, bool, error) {
	b.llamadas++
	if celular != "+573001234567" {
		return auth.DestinatarioIA{}, false, ErrHerramientaValidacion
	}
	return b.d, b.existe, nil
}
func (b *buscadorDestinatarioPrueba) DestinatariosPrevios(_ context.Context, usuario, cursor string) ([]string, error) {
	b.usuario = usuario
	b.paginas++
	if cursor == "" {
		ids := make([]string, 100)
		for i := range ids {
			ids[i] = fmt.Sprintf("%03d", i+1)
		}
		return ids, nil
	}
	return []string{"101"}, nil
}
func (b *buscadorDestinatarioPrueba) BuscarDestinatariosPrevios(_ context.Context, ids []string, _ string) ([]auth.DestinatarioIA, error) {
	if len(ids) == 100 {
		return nil, nil
	}
	return b.coincidencias, nil
}

func TestBusquedaDestinatarioErroresLimiteYContexto(t *testing.T) {
	usuario, conv := uuid.NewString(), uuid.New()
	id := identidad.Identidad{UsuarioID: usuario, ParteID: uuid.NewString()}
	ctx := identidad.ConIdentidad(context.WithValue(t.Context(), claveConversacionCiclo{}, conv), id)
	refs, _ := NuevasReferenciasDestinatario(bytes.Repeat([]byte{7}, 32))
	claves, _ := auth.NuevasClaves(bytes.Repeat([]byte{7}, 32))
	for nombre, caso := range map[string]struct {
		existe, propio, redisCaido, agotado bool
		codigo                              string
	}{
		"encontrado": {existe: true, codigo: "OK"}, "no encontrado": {codigo: "RecipientNotFound"}, "propio": {existe: true, propio: true, codigo: "SelfTransferNotAllowed"}, "redis caído": {redisCaido: true, codigo: "FunctionUnavailable"}, "límite": {agotado: true, codigo: "TooManyRequests"},
	} {
		t.Run(nombre, func(t *testing.T) {
			contador := &contadorDestinatarioPrueba{}
			if caso.redisCaido {
				contador.err = errors.New("redis")
			}
			if caso.agotado {
				contador.n = 1
			}
			b := &buscadorDestinatarioPrueba{existe: caso.existe, d: auth.DestinatarioIA{ID: uuid.NewString(), Nombre: "Laura R.", Celular: "+573001234567"}}
			if caso.propio {
				b.d.ID = usuario
			}
			h := ConsultaDestinatarioIA(b, b, auth.NuevoLimiteDestinatarios(contador, claves, 1, time.Hour), refs)
			catalogo, err := NuevoCatalogo(map[string]ManejadorHerramienta{"buscar_destinatario": h}, nil)
			if err != nil {
				t.Fatal(err)
			}
			r, err := catalogo.Ejecutar(ctx, LlamadaIA{Nombre: "buscar_destinatario", Argumentos: []byte(`{"celular":"300 123 4567"}`)})
			if err != nil {
				t.Fatal(err)
			}
			if caso.codigo == "OK" {
				if !bytes.Contains(r.Datos, []byte("destinatario_ref")) || bytes.Contains(r.Datos, []byte(b.d.ID)) || bytes.Contains(r.Datos, []byte(b.d.Celular)) {
					t.Fatalf("datos inseguros %s", r.Datos)
				}
			} else if !bytes.Contains(r.Datos, []byte(caso.codigo)) {
				t.Fatalf("%s", r.Datos)
			}
			if (caso.redisCaido || caso.agotado) && b.llamadas != 0 {
				t.Fatal("consultó auth después del rechazo")
			}
			if _, err := h(t.Context(), id, ArgumentosHerramienta{Celular: "+573001234567"}); !errors.Is(err, ErrHerramientaValidacion) {
				t.Fatal("aceptó sin conversación")
			}
		})
	}
}

func TestBusquedaNombrePaginaSinCoincidenciasYTopeCinco(t *testing.T) {
	usuario, conv := uuid.NewString(), uuid.New()
	id := identidad.Identidad{UsuarioID: usuario}
	ctx := context.WithValue(t.Context(), claveConversacionCiclo{}, conv)
	refs, _ := NuevasReferenciasDestinatario(bytes.Repeat([]byte{7}, 32))
	claves, _ := auth.NuevasClaves(bytes.Repeat([]byte{7}, 32))
	for _, cantidad := range []int{0, 1, 6} {
		t.Run(fmt.Sprint(cantidad), func(t *testing.T) {
			b := &buscadorDestinatarioPrueba{}
			contador := &contadorDestinatarioPrueba{err: errors.New("redis caído")}
			for range cantidad {
				b.coincidencias = append(b.coincidencias, auth.DestinatarioIA{ID: uuid.NewString(), Nombre: "Laura R.", Celular: "+573001234567"})
			}
			h := ConsultaDestinatarioIA(b, b, auth.NuevoLimiteDestinatarios(contador, claves, 1, time.Hour), refs)
			datos, err := h(ctx, id, ArgumentosHerramienta{Nombre: "Laura"})
			if err != nil {
				t.Fatal(err)
			}
			d := datos.(DestinatariosHerramienta)
			if b.usuario != usuario || b.paginas != 2 || len(d.Destinatarios) != min(cantidad, 5) || d.HayMas != (cantidad > 5) || contador.n != 0 {
				t.Fatalf("paginación/tope/contador: %+v %+v", b, d)
			}
			for _, dest := range d.Destinatarios {
				if _, err := refs.Resolver(usuario, conv, dest.Referencia); err != nil {
					t.Fatal(err)
				}
			}
			ctxCancelado, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := h(ctxCancelado, id, ArgumentosHerramienta{Nombre: "Laura"}); !errors.Is(err, context.Canceled) {
				t.Fatal("no respetó cancelación")
			}
		})
	}
}
