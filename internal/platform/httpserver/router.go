package httpserver

import (
	"net/http"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/receipts"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/repositorio"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	publicTimeout     = 15 * time.Second
	authedTimeout     = 60 * time.Second
	rateLimitRequests = 100
	rateLimitWindow   = time.Minute
)

// NewRouter arma el árbol de rutas con la cadena de middleware y el envelope común.
//
// El orden de la cadena es deliberado: RequestID (server-generated) primero, luego un
// único ClientIPFrom* (nginx sobrescribe X-Real-IP con el peer TCP en
// infra/servidor/pasos/30-nginx.sh y el puerto de la API no se publica, así que el header
// no es falsificable desde fuera), el logger de acceso, Recoverer, CORS en la raíz y por
// último el rate limit, para que un 429 siga trayendo los headers de CORS. Recoverer y
// Timeout son propios y responden dentro del envelope.
func NewRouter(pool *pgxpool.Pool, jwtSecret string) http.Handler {
	r := chi.NewRouter()

	r.Use(RequestID())
	r.Use(middleware.ClientIPFromHeader("X-Real-IP"))
	r.Use(logging.AccessLogger())
	r.Use(Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
		MaxAge:         300,
	}))
	r.Use(httprate.LimitBy(rateLimitRequests, rateLimitWindow, clientIPKey, httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusTooManyRequests, "TooManyRequests", "Rate limit exceeded.", nil)
	})))

	authHandler := auth.NewHandler(auth.NewService(repositorio.New(pool)))
	copilotHandler := copilot.NewHandler(copilot.NewService(nil))
	receiptHandler := receipts.NewHandler(receipts.NewService(nil))
	health := NewHealthResource(pool)

	// Grupo público: cada grupo declara su propio Timeout, nunca en la raíz.
	r.Group(func(r chi.Router) {
		r.Use(Timeout(publicTimeout))
		r.Post("/login", authHandler.Login)
		r.Post("/register", authHandler.Register)
		r.Post("/mfa/verify", authHandler.VerifyMFA)
		r.Get("/healthz", health.Live)
		r.Get("/readyz", health.Ready)
	})

	// Grupo autenticado: Timeout y RequireAuth solo acá. Copilot y receipts llaman
	// clientes externos con su propio presupuesto de retry, por eso el Timeout mayor.
	r.Group(func(r chi.Router) {
		r.Use(Timeout(authedTimeout))
		r.Use(RequireAuth(jwtSecret))
		r.Post("/copilot", copilotHandler.Chat)
		r.Post("/receipts", receiptHandler.Create)
	})

	// Los handlers por defecto de chi responden texto plano, que rompería el envelope.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusNotFound, "NotFound", "Resource not found.", nil)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "Method not allowed.", nil)
	})

	return r
}
