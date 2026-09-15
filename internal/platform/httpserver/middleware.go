package httpserver

import (
	"net/http"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// RequireAuth exige un bearer token presente en el grupo autenticado. La validación real
// del JWT sigue pendiente en internal/auth (ValidateJWT es TODO), así que por ahora
// conserva el comportamiento del wrapper Auth anterior: secreto vacío o token vacío da 401.
func RequireAuth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if jwtSecret == "" || token == "" {
				api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Missing or invalid token.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
