//go:build integration

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEscenarioDosCorridasYReinicio(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("requiere base migrada de demo")
	}
	if e := validarDSN(os.Getenv("DATABASE_URL")); e != nil {
		t.Fatal(e)
	}
	ruta := filepath.Join(t.TempDir(), "estado.json")
	usuario := uuid.NewString()
	t.Setenv("DEMO_USUARIO", usuario)
	t.Setenv("DEMO_FECHA", "2026-11-19")
	t.Setenv("ESTADO", ruta)
	t.Setenv("REINICIAR", "no")
	t.Setenv("AVISOS_CUPO", "5")
	leer := func() estado {
		t.Helper()
		if e := run(); e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(ruta)
		if e != nil {
			t.Fatal(e)
		}
		var st estado
		if e = json.Unmarshal(b, &st); e != nil {
			t.Fatal(e)
		}
		return st
	}
	pool, e := pgxpool.New(t.Context(), os.Getenv("DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	leerCursor := func() int64 {
		t.Helper()
		var cursor int64
		if e := pool.QueryRow(t.Context(), "SELECT ultimo_entry_id FROM inteligencia.cursor_enriquecimiento WHERE id=1").Scan(&cursor); e != nil {
			t.Fatalf("no pude leer el cursor global: %v", e)
		}
		return cursor
	}
	inicial := leerCursor()
	primero := leer()
	segundo := leer()
	if !reflect.DeepEqual(primero, segundo) {
		t.Fatal("segunda corrida cambia el estado")
	}
	if despues := leerCursor(); despues != inicial {
		t.Fatalf("cursor global cambió: %d, era %d", despues, inicial)
	}
	otro := uuid.NewString()
	t.Setenv("DEMO_USUARIO", otro)
	_ = leer()
	huella := func() string {
		t.Helper()
		var h string
		if e := pool.QueryRow(t.Context(), `SELECT md5(COALESCE(string_agg(m::text,'|' ORDER BY transaccion_id),'')) FROM inteligencia.movimiento_enriquecido m WHERE usuario_id=$1`, otro).Scan(&h); e != nil {
			t.Fatal(e)
		}
		return h
	}
	antes := huella()
	t.Setenv("DEMO_USUARIO", usuario)
	t.Setenv("REINICIAR", "si")
	tercero := leer()
	if !reflect.DeepEqual(primero.Categorias, tercero.Categorias) || !reflect.DeepEqual(primero.Avisos, tercero.Avisos) || primero.Series != tercero.Series {
		t.Fatal("reinicio cambia los conteos")
	}
	if !cumpleAceptacion(tercero) {
		t.Fatalf("aceptación incompleta: avisos=%v series=%d categorías=%v asientos=%d", tercero.Avisos, tercero.Series, tercero.Categorias, len(tercero.Asientos))
	}
	if huella() != antes {
		t.Fatal("el reinicio modificó datos de otro usuario")
	}
}
