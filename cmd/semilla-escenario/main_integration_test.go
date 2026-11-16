//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEscenarioDosCorridasYReinicio(t *testing.T) {
	// Variable propia: DATABASE_URL la comparten las demás pruebas de integración y apunta a la base
	// principal, que el guard de validarDSN rechaza a propósito. Esta prueba escribe y con
	// REINICIAR=si borra datos, así que exige su base desechable en 127.0.0.1:55437/vera_semilla_*.
	dsn := os.Getenv("SEMILLA_DATABASE_URL")
	if dsn == "" {
		t.Skip("requiere SEMILLA_DATABASE_URL con una base migrada de demo")
	}
	if e := validarDSN(dsn); e != nil {
		t.Fatal(e)
	}
	// run() lee DATABASE_URL, igual que el CLI, y la restaura al terminar la prueba.
	t.Setenv("DATABASE_URL", dsn)
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
	pool, e := pgxpool.New(t.Context(), dsn)
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
	if !reflect.DeepEqual(primero.Categorias, tercero.Categorias) || !reflect.DeepEqual(primero.Avisos, tercero.Avisos) || !reflect.DeepEqual(primero.Origenes, tercero.Origenes) || primero.Series != tercero.Series {
		t.Fatal("reinicio cambia los conteos")
	}
	if !cumpleAceptacion(tercero) {
		t.Fatalf("aceptación incompleta: avisos=%v series=%d categorías=%v orígenes=%v asientos=%d", tercero.Avisos, tercero.Series, tercero.Categorias, tercero.Origenes, len(tercero.Asientos))
	}
	if huella() != antes {
		t.Fatal("el reinicio modificó datos de otro usuario")
	}
}

// entornoDeSiembra prepara las variables que lee run() y devuelve la ruta del estado y el pool del
// dueño. Variable propia, igual que en la prueba de las dos corridas: la base es desechable.
func entornoDeSiembra(t *testing.T, usuario string) (string, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("SEMILLA_DATABASE_URL")
	if dsn == "" {
		t.Skip("requiere SEMILLA_DATABASE_URL con una base migrada de demo")
	}
	if e := validarDSN(dsn); e != nil {
		t.Fatal(e)
	}
	t.Setenv("DATABASE_URL", dsn)
	ruta := filepath.Join(t.TempDir(), "estado.json")
	t.Setenv("DEMO_USUARIO", usuario)
	t.Setenv("DEMO_FECHA", "2026-11-19")
	t.Setenv("ESTADO", ruta)
	t.Setenv("REINICIAR", "no")
	t.Setenv("AVISOS_CUPO", "5")
	pool, e := pgxpool.New(t.Context(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	return ruta, pool
}

// subcuentasDeUsuario lee las cuentas de clase usuario del titular, ordenadas por id.
func subcuentasDeUsuario(t *testing.T, pool *pgxpool.Pool, usuario string) []string {
	t.Helper()
	filas, e := pool.Query(t.Context(), `SELECT id || '|' || naturaleza || '|' || clase || '|' || titular_id || '|' || moneda
		FROM nucleo.cuenta WHERE titular_id = $1 AND clase = 'usuario' ORDER BY id`, usuario)
	if e != nil {
		t.Fatal(e)
	}
	defer filas.Close()
	var cuentas []string
	for filas.Next() {
		var c string
		if e = filas.Scan(&c); e != nil {
			t.Fatal(e)
		}
		cuentas = append(cuentas, c)
	}
	if e = filas.Err(); e != nil {
		t.Fatal(e)
	}
	sort.Strings(cuentas)
	return cuentas
}

// Regresión: el usuario del escenario quedó solo con la subcuenta disponible y la primera
// transferencia que retuvo dinero falló con una violación de llave foránea sobre :retenido.
func TestEscenarioSiembraLasDosSubcuentasDelUsuario(t *testing.T) {
	usuario := uuid.NewString()
	_, pool := entornoDeSiembra(t, usuario)
	esperadas := []string{
		nucleo.NombreCuentaDisponible(usuario) + "|pasivo|usuario|" + usuario + "|COP",
		nucleo.NombreCuentaRetenido(usuario) + "|pasivo|usuario|" + usuario + "|COP",
	}
	sort.Strings(esperadas)
	verificar := func(contexto string) {
		t.Helper()
		if got := subcuentasDeUsuario(t, pool, usuario); !reflect.DeepEqual(got, esperadas) {
			t.Fatalf("%s: subcuentas = %v, quiero %v", contexto, got, esperadas)
		}
	}

	if e := run(); e != nil {
		t.Fatal(e)
	}
	verificar("primera siembra")

	if e := run(); e != nil {
		t.Fatal(e)
	}
	verificar("siembra repetida (idempotente)")

	// Estado heredado de producción: manifiesto vigente y sin la cuenta retenido. Una siembra sin
	// reiniciar, como la segunda pasada de demo-inyectar.sh, debe repararlo.
	if _, e := pool.Exec(t.Context(), `DELETE FROM nucleo.cuenta WHERE id = $1`, nucleo.NombreCuentaRetenido(usuario)); e != nil {
		t.Fatal(e)
	}
	if e := run(); e != nil {
		t.Fatal(e)
	}
	verificar("reparación sin reiniciar")

	t.Setenv("REINICIAR", "si")
	if e := run(); e != nil {
		t.Fatal(e)
	}
	verificar("reinicio")
}

// Regresión: prepara la transferencia del usuario sembrado y la ejecuta por el mismo camino que la
// confirmación del asistente hasta el posting que usa la cuenta retenido.
func TestUsuarioSembradoPuedeConfirmarUnaTransferencia(t *testing.T) {
	appDSN := os.Getenv("APP_DATABASE_URL")
	if appDSN == "" {
		t.Skip("requiere APP_DATABASE_URL con el rol_api de la misma base de demo")
	}
	usuario := uuid.NewString()
	receptor := uuid.NewString()
	_, pool := entornoDeSiembra(t, usuario)
	if e := run(); e != nil {
		t.Fatal(e)
	}
	app, e := pgxpool.New(t.Context(), appDSN)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(app.Close)
	servicio := nucleo.NewServicio(app, nucleo.LectorNivelKYCFijo{}, nil, nil, nil,
		nucleo.ConResolutorDestino(destinoFijo{"destino": receptor}))
	if _, e = servicio.AbrirSubcuentasDeUsuario(t.Context(), receptor); e != nil {
		t.Fatal(e)
	}

	plan, e := servicio.PlanificarTransferencia(t.Context(), nucleo.SolicitudTransferencia{
		UsuarioID:      usuario,
		CuentaOrigen:   nucleo.NombreCuentaDisponible(usuario),
		LlaveDestino:   "destino",
		Monto:          nucleo.Centavos(100000),
		Moneda:         nucleo.MonedaCOP,
		IdempotencyKey: "k-" + uuid.NewString(),
	})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := app.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	transferencia, e := plan.EjecutarEn(t.Context(), tx)
	if e != nil {
		t.Fatalf("confirmar la transferencia del usuario sembrado: %v", e)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if transferencia.Estado != nucleo.EstadoTransferenciaConfirmada {
		t.Fatalf("estado = %q, quiero %q", transferencia.Estado, nucleo.EstadoTransferenciaConfirmada)
	}
	var lineasEnRetenido int
	if e = pool.QueryRow(t.Context(), `SELECT count(*) FROM nucleo.posting p JOIN nucleo.journal_entry j ON j.id = p.entry_id
		WHERE p.cuenta_id = $1 AND j.transferencia_id = $2`, nucleo.NombreCuentaRetenido(usuario), transferencia.ID).Scan(&lineasEnRetenido); e != nil {
		t.Fatal(e)
	}
	if lineasEnRetenido != 2 {
		t.Fatalf("apuntes en %s = %d, quiero 2 (entrada de la retención y salida de la transferencia)", nucleo.NombreCuentaRetenido(usuario), lineasEnRetenido)
	}
}

// destinoFijo resuelve las llaves contra un mapa en memoria.
type destinoFijo map[string]string

func (d destinoFijo) UsuarioIDDeDestino(_ context.Context, llave string) (string, bool, error) {
	id, ok := d[llave]
	return id, ok, nil
}
