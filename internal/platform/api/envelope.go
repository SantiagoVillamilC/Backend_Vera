// Package api define el envelope de respuesta compartido por todos los handlers HTTP.
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
type Envelope[T any] struct {
	Success bool      `json:"success"`
	Data    T         `json:"data"`
	Error   *APIError `json:"error"`
	Meta    *Meta     `json:"meta"`
}

// APIError es el objeto de error del envelope; Code es estable y Message es legible por una persona.
type APIError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldError `json:"details,omitempty"`
}

// FieldError describe un campo que no pasó la validación del cuerpo de la petición.
type FieldError struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
	Param string `json:"param,omitempty"`
}

// Meta transporta la paginación por cursor. Solo se llena en endpoints de listado.
type Meta struct {
	Limit      int    `json:"limit"`
	NextCursor *int64 `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
}

// WriteData responde con un envelope exitoso. meta se deja en nil salvo en listados.
func WriteData[T any](w http.ResponseWriter, status int, data T, meta *Meta) {
	writeJSON(w, status, Envelope[T]{Success: true, Data: data, Meta: meta})
}

// WriteError responde con un envelope de error a partir de sus piezas.
func WriteError(w http.ResponseWriter, status int, code, msg string, details []FieldError) {
	writeJSON(w, status, Envelope[any]{Success: false, Error: &APIError{Code: code, Message: msg, Details: details}})
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
