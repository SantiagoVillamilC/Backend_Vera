package transactions

type Transaction struct { ID, FromWalletID, ToWalletID, UserID string; Amount float64; Status string }
type CreateTransactionRequest struct { FromWalletID, ToWalletID string; Amount float64; ConfirmationToken string }
