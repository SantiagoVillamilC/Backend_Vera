package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUsuarioDePruebaUsuarioID(t *testing.T) {
	adapter := UsuarioDePrueba{}

	tests := map[string]struct {
		preparar  func(*http.Request)
		wantID    string
		wantError error
	}{
		"cabecera con valor": {
			preparar: func(r *http.Request) { r.Header.Set(CabeceraUsuarioDePrueba, "1") },
			wantID:   UsuarioDePruebaID,
		},
		"sin cabecera": {
			preparar:  func(*http.Request) {},
			wantError: ErrSinIdentidad,
		},
		"cabecera vacía": {
			preparar:  func(r *http.Request) { r.Header.Set(CabeceraUsuarioDePrueba, "") },
			wantError: ErrSinIdentidad,
		},
		"cabecera en blanco": {
			preparar:  func(r *http.Request) { r.Header.Set(CabeceraUsuarioDePrueba, "   ") },
			wantError: ErrSinIdentidad,
		},
		"un bearer no sustituye la cabecera": {
			preparar:  func(r *http.Request) { r.Header.Set("Authorization", "Bearer token") },
			wantError: ErrSinIdentidad,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			tc.preparar(req)

			got, err := adapter.UsuarioID(req)

			if !errors.Is(err, tc.wantError) {
				t.Fatalf("error = %v, quiero %v", err, tc.wantError)
			}
			if got != tc.wantID {
				t.Fatalf("usuarioID = %q, quiero %q", got, tc.wantID)
			}
		})
	}
}
