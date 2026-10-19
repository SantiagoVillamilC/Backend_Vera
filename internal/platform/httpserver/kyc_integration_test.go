//go:build integration

package httpserver

import (
	"net/http"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/kyc"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/kycsim"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sembrarKYCDeIntegracion siembra una parte del tipo pedido con rol_auth.
func sembrarKYCDeIntegracion(t *testing.T, pool *pgxpool.Pool, tipo string) string {
	t.Helper()
	id := uuid.New()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+auth.RolAuth); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if _, err := tx.Exec(t.Context(), "INSERT INTO auth.partes (id, tipo) VALUES ($1, $2)", id, tipo); err != nil {
		t.Fatalf("sembrar parte %s: %v", tipo, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id.String()
}

// routerKYCReal arma el router con el lector real inyectado en el núcleo y el handler real de KYC.
func routerKYCReal(t *testing.T, pool *pgxpool.Pool, parteID string, servicio *nucleo.Servicio) http.Handler {
	t.Helper()
	simulador, err := kycsim.Nuevo("verificado")
	if err != nil {
		t.Fatalf("simulador: %v", err)
	}
	modulo := kyc.NuevoServicio(pool, simulador, "simulador", servicio)
	return NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{
		Identidad: identidadFija{usuarioID: parteID},
		KYC:       NuevoKYC(modulo),
	})
}

// La persona sigue operando como con LectorNivelKYCFijo y su estado responde bajo monto.
func TestKYCRealPersonaOperaYRespondeBajoMonto(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, kyc.NuevoLectorNivelKYC(pool), nil, nil, nil,
		nucleo.ConResolutorDestino(auth.NuevoResolutorDestino(pool, 24*time.Hour)))
	emisor := sembrarUsuarioConSaldo(t, pool, servicio, 1000000, 0)
	receptor := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := routerKYCReal(t, pool, emisor, servicio)

	estado := pedirKYC(t, r, http.MethodGet, "/v1/kyc/estado", "", true)
	if estado.Code != http.StatusOK {
		t.Fatalf("estado persona: status = %d (cuerpo %q)", estado.Code, estado.Body.String())
	}
	data, _ := decodeEnvelope(t, estado)["data"].(map[string]any)
	if data["producto"] != kyc.ProductoBajoMonto {
		t.Fatalf("producto = %v, quiero bajo_monto", data["producto"])
	}

	transfer := pedirTransferenciaAutenticada(t, r, cuerpoTransferencia(receptor, 1000, "k-"+uuid.NewString()))
	if transfer.Code != http.StatusCreated {
		t.Fatalf("transferencia persona: status = %d, quiero 201 (cuerpo %q)", transfer.Code, transfer.Body.String())
	}
}

// H5 con fail closed: la transferencia de una empresa responde 403 KycRequired en envelope.
func TestKYCRealEmpresaNoOpera(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, kyc.NuevoLectorNivelKYC(pool), nil, nil, nil,
		nucleo.ConResolutorDestino(auth.NuevoResolutorDestino(pool, 24*time.Hour)))
	empresa := sembrarKYCDeIntegracion(t, pool, kyc.TipoEmpresa)
	receptor := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := routerKYCReal(t, pool, empresa, servicio)

	estado := pedirKYC(t, r, http.MethodGet, "/v1/kyc/estado", "", true)
	if estado.Code != http.StatusOK {
		t.Fatalf("estado empresa: status = %d (cuerpo %q)", estado.Code, estado.Body.String())
	}
	data, _ := decodeEnvelope(t, estado)["data"].(map[string]any)
	if data["producto"] != kyc.ProductoDepositoOrdinario {
		t.Fatalf("producto = %v, quiero deposito_ordinario", data["producto"])
	}

	transfer := pedirTransferenciaAutenticada(t, r, cuerpoTransferencia(receptor, 1000, "k-"+uuid.NewString()))
	assertErrorEnvelope(t, transfer, http.StatusForbidden, "KycRequired")
}

// El usuario de prueba del sandbox sigue respondiendo por HTTP gracias al lector real.
func TestKYCRealUsuarioDePruebaRespondePorHTTP(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, kyc.NuevoLectorNivelKYC(pool), nil, nil, nil)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{
		Identidad: auth.UsuarioDePrueba{},
		KYC:       NuevoKYC(kyc.NuevoServicio(pool, nil, "simulador", servicio)),
	})

	rec := pedirKYC(t, r, http.MethodGet, "/v1/kyc/estado", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}
