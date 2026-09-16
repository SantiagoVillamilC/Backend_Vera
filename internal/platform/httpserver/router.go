package httpserver

import (
	"net/http"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/receipts"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/repositorio"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	publicTimeout     = 15 * time.Second
	authedTimeout     = 60 * time.Second
	rateLimitRequests = 100
	rateLimitWindow   = time.Minute
)

// NewRouter arma el árbol de rutas con la cadena de middleware, el envelope y la identidad inyectada.
func NewRouter(pool *pgxpool.Pool, quien IdentidadDeLaPeticion, nucleoServicio *nucleo.Servicio) http.Handler {
	r := chi.NewRouter()

	r.Use(RequestID())
	r.Use(middleware.ClientIPFromHeader("X-Real-IP"))
	r.Use(logging.AccessLogger())
	r.Use(Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-Test-User"},
		MaxAge:         300,
	}))
	r.Use(RateLimitExempting([]string{"/healthz", "/readyz"}, rateLimitRequests, rateLimitWindow))
	r.Use(MethodNotAllowedEnvelope)

	authHandler := auth.NewHandler(auth.NewService(repositorio.New(pool)))
	copilotHandler := copilot.NewHandler(copilot.NewService(nil))
	receiptHandler := receipts.NewHandler(receipts.NewService(nil))
	health := NewHealthResource(pool)
	nucleoHandler := nucleo.NewHandler(nucleoServicio)

	// Grupo público: cada grupo declara su propio Timeout, nunca en la raíz.
	r.Group(func(r chi.Router) {
		r.Use(Timeout(publicTimeout))
		r.Post("/login", authHandler.Login)
		r.Post("/register", authHandler.Register)
		r.Post("/mfa/verify", authHandler.VerifyMFA)
		r.Get("/healthz", health.Live)
		r.Get("/readyz", health.Ready)
	})

	// Grupo autenticado: RequireAuth deja el usuario en el contexto y solo acá corre.
	r.Group(func(r chi.Router) {
		r.Use(Timeout(authedTimeout))
		r.Use(RequireAuth(quien))
		r.Get("/saldo", nucleoHandler.Saldo)
		r.Get("/movimientos", nucleoHandler.Movimientos)
		r.Post("/copilot", copilotHandler.Chat)
		r.Post("/receipts", receiptHandler.Create)
	})

	// Los handlers por defecto de chi responden texto plano, así que el 404 va dentro del envelope.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusNotFound, "NotFound", "Resource not found.", nil)
	})

	return r
}
