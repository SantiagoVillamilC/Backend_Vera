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

// ErrCelularSinVerificar es el destino de un desafío que exige celular verificado: responde 403 PhoneUnverified.
var ErrCelularSinVerificar = errors.New("auth: la credencial no tiene el celular verificado")

// ErrConsentimientoRequerido es la falta de opt-in vigente para enviar el OTP: responde 403 ConsentRequired.
var ErrConsentimientoRequerido = errors.New("auth: falta el consentimiento para enviar el OTP")

// ErrNombreInvalido es el nombre completo que no cumple la regla minima: responde 422 con nombre_completo/nombre.
var ErrNombreInvalido = errors.New("auth: el nombre completo no cumple la regla")

// ErrCelularInvalido es el celular que E.164 acepta pero la numeracion rechaza: responde 422 con celular/e164.
var ErrCelularInvalido = errors.New("auth: el celular no es un numero valido")

// ErrEntregaFallida es el canal que no pudo entregar el código del desafío: responde 503 OtpDeliveryFailed.
var ErrEntregaFallida = errors.New("auth: no se pudo entregar el código del desafío")

// ErrDesafioInvalido es un desafío inexistente, ya usado o en otro estado: responde 400 ChallengeInvalid.
var ErrDesafioInvalido = errors.New("auth: el desafío no es válido")

// ErrCodigoInvalido es el código presentado que no coincide con el del desafío: responde 400 OtpInvalid.
var ErrCodigoInvalido = errors.New("auth: el código del desafío no coincide")

// ErrCodigoExpirado es el desafío cuya vigencia terminó: responde 400 OtpExpired.
var ErrCodigoExpirado = errors.New("auth: el código del desafío expiró")

// ErrDemasiadosIntentos es el desafío que agotó sus intentos: responde 429 OtpTooManyAttempts.
var ErrDemasiadosIntentos = errors.New("auth: el desafío agotó los intentos")

// ErrEnfriamientoReenvio es el reenvío pedido antes de la ventana de espera: responde 429 OtpResendCooldown.
var ErrEnfriamientoReenvio = errors.New("auth: todavía no pasó el enfriamiento del reenvío")

// ErrDemasiadosReenvios es el desafío que agotó sus reenvíos: responde 429 OtpTooManyResends.
var ErrDemasiadosReenvios = errors.New("auth: el desafío agotó los reenvíos")

// ErrDemasiadosEnvios es el tope de cuenta, origen o fallos superado: responde 429 TooManyRequests genérico.
var ErrDemasiadosEnvios = errors.New("auth: se superó el tope de envíos")

// ErrCredencialesInvalidas es el correo inexistente o la contraseña que no coincide: responde 401 InvalidCredentials.
var ErrCredencialesInvalidas = errors.New("auth: las credenciales no son válidas")

// ErrCuentaSuspendida es la credencial suspendida con la contrasena correcta: responde 403 AccountSuspended.
var ErrCuentaSuspendida = errors.New("auth: la cuenta está suspendida")

// ErrSinRolDeEmpresa es el login con contexto empresa sin ningún rol vigente: responde 403 NoCompanyRole.
var ErrSinRolDeEmpresa = errors.New("auth: la parte no tiene un rol de empresa vigente")

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
	case errors.Is(err, ErrCelularSinVerificar):
		return platformerrors.New("PhoneUnverified", "The phone number is not verified.", http.StatusForbidden)
	case errors.Is(err, ErrConsentimientoRequerido):
		return platformerrors.New("ConsentRequired", "The required consent is missing.", http.StatusForbidden)
	case errors.Is(err, ErrNombreInvalido), errors.Is(err, ErrCelularInvalido):
		return platformerrors.New("ValidationError", "Request validation failed.", http.StatusUnprocessableEntity)
	case errors.Is(err, ErrEntregaFallida):
		return platformerrors.New("OtpDeliveryFailed", "The code could not be delivered.", http.StatusServiceUnavailable)
	case errors.Is(err, ErrDesafioInvalido):
		return platformerrors.New("ChallengeInvalid", "The challenge is invalid or no longer usable.", http.StatusBadRequest)
	case errors.Is(err, ErrCodigoInvalido):
		return platformerrors.New("OtpInvalid", "The code is invalid.", http.StatusBadRequest)
	case errors.Is(err, ErrCodigoExpirado):
		return platformerrors.New("OtpExpired", "The code has expired.", http.StatusBadRequest)
	case errors.Is(err, ErrDemasiadosIntentos):
		return platformerrors.New("OtpTooManyAttempts", "Too many attempts. Request a new code.", http.StatusTooManyRequests)
	case errors.Is(err, ErrEnfriamientoReenvio):
		return platformerrors.New("OtpResendCooldown", "A new code was requested too soon.", http.StatusTooManyRequests)
	case errors.Is(err, ErrDemasiadosReenvios):
		return platformerrors.New("OtpTooManyResends", "Too many resends. Request a new code.", http.StatusTooManyRequests)
	case errors.Is(err, ErrDemasiadosEnvios):
		return platformerrors.New("TooManyRequests", "Too many requests. Try again later.", http.StatusTooManyRequests)
	case errors.Is(err, ErrCredencialesInvalidas):
		return platformerrors.New("InvalidCredentials", "The email or password is incorrect.", http.StatusUnauthorized)
	case errors.Is(err, ErrCuentaSuspendida):
		return platformerrors.New("AccountSuspended", "The account is suspended.", http.StatusForbidden)
	case errors.Is(err, ErrSinRolDeEmpresa):
		return platformerrors.New("NoCompanyRole", "The account has no active company role.", http.StatusForbidden)
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
