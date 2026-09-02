package wallets

type Wallet struct { ID, UserID, Name string; Balance float64 }
type CreateWalletRequest struct { Name string; Budget float64 }
