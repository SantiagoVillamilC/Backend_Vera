package auth

import (
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrEmailEnUso es el conflicto de dominio para un correo que ya está registrado.
var ErrEmailEnUso = errors.New("auth: el correo ya está registrado")

// MapearErrorDeConsulta traduce el error del driver de Postgres a un error de dominio. Una
// violación del índice único de email se vuelve ErrEmailEnUso, cualquier otro error pasa tal cual.
func MapearErrorDeConsulta(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
		return fmt.Errorf("%w: %s", ErrEmailEnUso, pgErr.ConstraintName)
	}
	return err
}
