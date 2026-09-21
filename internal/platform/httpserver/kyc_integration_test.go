//go:build integration

package httpserver

import (
	"net/http"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/kyc"
)

// El usuario de prueba del sandbox sigue respondiendo por HTTP gracias al lector real.
func TestKYCRealUsuarioDePruebaRespondePorHTTP(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	r := NewRouter(pool, DependenciasAuth{
		Identidad: auth.UsuarioDePrueba{},
		KYC:       NuevoKYC(kyc.NuevoServicio(pool, nil, "simulador", subcuentasSinNucleo{})),
	})

	rec := pedirKYC(t, r, http.MethodGet, "/v1/kyc/estado", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}
