//go:build integration

package inteligencia

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPaginaAvisosEmpatesPrivacidadYDescarte(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	usuario, otro := uuid.NewString(), uuid.NewString()
	s := NuevoServicio(pool)
	ids := make([]uuid.UUID, 5)
	periodo := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	tx, e := pool.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(t.Context(), `SET LOCAL ROLE rol_inteligencia`); e != nil {
		t.Fatal(e)
	}
	for i := range ids {
		ids[i] = uuid.New()
		if _, e = tx.Exec(t.Context(), `INSERT INTO inteligencia.aviso(id,usuario_id,tipo,clave,periodo,severidad_centavos,prioridad,texto) VALUES($1,$2,$3,$4,'2026-07-01'::date,100,1,'aviso de prueba')`, ids[i], usuario, tipoDeRelleno(i), fmt.Sprintf("clave|%d", i/2)); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	vistos := map[uuid.UUID]bool{}
	for _, limite := range []int{1, 50} {
		p, err := s.AvisosDelUsuarioPaginados(t.Context(), usuario, periodo, EstadoVigentes, nil, limite)
		if err != nil || len(p.Avisos) != min(limite, len(ids)) {
			t.Fatalf("limite=%d: %d avisos, error=%v", limite, len(p.Avisos), err)
		}
	}
	var cursor *CursorDeAviso
	for {
		p, e := s.AvisosDelUsuarioPaginados(t.Context(), usuario, periodo, EstadoVigentes, cursor, 2)
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range p.Avisos {
			if vistos[a.ID] {
				t.Fatal("aviso repetido")
			}
			vistos[a.ID] = true
		}
		if !p.HayMas {
			if p.Siguiente != "" {
				t.Fatal("cursor final")
			}
			break
		}
		c, e := DecodificarCursorDeAviso(p.Siguiente)
		if e != nil {
			t.Fatal(e)
		}
		cursor = &c
	}
	if len(vistos) != 5 {
		t.Fatal("avisos omitidos")
	}
	for _, id := range []uuid.UUID{ids[0], uuid.New()} {
		if _, e := s.Aviso(t.Context(), otro, id); !errors.Is(e, ErrAvisoNoEncontrado) {
			t.Fatal(e)
		}
		if _, e := s.DescartarAviso(t.Context(), otro, id, time.Now()); !errors.Is(e, ErrAvisoNoEncontrado) {
			t.Fatal(e)
		}
	}
	if _, e := s.Aviso(t.Context(), usuario, ids[0]); e != nil {
		t.Fatal(e)
	}
	primero, e := s.DescartarAviso(t.Context(), usuario, ids[0], time.Now())
	if e != nil {
		t.Fatal(e)
	}
	segundo, e := s.DescartarAviso(t.Context(), usuario, ids[0], time.Now().Add(time.Hour))
	if e != nil || !primero.DescartadoEn.Equal(segundo.DescartadoEn) {
		t.Fatal("descarte no idempotente", e)
	}
	p, e := s.AvisosDelUsuarioPaginados(t.Context(), usuario, periodo, EstadoDescartados, nil, 20)
	if e != nil || len(p.Avisos) != 1 {
		t.Fatal(p, e)
	}
}

// tipoDeRelleno alterna dos tipos para que dos filas puedan compartir clave dentro del mismo
// periodo: la clave única es (usuario, tipo, clave, periodo), y el cursor ordena por clave e id.
func tipoDeRelleno(i int) string {
	if i%2 == 0 {
		return "cobro_nuevo"
	}
	return "caida_ingreso"
}
