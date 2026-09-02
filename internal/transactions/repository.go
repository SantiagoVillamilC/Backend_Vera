package transactions

import "context"
type Repository interface { Create(context.Context,Transaction)(Transaction,error); UpdateStatus(context.Context,string,string) error }
