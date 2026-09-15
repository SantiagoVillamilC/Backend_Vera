package httpserver

import (
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/receipts"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/transactions"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/wallets"
	"github.com/jackc/pgx/v5/pgxpool"
)

func NewRouter(pool *pgxpool.Pool, jwtSecret string) http.Handler {
	mux := http.NewServeMux()

	authHandler := auth.NewHandler(auth.NewService(repositorio.New(pool)))
	walletService := wallets.NewService(nil)
	walletHandler := wallets.NewHandler(walletService)
	transactionHandler := transactions.NewHandler(transactions.NewService(nil, walletService, transactions.NewConfirmationService(nil), nil))
	copilotHandler := copilot.NewHandler(copilot.NewService(nil))
	receiptHandler := receipts.NewHandler(receipts.NewService(nil))
	health := NewHealthResource(pool)

	mux.HandleFunc("POST /login", authHandler.Login)
	mux.HandleFunc("POST /register", authHandler.Register)
	mux.HandleFunc("POST /mfa/verify", authHandler.VerifyMFA)
	mux.HandleFunc("GET /healthz", health.Live)
	mux.HandleFunc("GET /readyz", health.Ready)
	mux.HandleFunc("GET /wallets", walletHandler.List)
	mux.HandleFunc("POST /wallets", walletHandler.Create)
	mux.HandleFunc("POST /transactions", transactionHandler.Create)
	mux.HandleFunc("POST /copilot", copilotHandler.Chat)
	mux.HandleFunc("POST /receipts", receiptHandler.Create)

	return Logging(Auth(jwtSecret, mux))
}
