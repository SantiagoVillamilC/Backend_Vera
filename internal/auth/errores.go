package auth

import (
	"errors"
	"fmt"
	"net/http"

	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrIdentificadorEnUso es el conflicto genérico de unicidad: no dice cuál de los datos chocó.
var ErrIdentificadorEnUso = errors.New("auth: el identificador ya está en uso")

// ErrAuthNoDisponible es el fail closed de auth: un error de Redis corta la operación con 503.
var ErrAuthNoDisponible = errors.New("auth: la dependencia de auth no está disponible")

// ErrRefreshInvalido es el refresh ausente, revocado, vencido o presentado de nuevo dentro de la ventana de gracia.
var ErrRefreshInvalido = errors.New("auth: el refresh no es válido")

// ErrRefreshReusado es el refresh ya usado fuera de la ventana de gracia, que revoca la familia entera.
var ErrRefreshReusado = errors.New("auth: el refresh ya fue usado")

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
	case errors.Is(err, ErrIdentificadorEnUso):
		return platformerrors.New("DuplicateIdentifier", "The identifier is already registered.", http.StatusConflict)
	case errors.Is(err, ErrConflictoDeEstado), errors.Is(err, ErrTransicionNoPermitida):
		return platformerrors.New("ChallengeInvalid", "The account is not in a state that allows this change.", http.StatusBadRequest)
	case errors.Is(err, ErrAuthNoDisponible):
		return platformerrors.New("AuthUnavailable", "Authentication is temporarily unavailable.", http.StatusServiceUnavailable)
	case errors.Is(err, ErrRefreshReusado):
		return platformerrors.New("RefreshReused", "The refresh token has already been used.", http.StatusUnauthorized)
	case errors.Is(err, ErrRefreshInvalido):
		return platformerrors.New("RefreshInvalid", "The refresh token is invalid or expired.", http.StatusUnauthorized)
	default:
		return err
	}
}

// MapearErrorDeConsulta traduce el driver a dominio, limpia el Detail y vuelve ErrIdentificadorEnUso toda violación única.
func MapearErrorDeConsulta(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == pgerrcode.UniqueViolation {
			return fmt.Errorf("%w: %s", ErrIdentificadorEnUso, pgErr.ConstraintName)
		}
		sanitizado := *pgErr
		sanitizado.Detail = ""
		return &sanitizado
	}
	return err
}
