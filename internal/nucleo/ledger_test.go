package nucleo

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeTransaccionConArgs guarda los argumentos de la última sentencia parametrizada, sin Postgres.
type fakeTransaccionConArgs struct {
	args []any
	err  error
}

func (f *fakeTransaccionConArgs) Exec(_ context.Context, _ string, args ...interface{}) (pgconn.CommandTag, error) {
	if len(args) > 0 {
		f.args = append([]any(nil), args...)
	}
	return pgconn.NewCommandTag("INSERT 0 2"), f.err
}

func (f *fakeTransaccionConArgs) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

func (f *fakeTransaccionConArgs) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return nil
}

func (f *fakeTransaccionConArgs) Commit(context.Context) error   { return nil }
func (f *fakeTransaccionConArgs) Rollback(context.Context) error { return nil }

func TestNombresDeCuentasDelPlan(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		got  string
		want string
	}{
		"custodia del banco":     {CuentaCustodiaBanco, "activo:custodia:banco"},
		"liquidacion pendiente":  {CuentaLiquidacionPendienteRiel, "pasivo:liquidacion_pendiente:riel"},
		"gmf por pagar":          {CuentaGMFPorPagar, "pasivo:gmf_por_pagar"},
		"subcuenta disponible":   {NombreCuentaDisponible("abc"), "pasivo:usuario:abc:disponible"},
		"subcuenta de retencion": {NombreCuentaRetenido("abc"), "pasivo:usuario:abc:retenido"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.got != tc.want {
				t.Errorf("nombre = %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestAbrirSubcuentasDeUsuarioRechazaUsuarioVacio(t *testing.T) {
	t.Parallel()

	s := NewServicio(nil, nil, nil, nil, nil)
	if _, err := s.AbrirSubcuentasDeUsuario(t.Context(), ""); !errors.Is(err, ErrUsuarioVacio) {
		t.Fatalf("AbrirSubcuentasDeUsuario(\"\") error = %v, want ErrUsuarioVacio", err)
	}
}

func TestAbrirSubcuentasDeUsuarioRechazaUsuarioSinFormaDeUUID(t *testing.T) {
	t.Parallel()

	s := NewServicio(nil, nil, nil, nil, nil)
	if _, err := s.AbrirSubcuentasDeUsuario(t.Context(), "usuario-1"); !errors.Is(err, ErrUsuarioIDInvalido) {
		t.Fatalf("AbrirSubcuentasDeUsuario(\"usuario-1\") error = %v, want ErrUsuarioIDInvalido", err)
	}
}

func TestAbrirSubcuentasDeUsuarioInsertaLasDosCuentasConTitular(t *testing.T) {
	t.Parallel()

	const usuarioID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	tx := &fakeTransaccionConArgs{}
	s := &Servicio{pool: fakeAbridor{tx: tx}}

	creadas, err := s.AbrirSubcuentasDeUsuario(t.Context(), usuarioID)
	if err != nil || creadas != 2 {
		t.Fatalf("AbrirSubcuentasDeUsuario = %d, %v; want 2, nil", creadas, err)
	}

	if len(tx.args) != 3 {
		t.Fatalf("argumentos = %v, want 3", tx.args)
	}
	if tx.args[0] != "pasivo:usuario:"+usuarioID+":disponible" || tx.args[2] != "pasivo:usuario:"+usuarioID+":retenido" {
		t.Errorf("nombres = %v, want las dos subcuentas del usuario", tx.args)
	}
	titular, ok := tx.args[1].(pgtype.Text)
	if !ok || !titular.Valid || titular.String != usuarioID {
		t.Errorf("titular = %#v, want pgtype.Text{String: %q, Valid: true}", tx.args[1], usuarioID)
	}
}
