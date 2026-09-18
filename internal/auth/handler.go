package auth

// Handler expone los endpoints autenticados de autenticación al servidorV1.
type Handler struct{ service *Service }

// NewHandler construye el handler sobre el service de auth.
func NewHandler(s *Service) *Handler { return &Handler{service: s} }
