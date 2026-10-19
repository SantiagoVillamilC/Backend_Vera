package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
)

// Envelope es la forma única de toda respuesta; las cuatro claves siempre se serializan.
type Envelope[T any] = EnvelopeConMeta[T, Meta]

// EnvelopeConMeta conserva las cuatro claves con metadata tipada según el contrato de la ruta.
type EnvelopeConMeta[T, M any] struct {
	Success bool      `json:"success"`
	Data    T         `json:"data"`
	Error   *APIError `json:"error"`
	Meta    *M        `json:"meta"`
}

// WriteData responde con un envelope exitoso. meta se deja en nil salvo en listados.
func WriteData[T any](w http.ResponseWriter, status int, data T, meta *Meta) {
	writeJSON(w, status, Envelope[T]{Success: true, Data: data, Meta: meta})
}

// WriteDataConCursor usa la metadata generada del historial sin alterar el cursor del núcleo.
func WriteDataConCursor[T any](w http.ResponseWriter, status int, data T, meta *MetaHistorialVera) {
	writeJSON(w, status, EnvelopeConMeta[T, MetaHistorialVera]{Success: true, Data: data, Meta: meta})
}

// WriteError responde con un envelope de error a partir de sus piezas.
func WriteError(w http.ResponseWriter, status int, code, msg string, details []FieldError) {
	errObj := &APIError{Code: code, Message: msg}
	if len(details) > 0 {
		errObj.Details = &details
	}
	writeJSON(w, status, Envelope[any]{Success: false, Error: errObj})
}

// WriteAPIError convierte un error de dominio: un 4xx conserva su código y el resto sale como 500 genérico.
func WriteAPIError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *platformerrors.APIError
	if errors.As(err, &apiErr) && apiErr.Status >= http.StatusBadRequest && apiErr.Status < http.StatusInternalServerError {
		WriteError(w, apiErr.Status, apiErr.Code, apiErr.Message, nil)
		return
	}
	slog.ErrorContext(r.Context(), "unhandled request error",
		"err", err, "method", r.Method, "path", r.URL.Path)
	WriteError(w, http.StatusInternalServerError, "InternalError", "Something went wrong.", nil)
}

// ParamError traduce los errores de binding del wrapper generado al envelope, nunca a texto plano.
func ParamError(w http.ResponseWriter, _ *http.Request, err error) {
	var (
		requerido *RequiredParamError
		formato   *InvalidParamFormatError
	)
	switch {
	case errors.As(err, &requerido):
		WriteError(w, http.StatusBadRequest, "BadRequest", "A required parameter is missing.",
			[]FieldError{{Field: requerido.ParamName, Rule: "required"}})
	case errors.As(err, &formato):
		WriteError(w, http.StatusBadRequest, "BadRequest", "A parameter has an invalid format.",
			[]FieldError{{Field: formato.ParamName, Rule: "format"}})
	default:
		WriteError(w, http.StatusBadRequest, "BadRequest", "Malformed request parameters.", nil)
	}
}

// writeJSON serializa el envelope en un buffer antes de tocar el ResponseWriter, para no dejar un 200 truncado.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		slog.Error("envelope encode failed", "err", err)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"data":null,"error":{"code":"InternalError","message":"Something went wrong."},"meta":null}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
