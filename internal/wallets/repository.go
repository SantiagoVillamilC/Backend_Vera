package wallets

import "context"
type Repository interface { ListByUser(context.Context,string) ([]Wallet,error); Create(context.Context,Wallet) (Wallet,error); Balance(context.Context,string) (float64,error) }
