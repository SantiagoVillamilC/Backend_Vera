//go:build integration

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// servicioConResolutor arma el servicio del núcleo con el resolutor real de auth y el nivel fijo de KYC.
func servicioConResolutor(pool *pgxpool.Pool) *nucleo.Servicio {
	return nucleo.NewServicio(pool, nucleo.LectorNivelKYCFijo{}, nil, nil, nil,
		nucleo.ConResolutorDestino(auth.NuevoResolutorDestino(pool, 24*time.Hour)))
}

// pedirTransferenciaAutenticada manda el envío por el router real con la credencial de prueba.
func pedirTransferenciaAutenticada(t *testing.T, r http.Handler, cuerpo string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/transferencias", strings.NewReader(cuerpo))
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// cuerpoTransferencia arma el cuerpo JSON del envío con el monto en centavos.
func cuerpoTransferencia(destino string, monto int64, clave string) string {
	return `{"destino":"` + destino + `","monto":"` + strconv.FormatInt(monto, 10) +
		`","idempotency_key":"` + clave + `"}`
}

// HU-NF-06 Criterio 3: un monto que supera el disponible, incluido el saldo cero, se rechaza por saldo insuficiente.
func TestHU_NF_06_Criterio3_EnvioSinSaldoSeRechaza(t *testing.T) {
	casos := map[string]int64{
		"saldo cero":           0,
		"saldo menor al monto": 500,
	}

	for nombre, saldo := range casos {
		t.Run(nombre, func(t *testing.T) {
			pool := abrirPoolDeIntegracion(t)
			servicio := servicioConResolutor(pool)
			emisor := sembrarUsuarioConSaldo(t, pool, servicio, saldo, 0)
			destino := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
			r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{emisor}})

			rec := pedirTransferenciaAutenticada(t, r, cuerpoTransferencia(destino, 1000, "k-"+uuid.NewString()))

			assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "InsufficientFunds")
		})
	}
}

// HU-NF-06 Criterio 4: un destino que no corresponde a ningún usuario se rechaza.
func TestHU_NF_06_Criterio4_DestinoInexistenteSeRechaza(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := servicioConResolutor(pool)
	emisor := sembrarUsuarioConSaldo(t, pool, servicio, 1000000, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{emisor}})

	rec := pedirTransferenciaAutenticada(t, r, cuerpoTransferencia(uuid.NewString(), 1000, "k-"+uuid.NewString()))

	assertErrorEnvelope(t, rec, http.StatusNotFound, "RecipientNotFound")
}

// HU-NF-06 Criterio 5: enviar al propio usuario se rechaza.
func TestHU_NF_06_Criterio5_EnvioAUnMismoSeRechaza(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := servicioConResolutor(pool)
	emisor := sembrarUsuarioConSaldo(t, pool, servicio, 1000000, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{emisor}})

	rec := pedirTransferenciaAutenticada(t, r, cuerpoTransferencia(emisor, 1000, "k-"+uuid.NewString()))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "SelfTransferNotAllowed")
}

// HU-NF-06 Criterios 7 y 9: el envío feliz responde 201 y el saldo del receptor sube, comprobado por HTTP.
func TestHU_NF_06_Criterio7_EnvioFelizSubeElSaldoDelReceptor(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := servicioConResolutor(pool)
	emisor := sembrarUsuarioConSaldo(t, pool, servicio, 1000000, 0)
	receptor := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{emisor}})

	rec := pedirTransferenciaAutenticada(t, r, cuerpoTransferencia(receptor, 250000, "k-"+uuid.NewString()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quiero 201 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente en %q", rec.Body.String())
	}
	if data["estado"] != nucleo.EstadoTransferenciaConfirmada {
		t.Errorf("estado = %v, quiero %q", data["estado"], nucleo.EstadoTransferenciaConfirmada)
	}
	if id, _ := data["id"].(string); id == "" {
		t.Errorf("id = %v, quiero un identificador único", data["id"])
	} else if _, err := uuid.Parse(id); err != nil {
		t.Errorf("id = %q, no es UUID: %v", id, err)
	}

	rReceptor := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{receptor}})
	recSaldo := pedirSaldo(t, rReceptor, "")
	if recSaldo.Code != http.StatusOK {
		t.Fatalf("saldo del receptor: status = %d (cuerpo %q)", recSaldo.Code, recSaldo.Body.String())
	}
	if got := datosDeSaldo(t, recSaldo)["monto"]; got != "250000" {
		t.Errorf("saldo del receptor = %v, quiero %q", got, "250000")
	}
}
