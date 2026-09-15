package auth

import (
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapearErrorDeConsulta(t *testing.T) {
	otro := errors.New("otro error")
	casos := map[string]struct {
		err  error
		want error
	}{
		"violación de unicidad": {
			err:  &pgconn.PgError{Code: pgerrcode.UniqueViolation, ConstraintName: "users_email_key"},
			want: ErrEmailEnUso,
		},
		"error no relacionado": {
			err:  otro,
			want: otro,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			got := MapearErrorDeConsulta(caso.err)
			if !errors.Is(got, caso.want) {
				t.Fatalf("se esperaba %v, se obtuvo %v", caso.want, got)
			}
		})
	}
}
