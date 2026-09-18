package auth

import (
	"errors"
	"fmt"
	"net/http"

	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrEmailEnUso es el conflicto de dominio para un correo que ya está registrado.
var ErrEmailEnUso = errors.New("auth: el correo ya está registrado")

// ErrAuthNoDisponible es el fail closed de auth: un error de Redis corta la operación con 503.
var ErrAuthNoDisponible = errors.New("auth: la dependencia de auth no está disponible")

// ConFailClosed convierte el error de una dependencia en ErrAuthNoDisponible para que el mapeo responda 503.
func ConFailClosed(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrAuthNoDisponible, err)
}

// MapearError traduce cada error exportado de auth al APIError de su código del catálogo.
func MapearError(err error) error {
	var apiErr *platformerrors.APIError
	if errors.As(err, &apiErr) {
		return err
	}
	err = MapearErrorDeConsulta(err)
	switch {
	case errors.Is(err, ErrSinIdentidad):
		return platformerrors.New("Unauthorized", "Missing or invalid token.", http.StatusUnauthorized)
	case errors.Is(err, ErrEmailEnUso):
		return platformerrors.New("DuplicateIdentifier", "The identifier is already registered.", http.StatusConflict)
	case errors.Is(err, ErrAuthNoDisponible):
		return platformerrors.New("AuthUnavailable", "Authentication is temporarily unavailable.", http.StatusServiceUnavailable)
	default:
		return err
	}
}

// MapearErrorDeConsulta traduce el error del driver a error de dominio, limpia el Detail y vuelve ErrEmailEnUso la violación única de email.
func MapearErrorDeConsulta(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == pgerrcode.UniqueViolation {
			return fmt.Errorf("%w: %s", ErrEmailEnUso, pgErr.ConstraintName)
		}
		sanitizado := *pgErr
		sanitizado.Detail = ""
		return &sanitizado
	}
	return err
}
