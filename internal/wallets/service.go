package wallets

import ("context"; "fmt")
type Service struct { repository Repository }
func NewService(repository Repository) *Service { return &Service{repository:repository} }
func (s *Service) List(ctx context.Context, userID string) ([]Wallet,error) { if s.repository==nil{return []Wallet{},nil}; return s.repository.ListByUser(ctx,userID) }
func (s *Service) Create(ctx context.Context, userID string, req CreateWalletRequest) (Wallet,error) { return Wallet{},fmt.Errorf("TODO: create wallet") }
func (s *Service) GetBalance(ctx context.Context, walletID string) (float64,error) { if s.repository==nil{return 0,fmt.Errorf("wallet repository is not configured")}; return s.repository.Balance(ctx,walletID) }
