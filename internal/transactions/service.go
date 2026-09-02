package transactions

import ("context"; "fmt")

// WalletBalanceReader is deliberately exposed by the wallets service; transactions never imports its repository.
type WalletBalanceReader interface { GetBalance(context.Context,string)(float64,error) }
type Service struct { repository Repository; wallets WalletBalanceReader; confirmation *ConfirmationService; publisher RealtimePublisher }
func NewService(repository Repository, wallets WalletBalanceReader, confirmation *ConfirmationService, publisher RealtimePublisher) *Service { return &Service{repository:repository,wallets:wallets,confirmation:confirmation,publisher:publisher} }
func (s *Service) Execute(ctx context.Context, req CreateTransactionRequest) (Transaction,error) {
	if req.Amount<=0{return Transaction{},fmt.Errorf("amount must be greater than zero")}; if s.confirmation==nil{return Transaction{},fmt.Errorf("confirmation service is required")}; if err:=s.confirmation.Require(ctx,"",req.ConfirmationToken);err!=nil{return Transaction{},err}; if s.wallets==nil{return Transaction{},fmt.Errorf("wallet service is not configured")}; balance,err:=s.wallets.GetBalance(ctx,req.FromWalletID);if err!=nil{return Transaction{},err};if balance<req.Amount{return Transaction{},fmt.Errorf("insufficient funds")}; return Transaction{},fmt.Errorf("TODO: persist and publish transaction")
}
