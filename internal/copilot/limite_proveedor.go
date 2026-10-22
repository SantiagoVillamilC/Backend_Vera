package copilot

import "time"

// ClaseLimite describe sólo dimensiones públicas de cuota, nunca detalles del SDK.
type ClaseLimite string

// Clases conocidas de cuota del proveedor.
const (
	LimiteMinuto      ClaseLimite = "minuto"
	LimiteDia         ClaseLimite = "dia"
	LimiteDesconocido ClaseLimite = "desconocido"
)

// ErrorLimiteIA conserva una sugerencia de espera acotada sin mensajes del proveedor.
type ErrorLimiteIA struct {
	Clase        ClaseLimite
	ReintentarEn time.Duration
}

func (*ErrorLimiteIA) Error() string { return string(ErrLimiteIA) }

// Unwrap conserva la categoría pública para los servicios existentes.
func (*ErrorLimiteIA) Unwrap() error { return ErrLimiteIA }
