//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// eventoDePrueba arma un evento mínimo con el resultado marcador para localizar la fila.
func eventoDePrueba(tipo, resultado string) Evento {
	return Evento{
		Tipo:      tipo,
		Resultado: resultado,
		Fecha:     time.Now().UTC(),
		Origen:    "203.0.113.7",
		Contexto:  "prueba",
		Detalle:   map[string]string{"proposito": "inicio_sesion"},
	}
}

// contarEventos cuenta las filas con el resultado marcador, como rol_auth y sin retener la conexión.
func contarEventos(t *testing.T, pool *pgxpool.Pool, resultado string) int {
	t.Helper()

	tx, _ := abrirTransaccionDePrueba(t, pool)
	var total int
	err := tx.QueryRow(t.Context(), "SELECT count(*) FROM auth.eventos_autenticacion WHERE resultado = $1", resultado).Scan(&total)
	_ = tx.Rollback(t.Context())
	if err != nil {
		t.Fatalf("contar eventos: %v", err)
	}
	return total
}

// leerIDEvento devuelve el id de la fila con el resultado marcador.
func leerIDEvento(t *testing.T, pool *pgxpool.Pool, resultado string) int64 {
	t.Helper()

	tx, _ := abrirTransaccionDePrueba(t, pool)
	var id int64
	err := tx.QueryRow(t.Context(), "SELECT id FROM auth.eventos_autenticacion WHERE resultado = $1", resultado).Scan(&id)
	_ = tx.Rollback(t.Context())
	if err != nil {
		t.Fatalf("leer id del evento: %v", err)
	}
	return id
}

// assertSQLSTATE exige que el error sea un *pgconn.PgError con el SQLSTATE pedido.
func assertSQLSTATE(t *testing.T, err error, code string) {
	t.Helper()

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("el error no es *pgconn.PgError: %v", err)
	}
	if pgErr.Code != code {
		t.Errorf("SQLSTATE = %q, quiero %q (%s)", pgErr.Code, code, pgErr.Message)
	}
}

func TestEventoEnTransaccionRevierteNoQuedaYConfirmadoSi(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()

	revertido := "revertido-" + uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		t.Fatalf("SET LOCAL ROLE %s: %v", RolAuth, err)
	}
	if err := NuevoRegistradorEventosEnTx(repositorio.New(tx)).Registrar(ctx, eventoDePrueba(TipoEventoInicioSesion, revertido)); err != nil {
		t.Fatalf("registrar dentro de la transacción: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := contarEventos(t, pool, revertido); got != 0 {
		t.Errorf("evento de una transacción revertida visible = %d, quiero 0", got)
	}

	confirmado := "confirmado-" + uuid.NewString()
	if _, err := enTransaccion(ctx, poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, NuevoRegistradorEventosEnTx(q).Registrar(ctx, eventoDePrueba(TipoEventoInicioSesion, confirmado))
		}); err != nil {
		t.Fatalf("registrar y confirmar: %v", err)
	}
	if got := contarEventos(t, pool, confirmado); got != 1 {
		t.Errorf("evento de una transacción confirmada visible = %d, quiero 1", got)
	}
}

func TestRegistradorFueraDeTransaccionPersiste(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	marca := "fuera-" + uuid.NewString()

	if err := NuevoRegistradorEventos(pool).Registrar(t.Context(), eventoDePrueba(TipoEventoInicioSesion, marca)); err != nil {
		t.Fatalf("registrar fuera de transacción: %v", err)
	}
	if got := contarEventos(t, pool, marca); got != 1 {
		t.Errorf("evento fuera de transacción visible = %d, quiero 1", got)
	}
}

func TestEventosAutenticacionNoAdmitenUpdateNiDelete(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()

	marca := "inmutable-" + uuid.NewString()
	if err := NuevoRegistradorEventos(pool).Registrar(ctx, eventoDePrueba(TipoEventoRegistro, marca)); err != nil {
		t.Fatalf("registrar: %v", err)
	}
	id := leerIDEvento(t, pool, marca)

	txUpdate, _ := abrirTransaccionDePrueba(t, pool)
	_, errUpdate := txUpdate.Exec(ctx, "UPDATE auth.eventos_autenticacion SET resultado = 'alterado' WHERE id = $1", id)
	t.Logf("error literal de UPDATE como rol_auth: %v", errUpdate)
	_ = txUpdate.Rollback(ctx)

	txDelete, _ := abrirTransaccionDePrueba(t, pool)
	_, errDelete := txDelete.Exec(ctx, "DELETE FROM auth.eventos_autenticacion WHERE id = $1", id)
	t.Logf("error literal de DELETE como rol_auth: %v", errDelete)
	_ = txDelete.Rollback(ctx)

	if errUpdate == nil || !strings.Contains(errUpdate.Error(), "permission denied") {
		t.Errorf("UPDATE no falló por permiso: %v", errUpdate)
	}
	if errDelete == nil || !strings.Contains(errDelete.Error(), "permission denied") {
		t.Errorf("DELETE no falló por permiso: %v", errDelete)
	}
	assertSQLSTATE(t, errUpdate, pgerrcode.InsufficientPrivilege)
	assertSQLSTATE(t, errDelete, pgerrcode.InsufficientPrivilege)

	// El trigger se prueba en un UPDATE real sobre una tabla temporal: rol_auth no puede escribir la real.
	txTrigger, _ := abrirTransaccionDePrueba(t, pool)
	var definicion string
	errDefinicion := txTrigger.QueryRow(ctx,
		"SELECT pg_get_triggerdef(oid) FROM pg_trigger WHERE tgname = 'eventos_autenticacion_inmutable'").Scan(&definicion)
	errTemp := crearTablaTemporalConTrigger(ctx, txTrigger)
	_, errTrigger := txTrigger.Exec(ctx, "UPDATE eventos_prueba SET resultado = 'alterado' WHERE id = 1")
	t.Logf("definición del trigger: %s (err=%v)", definicion, errDefinicion)
	t.Logf("error literal del trigger en un UPDATE real: %v", errTrigger)
	_ = txTrigger.Rollback(ctx)

	if errDefinicion != nil {
		t.Fatalf("leer el trigger en el catálogo: %v", errDefinicion)
	}
	if errTemp != nil {
		t.Fatalf("montar la tabla temporal con el trigger: %v", errTemp)
	}
	if !strings.Contains(definicion, "BEFORE DELETE OR UPDATE") {
		t.Errorf("definición del trigger = %q, quiero que abarque BEFORE DELETE OR UPDATE", definicion)
	}
	assertSQLSTATE(t, errTrigger, pgerrcode.RestrictViolation)
}

// crearTablaTemporalConTrigger monta una tabla temporal con el trigger real para dispararlo en un UPDATE.
func crearTablaTemporalConTrigger(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, "CREATE TEMP TABLE eventos_prueba (id int, resultado text)"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "CREATE TRIGGER eventos_prueba_inmutable BEFORE UPDATE OR DELETE ON eventos_prueba FOR EACH ROW EXECUTE FUNCTION auth.eventos_autenticacion_inmutable()"); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "INSERT INTO eventos_prueba (id, resultado) VALUES (1, 'original')")
	return err
}

func TestDetalleDescartaClavesProhibidasYNuncaLasGuarda(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	marca := "detalle-" + uuid.NewString()

	evento := eventoDePrueba(TipoEventoInicioSesion, marca)
	evento.Actor = uuid.New()
	evento.Detalle = map[string]string{
		"proposito":      "inicio_sesion",
		"contrasena":     "SECRETO-contrasena",
		"codigo":         "SECRETO-codigo",
		"valor_derivado": "SECRETO-valor_derivado",
		"refresh_token":  "SECRETO-refresh_token",
		"password":       "SECRETO-password",
	}
	if err := NuevoRegistradorEventos(pool).Registrar(t.Context(), evento); err != nil {
		t.Fatalf("registrar: %v", err)
	}

	tx, _ := abrirTransaccionDePrueba(t, pool)
	var fila, detalleJSON string
	errFila := tx.QueryRow(t.Context(),
		`SELECT id::text || ' ' || tipo || ' ' || coalesce(actor_parte_id::text, '') || ' ' || coalesce(credencial_id::text, '') || ' ' || resultado || ' ' || origen || ' ' || contexto || ' ' || coalesce(sesion_id::text, '') || ' ' || detalle::text
		 FROM auth.eventos_autenticacion WHERE resultado = $1`, marca).Scan(&fila)
	errDetalle := tx.QueryRow(t.Context(), "SELECT detalle::text FROM auth.eventos_autenticacion WHERE resultado = $1", marca).Scan(&detalleJSON)
	_ = tx.Rollback(t.Context())
	if errFila != nil || errDetalle != nil {
		t.Fatalf("leer la fila: fila=%v detalle=%v", errFila, errDetalle)
	}

	var detalle map[string]string
	if err := json.Unmarshal([]byte(detalleJSON), &detalle); err != nil {
		t.Fatalf("detalle no es un objeto JSON: %v (%s)", err, detalleJSON)
	}
	if detalle["proposito"] != "inicio_sesion" {
		t.Errorf("detalle = %v, quiero que conserve proposito", detalle)
	}
	for _, prohibida := range []string{"contrasena", "codigo", "valor_derivado", "refresh_token", "password"} {
		if _, ok := detalle[prohibida]; ok {
			t.Errorf("el detalle conservó la clave prohibida %q: %v", prohibida, detalle)
		}
	}

	for _, prohibido := range []string{"contrasena", "valor_derivado", "refresh_token", "password", "SECRETO-"} {
		if strings.Contains(fila, prohibido) {
			t.Errorf("la fila contiene %q; fila = %q", prohibido, fila)
		}
	}
}

func TestTablaDeEventosNoTieneColumnasDeSecretos(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	tx, _ := abrirTransaccionDePrueba(t, pool)

	rows, err := tx.Query(t.Context(),
		"SELECT column_name FROM information_schema.columns WHERE table_schema = 'auth' AND table_name = 'eventos_autenticacion'")
	if err != nil {
		_ = tx.Rollback(t.Context())
		t.Fatalf("listar columnas: %v", err)
	}
	columnas := map[string]bool{}
	for rows.Next() {
		var columna string
		if err := rows.Scan(&columna); err != nil {
			rows.Close()
			_ = tx.Rollback(t.Context())
			t.Fatalf("leer columna: %v", err)
		}
		columnas[columna] = true
	}
	rows.Close()
	_ = tx.Rollback(t.Context())

	if len(columnas) == 0 {
		t.Fatal("information_schema.columns no devolvió columnas para auth.eventos_autenticacion")
	}
	for _, prohibida := range []string{"contrasena", "codigo", "codigo_otp", "valor_derivado", "refresh_token", "password"} {
		if columnas[prohibida] {
			t.Errorf("la tabla tiene una columna para el secreto %q", prohibida)
		}
	}
}

func TestOrigenSeGuardaCanonicalizadoIPv6(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	marca := "origen-" + uuid.NewString()

	evento := eventoDePrueba(TipoEventoInicioSesion, marca)
	evento.Origen = "2001:0db8:85a3:0000:0000:8a2e:0370:7334"
	if err := NuevoRegistradorEventos(pool).Registrar(t.Context(), evento); err != nil {
		t.Fatalf("registrar: %v", err)
	}

	tx, _ := abrirTransaccionDePrueba(t, pool)
	var origen string
	err := tx.QueryRow(t.Context(), "SELECT origen FROM auth.eventos_autenticacion WHERE resultado = $1", marca).Scan(&origen)
	_ = tx.Rollback(t.Context())
	if err != nil {
		t.Fatalf("leer origen: %v", err)
	}
	if origen != "2001:db8:85a3::" {
		t.Errorf("origen = %q, quiero %q", origen, "2001:db8:85a3::")
	}
}
