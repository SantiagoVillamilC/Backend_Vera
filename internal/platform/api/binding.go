package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-playground/validator/v10"
)

// validate es una sola instancia por proceso: cachea los metadatos de cada struct y es
// segura para uso concurrente. Nunca se construye dentro de un handler.
var validate = validator.New(validator.WithRequiredStructEnabled())

// DecodeAndValidate decodifica un cuerpo JSON en dst y valida sus etiquetas validate.
// Devuelve false cuando ya escribió una respuesta de error, y en ese caso el handler debe
// retornar sin escribir nada más. Un JSON mal formado da 400, y una regla incumplida da
// 422 con el detalle por campo.
func DecodeAndValidate(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			WriteError(w, http.StatusRequestEntityTooLarge, "PayloadTooLarge", "Request body exceeds 1 MiB.", nil)
		} else {
			WriteError(w, http.StatusBadRequest, "BadRequest", "Request body is not valid JSON.", nil)
		}
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, "BadRequest", "Body must contain a single JSON object.", nil)
		return false
	}
	var ve validator.ValidationErrors
	if err := validate.Struct(dst); errors.As(err, &ve) {
		details := make([]FieldError, 0, len(ve))
		for _, fe := range ve {
			details = append(details, FieldError{Field: fe.Field(), Rule: fe.Tag(), Param: fe.Param()})
		}
		WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.", details)
		return false
	} else if err != nil {
		WriteError(w, http.StatusInternalServerError, "InternalError", "Something went wrong.", nil)
		return false
	}
	return true
}
