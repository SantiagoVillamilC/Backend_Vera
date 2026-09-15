package auth

import (
	"context"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/repositorio"
)

type Service struct{ q repositorio.Querier }

func NewService(q repositorio.Querier) *Service { return &Service{q: q} }
func (s *Service) Login(ctx context.Context, req LoginRequest) (Session, error) {
	return Session{}, fmt.Errorf("TODO: authenticate user")
}
func (s *Service) Register(ctx context.Context, req LoginRequest) (User, error) {
	return User{}, fmt.Errorf("TODO: register user")
}
func (s *Service) ValidateJWT(token string) (User, error) {
	return User{}, fmt.Errorf("TODO: validate JWT")
}
func (s *Service) GenerateTOTP(userID string) (string, error) {
	return "", fmt.Errorf("TODO: generate TOTP")
}
