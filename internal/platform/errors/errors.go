// Package errors define el error tipado que la capa HTTP convierte en envelope.
package errors

// APIError es un error de dominio con status y código HTTP asociados. La capa HTTP lo
// mapea al envelope y nunca serializa su causa interna.
type APIError struct {
	Code    string
	Message string
	Status  int
}

// Error implementa la interfaz error.
func (e *APIError) Error() string { return e.Message }

// New construye un APIError con su código de envelope y su status HTTP.
func New(code, message string, status int) *APIError {
	return &APIError{Code: code, Message: message, Status: status}
}
