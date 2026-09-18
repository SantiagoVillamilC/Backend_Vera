package auth

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeTransaccionAuth es la transacción de auth sin Postgres y registra el orden de las sentencias.
type fakeTransaccionAuth struct {
	eventos   *[]string
	errExec   error
	errCommit error
	fila      pgx.Row
}

func (f *fakeTransaccionAuth) Exec(_ context.Context, sql string, _ ...interface{}) (pgconn.CommandTag, error) {
	*f.eventos = append(*f.eventos, "exec:"+sql)
	return pgconn.CommandTag{}, f.errExec
}

func (f *fakeTransaccionAuth) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (f *fakeTransaccionAuth) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return f.fila
}

func (f *fakeTransaccionAuth) Commit(context.Context) error {
	*f.eventos = append(*f.eventos, "commit")
	return f.errCommit
}

func (f *fakeTransaccionAuth) Rollback(context.Context) error {
	*f.eventos = append(*f.eventos, "rollback")
	return nil
}

type fakeAbridorAuth struct {
	tx  transaccionDeAuth
	err error
}

func (f fakeAbridorAuth) Begin(context.Context) (transaccionDeAuth, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tx, nil
}

func TestEnTransaccionFijaElRolYElLockTimeoutAntesDeLaOperacion(t *testing.T) {
	t.Parallel()

	var eventos []string
	tx := &fakeTransaccionAuth{eventos: &eventos}

	got, err := enTransaccion(t.Context(), fakeAbridorAuth{tx: tx},
		func(_ context.Context, _ transaccionDeAuth, _ *repositorio.Queries) (string, error) {
			eventos = append(eventos, "operacion")
			return "resultado", nil
		})
	if err != nil {
		t.Fatalf("enTransaccion: error inesperado: %v", err)
	}
	if got != "resultado" {
		t.Errorf("enTransaccion = %q, want %q", got, "resultado")
	}

	want := []string{"exec:" + sentenciaCambioDeRol, "exec:" + sentenciaEsperaDeBloqueo, "operacion", "commit", "rollback"}
	if !slices.Equal(eventos, want) {
		t.Errorf("orden de eventos = %v, want %v", eventos, want)
	}
}

func TestEnTransaccionEnvuelveElErrorDeAbrir(t *testing.T) {
	t.Parallel()

	errAbrir := errors.New("sin conexión")
	if _, err := enTransaccion(t.Context(), fakeAbridorAuth{err: errAbrir},
		func(context.Context, transaccionDeAuth, *repositorio.Queries) (struct{}, error) {
			t.Fatal("la operación no debe correr si falla el Begin")
			return struct{}{}, nil
		}); !errors.Is(err, errAbrir) {
		t.Fatalf("enTransaccion error = %v, want que envuelva %v", err, errAbrir)
	}
}

func TestEnTransaccionNoConfirmaSiFallaLaOperacion(t *testing.T) {
	t.Parallel()

	var eventos []string
	tx := &fakeTransaccionAuth{eventos: &eventos}
	errOperacion := errors.New("la operación falló")

	if _, err := enTransaccion(t.Context(), fakeAbridorAuth{tx: tx},
		func(context.Context, transaccionDeAuth, *repositorio.Queries) (struct{}, error) {
			eventos = append(eventos, "operacion")
			return struct{}{}, errOperacion
		}); !errors.Is(err, errOperacion) {
		t.Fatalf("enTransaccion error = %v, want %v", err, errOperacion)
	}

	want := []string{"exec:" + sentenciaCambioDeRol, "exec:" + sentenciaEsperaDeBloqueo, "operacion", "rollback"}
	if !slices.Equal(eventos, want) {
		t.Errorf("orden de eventos = %v, want %v", eventos, want)
	}
}

func TestEnTransaccionAbortaSiElCambioDeRolFalla(t *testing.T) {
	t.Parallel()

	var eventos []string
	errRol := errors.New("permiso denegado para SET ROLE")
	tx := &fakeTransaccionAuth{eventos: &eventos, errExec: errRol}

	if _, err := enTransaccion(t.Context(), fakeAbridorAuth{tx: tx},
		func(context.Context, transaccionDeAuth, *repositorio.Queries) (struct{}, error) {
			eventos = append(eventos, "operacion")
			return struct{}{}, nil
		}); !errors.Is(err, errRol) {
		t.Fatalf("enTransaccion error = %v, want que envuelva %v", err, errRol)
	}

	want := []string{"exec:" + sentenciaCambioDeRol, "rollback"}
	if !slices.Equal(eventos, want) {
		t.Errorf("orden de eventos = %v, want %v", eventos, want)
	}
}
