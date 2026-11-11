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
	for i := range ids {
		ids[i] = uuid.New()
		_, e := pool.Exec(t.Context(), `INSERT INTO inteligencia.aviso(id,usuario_id,tipo,clave,periodo,severidad_centavos,prioridad,texto) VALUES($1,$2,'cobro_nuevo',$3,$4::date,100,1,'aviso de prueba')`, ids[i], usuario, fmt.Sprintf("clave|%d", i/2), fmt.Sprintf("2026-07-%02d", i+1))
		if e != nil {
			t.Fatal(e)
		}
	}
	vistos := map[uuid.UUID]bool{}
	for _, limite := range []int{1, 50} {
		p, err := s.AvisosDelUsuarioPaginados(t.Context(), usuario, EstadoVigentes, nil, limite)
		if err != nil || len(p.Avisos) != min(limite, len(ids)) {
			t.Fatalf("limite=%d: %d avisos, error=%v", limite, len(p.Avisos), err)
		}
	}
	var cursor *CursorDeAviso
	for {
		p, e := s.AvisosDelUsuarioPaginados(t.Context(), usuario, EstadoVigentes, cursor, 2)
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
	p, e := s.AvisosDelUsuarioPaginados(t.Context(), usuario, EstadoDescartados, nil, 20)
	if e != nil || len(p.Avisos) != 1 {
		t.Fatal(p, e)
	}
}
