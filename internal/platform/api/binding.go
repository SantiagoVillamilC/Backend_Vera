package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/go-playground/validator/v10"
)

// validate es la instancia única por proceso que cachea metadatos y es segura para uso concurrente.
var validate = validator.New(validator.WithRequiredStructEnabled())

func init() {
	validate.RegisterTagNameFunc(nombreDeCampoJSON)
	_ = validate.RegisterValidation("nit", validarNIT)
	_ = validate.RegisterValidation("e164", validarE164)
}

// nombreDeCampoJSON usa la etiqueta json, sin opciones, como nombre del campo en los detalles.
func nombreDeCampoJSON(fld reflect.StructField) string {
	nombre := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
	if nombre == "-" || nombre == "" {
		return fld.Name
	}
	return nombre
}

// validarNIT comprueba el digito de verificacion DIAN (modulo 11) de un NIT.
func validarNIT(fl validator.FieldLevel) bool {
	nit := strings.NewReplacer(".", "", "-", "", " ", "").Replace(strings.TrimSpace(fl.Field().String()))
	if len(nit) < 2 || len(nit) > 15 {
		return false
	}
	cuerpo, verif := nit[:len(nit)-1], nit[len(nit)-1]
	if verif < '0' || verif > '9' {
		return false
	}
	pesos := []int{3, 7, 13, 17, 19, 23, 29, 37, 41, 43, 47, 53, 59, 67, 71}
	suma := 0
	for i := 0; i < len(cuerpo); i++ {
		d := cuerpo[len(cuerpo)-1-i]
		if d < '0' || d > '9' || i >= len(pesos) {
			return false
		}
		suma += int(d-'0') * pesos[i]
	}
	digito := suma % 11
	if digito > 1 {
		digito = 11 - digito
	}
	return digito == int(verif-'0')
}

// validarE164 acepta un celular E.164: signo mas y de 1 a 15 digitos.
func validarE164(fl validator.FieldLevel) bool {
	v := strings.TrimSpace(fl.Field().String())
	if len(v) < 2 || len(v) > 16 || v[0] != '+' {
		return false
	}
	for i := 1; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return false
		}
	}
	return true
}

// DecodeAndValidate decodifica y valida el cuerpo, devolviendo false tras escribir 400 o 422.
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
	if !validateStruct(w, dst) {
		return false
	}
	return true
}

// DecodeDiscriminated lee el cuerpo una vez, elige el tipo por el discriminador y lo valida.
func DecodeDiscriminated(w http.ResponseWriter, r *http.Request, campo string, destinos map[string]any) (any, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	crudo, err := io.ReadAll(r.Body)
	if err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			WriteError(w, http.StatusRequestEntityTooLarge, "PayloadTooLarge", "Request body exceeds 1 MiB.", nil)
		} else {
			WriteError(w, http.StatusBadRequest, "BadRequest", "Request body is not valid JSON.", nil)
		}
		return nil, false
	}
	var peek map[string]json.RawMessage
	if err := json.Unmarshal(crudo, &peek); err != nil {
		WriteError(w, http.StatusBadRequest, "BadRequest", "Request body is not valid JSON.", nil)
		return nil, false
	}
	var valor string
	if err := json.Unmarshal(peek[campo], &valor); err != nil {
		writeDiscriminadorInvalido(w, campo, destinos)
		return nil, false
	}
	dst, ok := destinos[valor]
	if !ok {
		writeDiscriminadorInvalido(w, campo, destinos)
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(crudo))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		WriteError(w, http.StatusBadRequest, "BadRequest", "Request body is not valid JSON.", nil)
		return nil, false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		WriteError(w, http.StatusBadRequest, "BadRequest", "Body must contain a single JSON object.", nil)
		return nil, false
	}
	if !validateStruct(w, dst) {
		return nil, false
	}
	return dst, true
}

// validateStruct escribe el 422 con los detalles en snake_case cuando una regla no se cumple.
func validateStruct(w http.ResponseWriter, dst any) bool {
	var ve validator.ValidationErrors
	if err := validate.Struct(dst); errors.As(err, &ve) {
		details := make([]FieldError, 0, len(ve))
		for _, fe := range ve {
			campo := FieldError{Field: campoEnSnakeCase(fe.Namespace()), Rule: fe.Tag()}
			if param := fe.Param(); param != "" {
				campo.Param = &param
			}
			details = append(details, campo)
		}
		WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.", details)
		return false
	} else if err != nil {
		WriteError(w, http.StatusInternalServerError, "InternalError", "Something went wrong.", nil)
		return false
	}
	return true
}

// campoEnSnakeCase quita el prefijo del tipo raiz y deja la ruta anidada con los tags json.
func campoEnSnakeCase(ns string) string {
	if i := strings.IndexByte(ns, '.'); i >= 0 {
		return ns[i+1:]
	}
	return ns
}

// writeDiscriminadorInvalido responde 422 cuando el discriminador falta o no esta en el conjunto.
func writeDiscriminadorInvalido(w http.ResponseWriter, campo string, destinos map[string]any) {
	claves := make([]string, 0, len(destinos))
	for k := range destinos {
		claves = append(claves, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(claves)))
	param := strings.Join(claves, " ")
	WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
		[]FieldError{{Field: campo, Rule: "oneof", Param: &param}})
}
