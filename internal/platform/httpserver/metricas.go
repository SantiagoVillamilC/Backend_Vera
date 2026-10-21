package httpserver

import (
	"errors"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// ErrMetricasSinToken reporta la construcción del router con un handler de métricas sin autenticador.
var ErrMetricasSinToken = errors.New("httpserver: /metrics necesita un autenticador no nulo")

// AutenticadorDeMetricas decide si el token presentado puede leer las métricas. Es el mismo contrato
// que ya cumple el token estático del custodio, con su propia credencial.
type AutenticadorDeMetricas interface {
	TokenValido(presentado string) bool
}

// MetricasDelRouter es la superficie de métricas que main inyecta en el router. Con Handler nulo el
// router no monta la ruta y /metrics responde el 404 del envelope.
type MetricasDelRouter struct {
	Handler      http.Handler
	Autenticador AutenticadorDeMetricas
}

// RequireMetricas exige el token de métricas en Authorization: Bearer antes de servir la exposición.
func RequireMetricas(autenticador AutenticadorDeMetricas) func(http.Handler) http.Handler {
	if autenticador == nil {
		panic(ErrMetricasSinToken)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !autenticador.TokenValido(tokenPortador(r.Header.Get("Authorization"))) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
