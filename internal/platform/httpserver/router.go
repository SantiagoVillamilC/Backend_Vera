package httpserver

import (
	"net/http"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/receipts"
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

// RutasDeDinero son las operaciones sensibles que cierran si la denylist no responde.
var RutasDeDinero = []string{
	http.MethodPost + " /v1/transferencias",
	http.MethodPost + " /v1/depositos",
	http.MethodPost + " /v1/transferencias/interbancarias",
	http.MethodGet + " /v1/destinatarios",
}

// NewRouter arma el árbol de rutas con la cadena de middleware, el envelope y las identidades inyectadas.
func NewRouter(pool *pgxpool.Pool, nucleoServicio *nucleo.Servicio, autenticadorCustodio AutenticadorDelCustodio, authDeps DependenciasAuth) http.Handler {
	if autenticadorCustodio == nil {
		panic(ErrCustodioNulo)
	}
	r := chi.NewRouter()

	r.Use(RequestID())
	r.Use(middleware.ClientIPFromHeader("X-Real-IP"))
	r.Use(logging.AccessLogger())
	r.Use(Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions, http.MethodDelete},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
		MaxAge:         300,
	}))
	r.Use(RateLimitExempting([]string{"/healthz", "/readyz"}, rateLimitRequests, rateLimitWindow))
	r.Use(MethodNotAllowedEnvelope)

	copilotHandler := copilot.NewHandler(copilot.NewService(nil))
	receiptHandler := receipts.NewHandler(receipts.NewService(nil))
	health := NewHealthResource(pool)
	nucleoHandler := nucleo.NewHandler(nucleoServicio)
	srv := servidorV1{auth: authDeps.Auth, kyc: authDeps.KYC, consent: authDeps.Consent, phoneChange: authDeps.PhoneChange, companies: authDeps.Companies, nucleo: nucleoHandler, destinatarios: authDeps.Destinatarios}

	// Grupo público: cada grupo declara su propio Timeout, nunca en la raíz.
	r.Group(func(r chi.Router) {
		r.Use(Timeout(publicTimeout))
		r.Post("/v1/auth/register", registroDePersona(authDeps.Registro))
		r.Post("/v1/auth/register/verify-phone", verificacionDeCelular(authDeps.Verificacion))
		r.Post("/v1/auth/otp/resend", reenvioDeOtp(authDeps.Desafios))
		r.Post("/v1/auth/login", loginDeSesion(authDeps.Login))
		r.Post("/v1/auth/refresh", rotacionDeRefresh(authDeps.Refresh))
		r.Post("/v1/auth/recovery/request", recuperacionDeSesion(authDeps.Recuperacion))
		r.Post("/v1/auth/recovery/verify", verificacionDeRecuperacion(authDeps.Recuperar2FA))
		r.Post("/v1/auth/recovery/reset", restablecimientoDeContrasena(authDeps.ResetClave))
		r.Get("/healthz", health.Live)
		r.Get("/readyz", health.Ready)
	})

	// Grupo autenticado: RequireAuth deja la identidad completa y solo acá corre.
	r.Group(func(r chi.Router) {
		r.Use(Timeout(authedTimeout))
		r.Use(RequireAuth(authDeps.Identidad, RutasDeDinero))
		r.Post("/copilot", copilotHandler.Chat)
		r.Post("/receipts", receiptHandler.Create)
		api.HandlerWithOptions(srv, api.ChiServerOptions{BaseRouter: r, ErrorHandlerFunc: api.ParamError})
	})

	// Grupo del custodio simulado: token estático, sin identidad de usuario.
	r.Group(func(r chi.Router) {
		r.Use(Timeout(authedTimeout))
		r.Use(RequireCustodio(autenticadorCustodio))
		r.Post("/v1/depositos/confirmaciones", nucleoHandler.ConfirmarDeposito)
	})

	// Los handlers por defecto de chi responden texto plano, así que el 404 va dentro del envelope.
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteError(w, http.StatusNotFound, "NotFound", "Resource not found.", nil)
	})

	return r
}
