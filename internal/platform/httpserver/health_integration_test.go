//go:build integration

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
)

// TestReadyzNoDependeDeRedis comprueba que un Redis caído no drena los endpoints del núcleo.
func TestReadyzNoDependeDeRedis(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	cliente, err := platformredis.NuevoCliente("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("construir cliente Redis cerrado: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })

	r := NewRouter(pool, nucleo.NewServicio(pool, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: auth.UsuarioDePrueba{},
		Redis:     cliente,
	})
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/readyz con Redis apuntando a 127.0.0.1:1 = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}
