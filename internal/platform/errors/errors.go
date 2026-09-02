package errors

type APIError struct { Message string; Status int }
func (e *APIError) Error() string { return e.Message }
func New(message string, status int) *APIError { return &APIError{Message: message, Status: status} }
