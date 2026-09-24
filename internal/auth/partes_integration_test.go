//go:build integration

package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// abrirTransaccionDePrueba abre una transacción como rol_auth que siempre revierte y no ensucia la base.
func abrirTransaccionDePrueba(tb testing.TB, pool *pgxpool.Pool) (pgx.Tx, *repositorio.Queries) {
	tb.Helper()

	tx, err := pool.Begin(context.Background())
	if err != nil {
		tb.Fatalf("begin: %v", err)
	}
	tb.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(context.Background(), sentenciaCambioDeRol); err != nil {
		tb.Fatalf("SET LOCAL ROLE %s: %v", RolAuth, err)
	}
	return tx, repositorio.New(tx)
}

// pgUUID envuelve un uuid de Go en el tipo de pgx que usa el generado de sqlc.
func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

// sembrarParte inserta una parte del tipo pedido y devuelve su id.
func sembrarParte(tb testing.TB, ctx context.Context, q *repositorio.Queries, tipo string) uuid.UUID {
	tb.Helper()

	id := uuid.New()
	if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(id), Tipo: tipo}); err != nil {
		tb.Fatalf("crear parte: %v", err)
	}
	return id
}

// sembrarCredencial inserta una credencial con valor derivado y devuelve su id.
func sembrarCredencial(tb testing.TB, ctx context.Context, q *repositorio.Queries, parte uuid.UUID, correo, celular, estado string) uuid.UUID {
	tb.Helper()

	id := uuid.New()
	if _, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
		ID:            pgUUID(id),
		ParteID:       pgUUID(parte),
		Correo:        correo,
		Celular:       celular,
		ValorDerivado: pgtype.Text{String: "$argon2id$hash", Valid: true},
		Estado:        estado,
	}); err != nil {
		tb.Fatalf("crear credencial %s: %v", correo, err)
	}
	return id
}

// exigirViolacionUnica exige que err sea la violación única del índice pedido y que el mapeo la vuelva ErrIdentificadorEnUso.
func exigirViolacionUnica(tb testing.TB, err error, indice string) {
	tb.Helper()

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		tb.Fatalf("el error no es *pgconn.PgError: %v", err)
	}
	if pgErr.Code != pgerrcode.UniqueViolation {
		tb.Fatalf("SQLSTATE = %q, quiero %q: %v", pgErr.Code, pgerrcode.UniqueViolation, err)
	}
	if pgErr.ConstraintName != indice {
		tb.Fatalf("índice = %q, quiero %q", pgErr.ConstraintName, indice)
	}
	if !errors.Is(MapearErrorDeConsulta(err), ErrIdentificadorEnUso) {
		tb.Fatalf("el mapeo = %v, quiero ErrIdentificadorEnUso", MapearErrorDeConsulta(err))
	}
	tb.Logf("error literal de Postgres: %v", err)
}

func TestCorreoRepetidoChocaPorElIndiceParcial(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	_, q := abrirTransaccionDePrueba(t, pool)

	parteA := sembrarParte(t, ctx, q, "persona")
	parteB := sembrarParte(t, ctx, q, "persona")
	sembrarCredencial(t, ctx, q, parteA, "repetido@centinela.test", "+573000000001", "activo")

	_, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
		ID:            pgUUID(uuid.New()),
		ParteID:       pgUUID(parteB),
		Correo:        "repetido@centinela.test",
		Celular:       "+573000000002",
		ValorDerivado: pgtype.Text{String: "$argon2id$hash", Valid: true},
		Estado:        "activo",
	})
	exigirViolacionUnica(t, err, "credenciales_correo_uk")
}

func TestCelularRepetidoChocaPorElIndiceParcial(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	_, q := abrirTransaccionDePrueba(t, pool)

	parteA := sembrarParte(t, ctx, q, "persona")
	parteB := sembrarParte(t, ctx, q, "persona")
	sembrarCredencial(t, ctx, q, parteA, "a@centinela.test", "+573000000010", "activo")

	_, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
		ID:            pgUUID(uuid.New()),
		ParteID:       pgUUID(parteB),
		Correo:        "b@centinela.test",
		Celular:       "+573000000010",
		ValorDerivado: pgtype.Text{String: "$argon2id$hash", Valid: true},
		Estado:        "activo",
	})
	exigirViolacionUnica(t, err, "credenciales_celular_uk")
}

func TestCredencialCerradaLibraElCorreo(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	tx, q := abrirTransaccionDePrueba(t, pool)

	parteA := sembrarParte(t, ctx, q, "persona")
	parteB := sembrarParte(t, ctx, q, "persona")
	sembrarCredencial(t, ctx, q, parteA, "liberado@centinela.test", "+573000000020", "activo")

	if _, err := tx.Exec(ctx, "UPDATE auth.credenciales SET estado = 'cerrado' WHERE parte_id = $1", pgUUID(parteA)); err != nil {
		t.Fatalf("cerrar la credencial: %v", err)
	}

	idB := sembrarCredencial(t, ctx, q, parteB, "liberado@centinela.test", "+573000000021", "activo")
	fila, err := q.CredencialPorID(ctx, pgUUID(idB))
	if err != nil {
		t.Fatalf("leer la credencial nueva: %v", err)
	}
	if fila.Correo != "liberado@centinela.test" || fila.Estado != "activo" {
		t.Fatalf("credencial nueva = %+v, quiero el correo liberado en estado activo", fila)
	}
}

func TestSegundaCredencialParaLaMismaParteChoca(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	_, q := abrirTransaccionDePrueba(t, pool)

	parte := sembrarParte(t, ctx, q, "persona")
	id := sembrarCredencial(t, ctx, q, parte, "unica@centinela.test", "+573000000030", "activo")

	fila, err := q.CredencialPorID(ctx, pgUUID(id))
	if err != nil {
		t.Fatalf("leer la credencial: %v", err)
	}
	if fila.ParteID != pgUUID(parte) {
		t.Fatalf("parte_id = %v, quiero %v", fila.ParteID, pgUUID(parte))
	}

	_, err = q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
		ID:            pgUUID(uuid.New()),
		ParteID:       pgUUID(parte),
		Correo:        "otra@centinela.test",
		Celular:       "+573000000031",
		ValorDerivado: pgtype.Text{String: "$argon2id$hash", Valid: true},
		Estado:        "activo",
	})
	exigirViolacionUnica(t, err, "credenciales_parte_id_uk")
}

func TestCredencialActivaSinValorDerivadoViolaElCheck(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	_, q := abrirTransaccionDePrueba(t, pool)

	parte := sembrarParte(t, ctx, q, "persona")
	_, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
		ID:            pgUUID(uuid.New()),
		ParteID:       pgUUID(parte),
		Correo:        "sindato@centinela.test",
		Celular:       "+573000000040",
		ValorDerivado: pgtype.Text{},
		Estado:        "activo",
	})
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("el error no es *pgconn.PgError: %v", err)
	}
	if pgErr.Code != pgerrcode.CheckViolation {
		t.Fatalf("SQLSTATE = %q, quiero %q: %v", pgErr.Code, pgerrcode.CheckViolation, err)
	}
	if pgErr.ConstraintName != "credenciales_valor_derivado_requerido" {
		t.Fatalf("check = %q, quiero %q", pgErr.ConstraintName, "credenciales_valor_derivado_requerido")
	}
	t.Logf("error literal de Postgres: %v", err)
}

func TestPersonaConDocumentoRepetidoChoca(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	_, q := abrirTransaccionDePrueba(t, pool)

	parteA := sembrarParte(t, ctx, q, "persona")
	parteB := sembrarParte(t, ctx, q, "persona")
	if _, err := q.CrearPersona(ctx, repositorio.CrearPersonaParams{
		ParteID:       pgUUID(parteA),
		TipoDocumento: "CC",
		Documento:     "1020304050",
		Nombre:        "Ana Centinela",
	}); err != nil {
		t.Fatalf("crear la persona: %v", err)
	}

	_, err := q.CrearPersona(ctx, repositorio.CrearPersonaParams{
		ParteID:       pgUUID(parteB),
		TipoDocumento: "CC",
		Documento:     "1020304050",
		Nombre:        "Otra Persona",
	})
	exigirViolacionUnica(t, err, "personas_documento_uk")
}

func TestEmpresaConNitRepetidoChoca(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	_, q := abrirTransaccionDePrueba(t, pool)

	parteA := sembrarParte(t, ctx, q, "empresa")
	parteB := sembrarParte(t, ctx, q, "empresa")
	if _, err := q.CrearEmpresa(ctx, repositorio.CrearEmpresaParams{
		ParteID:     pgUUID(parteA),
		Nit:         "900123456",
		RazonSocial: "Centinela SAS",
	}); err != nil {
		t.Fatalf("crear la empresa: %v", err)
	}

	_, err := q.CrearEmpresa(ctx, repositorio.CrearEmpresaParams{
		ParteID:     pgUUID(parteB),
		Nit:         "900123456",
		RazonSocial: "Otra Empresa SAS",
	})
	exigirViolacionUnica(t, err, "empresas_nit_uk")
}

// TestTransicionCredencialRechazaUpdate comprueba que rol_auth no puede reescribir el historial append-only.
func TestTransicionCredencialRechazaUpdate(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		t.Fatalf("SET LOCAL ROLE %s: %v", RolAuth, err)
	}

	_, errUpdate := tx.Exec(ctx, "UPDATE auth.transiciones_credencial SET motivo = 'reescrito'")
	exigirPermisoDenegado(t, errUpdate, "permission denied for table transiciones_credencial")
}

// TestTransicionCredencialRechazaDelete comprueba que rol_auth no puede borrar el historial append-only.
func TestTransicionCredencialRechazaDelete(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		t.Fatalf("SET LOCAL ROLE %s: %v", RolAuth, err)
	}

	_, errDelete := tx.Exec(ctx, "DELETE FROM auth.transiciones_credencial")
	exigirPermisoDenegado(t, errDelete, "permission denied for table transiciones_credencial")
}

// exigirPermisoDenegado exige SQLSTATE 42501 y el mensaje literal de Postgres.
func exigirPermisoDenegado(tb testing.TB, err error, mensaje string) {
	tb.Helper()

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		tb.Fatalf("el error no es *pgconn.PgError: %v", err)
	}
	if pgErr.Code != pgerrcode.InsufficientPrivilege {
		tb.Fatalf("SQLSTATE = %q, quiero %q: %v", pgErr.Code, pgerrcode.InsufficientPrivilege, err)
	}
	if !strings.Contains(pgErr.Message, mensaje) {
		tb.Fatalf("mensaje = %q, quiero que contenga %q", pgErr.Message, mensaje)
	}
	tb.Logf("error literal de Postgres: %v", err)
}

// TestRolApiNoLeeAuthSinSetLocalRole comprueba el aislamiento: el rol de login del pool no ve el esquema auth.
func TestRolApiNoLeeAuthSinSetLocalRole(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := context.Background()

	var total int64
	err := pool.QueryRow(ctx, "SELECT count(*) FROM auth.credenciales").Scan(&total)
	exigirPermisoDenegado(t, err, "permission denied for schema auth")
}

func TestRepresentanteCubreDosEmpresasConUnaCredencial(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	tx, q := abrirTransaccionDePrueba(t, pool)

	representante := sembrarParte(t, ctx, q, "persona")
	sembrarCredencial(t, ctx, q, representante, "representante@centinela.test", "+573000000050", "activo")
	empresaA := sembrarParte(t, ctx, q, "empresa")
	empresaB := sembrarParte(t, ctx, q, "empresa")

	ids := make([]int64, 0, 2)
	for _, empresa := range []uuid.UUID{empresaA, empresaB} {
		fila, err := q.CrearRolDeParte(ctx, repositorio.CrearRolDeParteParams{
			ParteID:        pgUUID(representante),
			Rol:            "representante_legal",
			RepresentadaID: pgUUID(empresa),
		})
		if err != nil {
			t.Fatalf("crear rol de representante: %v", err)
		}
		if fila.ID == 0 {
			t.Fatalf("id del rol = %d, quiero distinto de cero", fila.ID)
		}
		ids = append(ids, fila.ID)
	}

	if _, err := tx.Exec(ctx, "SAVEPOINT rol_repetido"); err != nil {
		t.Fatalf("abrir savepoint: %v", err)
	}
	_, err := q.CrearRolDeParte(ctx, repositorio.CrearRolDeParteParams{
		ParteID:        pgUUID(representante),
		Rol:            "representante_legal",
		RepresentadaID: pgUUID(empresaA),
	})
	exigirViolacionUnica(t, err, "roles_de_parte_vigente_uk")
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT rol_repetido"); err != nil {
		t.Fatalf("volver al savepoint: %v", err)
	}

	if _, err := tx.Exec(ctx, "UPDATE auth.roles_de_parte SET revocado_en = now() WHERE id = $1", ids[0]); err != nil {
		t.Fatalf("revocar el rol: %v", err)
	}
	reotorgado, err := q.CrearRolDeParte(ctx, repositorio.CrearRolDeParteParams{
		ParteID:        pgUUID(representante),
		Rol:            "representante_legal",
		RepresentadaID: pgUUID(empresaA),
	})
	if err != nil {
		t.Fatalf("reotorgar el rol tras revocarlo: %v", err)
	}
	if reotorgado.ID == 0 || reotorgado.ID == ids[0] {
		t.Fatalf("id reotorgado = %d, quiero uno nuevo distinto de %d", reotorgado.ID, ids[0])
	}
}
