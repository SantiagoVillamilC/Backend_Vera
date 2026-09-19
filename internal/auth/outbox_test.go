package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPoliticaReintentoDemora(t *testing.T) {
	t.Parallel()

	politica := PoliticaReintento{Base: 5 * time.Second, Factor: 2, Tope: 5 * time.Minute}
	casos := []struct {
		intentos int
		want     time.Duration
	}{
		{intentos: 0, want: 0},
		{intentos: -1, want: 0},
		{intentos: 1, want: 5 * time.Second},
		{intentos: 2, want: 10 * time.Second},
		{intentos: 3, want: 20 * time.Second},
		{intentos: 4, want: 40 * time.Second},
		{intentos: 5, want: 80 * time.Second},
		{intentos: 6, want: 160 * time.Second},
		{intentos: 7, want: 5 * time.Minute},
		{intentos: 8, want: 5 * time.Minute},
	}
	for _, caso := range casos {
		if got := politica.Demora(caso.intentos); got != caso.want {
			t.Errorf("Demora(%d) = %s, quiero %s", caso.intentos, got, caso.want)
		}
	}
}

func TestPoliticaReintentoVaciaUsaLaDePorDefecto(t *testing.T) {
	t.Parallel()

	var vacia PoliticaReintento
	if got := vacia.Demora(1); got != PoliticaReintentoPorDefecto().Base {
		t.Errorf("Demora(1) de la política vacía = %s, quiero %s", got, PoliticaReintentoPorDefecto().Base)
	}
}

// almacenAvisosFalso es el outbox en memoria para el test unitario del despachador.
type almacenAvisosFalso struct {
	filas      []avisoReclamado
	enEspera   map[int64]bool
	entregados map[int64]bool
	fallos     map[int64]string
}

func nuevoAlmacenAvisosFalso(filas ...avisoReclamado) *almacenAvisosFalso {
	return &almacenAvisosFalso{
		filas:      filas,
		enEspera:   map[int64]bool{},
		entregados: map[int64]bool{},
		fallos:     map[int64]string{},
	}
}

func (a *almacenAvisosFalso) ReclamarSiguiente(context.Context) (*avisoReclamado, error) {
	for i := range a.filas {
		fila := a.filas[i]
		if a.entregados[fila.id] || a.enEspera[fila.id] {
			continue
		}
		return &fila, nil
	}
	return nil, nil
}

func (a *almacenAvisosFalso) RegistrarFallo(_ context.Context, id int64, ultimoError string) error {
	a.fallos[id] = ultimoError
	a.enEspera[id] = true
	return nil
}

func (a *almacenAvisosFalso) MarcarEntregado(_ context.Context, id int64, _ string) error {
	a.entregados[id] = true
	return nil
}

func (a *almacenAvisosFalso) liberar(id int64) { a.enEspera[id] = false }

// notificadorFalso falla las primeras veces que se llama y luego entrega.
type notificadorFalso struct {
	fallar   int
	llamadas int
	err      error
}

func (n *notificadorFalso) Enviar(context.Context, Aviso) (string, error) {
	n.llamadas++
	if n.llamadas <= n.fallar {
		return "", n.err
	}
	return fmt.Sprintf("msg-%d", n.llamadas), nil
}

func TestDespachadorReintentaYEntregaConCanalesFalsos(t *testing.T) {
	t.Parallel()

	const destino = "aviso@centinela.test"
	payload := []byte(`{"destino":"correo","correo":"` + destino + `","plantilla":"marcador"}`)
	almacen := nuevoAlmacenAvisosFalso(avisoReclamado{id: 7, tipo: TipoAvisoContrasenaCambiada, payload: payload})
	canal := &notificadorFalso{fallar: 1, err: fmt.Errorf("no se pudo entregar a %s", destino)}
	despachador := nuevoDespachadorDeAvisos(almacen, canal)

	if err := despachador.DespacharAvisos(t.Context()); err == nil {
		t.Fatal("el primer barrido debía fallar")
	}
	if canal.llamadas != 1 {
		t.Errorf("llamadas = %d, quiero 1", canal.llamadas)
	}
	if fallo := almacen.fallos[7]; fallo == "" || strings.Contains(fallo, destino) {
		t.Errorf("ultimo_error = %q, quiero no vacío y sin el destino", fallo)
	}

	almacen.liberar(7)
	if err := despachador.DespacharAvisos(t.Context()); err != nil {
		t.Fatalf("segundo barrido: %v", err)
	}
	if !almacen.entregados[7] {
		t.Error("el aviso no quedó entregado")
	}
	if canal.llamadas != 2 {
		t.Errorf("llamadas = %d, quiero 2", canal.llamadas)
	}
}
