package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// AutenticadorDelCustodio responde si el token presentado es el del custodio simulado.
type AutenticadorDelCustodio interface {
	TokenValido(presentado string) bool
}

// ErrCustodioNulo reporta la construcción del router sin el autenticador del custodio.
var ErrCustodioNulo = errors.New("httpserver: NewRouter necesita un AutenticadorDelCustodio no nulo")

// RequireCustodio exige el token del custodio simulado y responde 401 en cualquier otro caso.
func RequireCustodio(autenticador AutenticadorDelCustodio) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if autenticador == nil || !autenticador.TokenValido(tokenPortador(r.Header.Get("Authorization"))) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// tokenPortador extrae el token de un header Authorization: Bearer; sin el esquema devuelve "".
func tokenPortador(header string) string {
	const esquema = "Bearer "
	if len(header) <= len(esquema) || !strings.EqualFold(header[:len(esquema)], esquema) {
		return ""
	}
	return strings.TrimSpace(header[len(esquema):])
}
