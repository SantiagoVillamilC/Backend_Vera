package auth

import ("context"; "fmt")

type Service struct { repository Repository }
func NewService(repository Repository) *Service { return &Service{repository: repository} }
func (s *Service) Login(ctx context.Context, req LoginRequest) (Session, error) { return Session{}, fmt.Errorf("TODO: authenticate user") }
func (s *Service) Register(ctx context.Context, req LoginRequest) (User, error) { return User{}, fmt.Errorf("TODO: register user") }
func (s *Service) ValidateJWT(token string) (User, error) { return User{}, fmt.Errorf("TODO: validate JWT") }
func (s *Service) GenerateTOTP(userID string) (string, error) { return "", fmt.Errorf("TODO: generate TOTP") }
