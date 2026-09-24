// Package auth implementa el registro, la activación, las sesiones y la verificación de identidad de la billetera.
package auth

import "github.com/SantiagoVillamilC/Backend_Centinela/internal/repositorio"

// Service es el punto de entrada del módulo; los DTOs viven en platform/api, no acá.
type Service struct{ q repositorio.Querier }

// NewService construye el service sobre el repositorio de auth.
func NewService(q repositorio.Querier) *Service { return &Service{q: q} }
