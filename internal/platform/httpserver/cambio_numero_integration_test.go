//go:build integration

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/denylist"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/otpsim"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pepperDeCambioHTTP es el OTP_PEPPER con el que la prueba monta los servicios.
var pepperDeCambioHTTP = bytes.Repeat([]byte{0x22}, 32)

// contadorCelularHTTP reparte números únicos entre pruebas para no chocar con la unicidad.
var contadorCelularHTTP atomic.Int64

// celularHTTPDePrueba devuelve un E.164 único dentro del rango de prueba.
func celularHTTPDePrueba() string {
	n := contadorCelularHTTP.Add(1) + time.Now().UnixNano()%1000000
	return fmt.Sprintf("+57300%07d", n%10000000)
}

// sembrarCredencialHTTP inserta parte y credencial activa con el celular verificado y devuelve su id.
func sembrarCredencialHTTP(t *testing.T, pool *pgxpool.Pool, derivador auth.Derivador, correo, celular, contrasena string) uuid.UUID {
	t.Helper()

	phc, err := derivador.Derivar(context.Background(), contrasena)
	if err != nil {
		t.Fatalf("derivar la contraseña: %v", err)
	}
	credencialID, parteID := uuid.New(), uuid.New()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("abrir transacción: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE rol_auth"); err != nil {
		t.Fatalf("bajar a rol_auth: %v", err)
	}
	if _, err := tx.Exec(t.Context(), "INSERT INTO auth.partes (id, tipo) VALUES ($1, 'persona')", parteID); err != nil {
		t.Fatalf("sembrar la parte: %v", err)
	}
	if _, err := tx.Exec(t.Context(),
		`INSERT INTO auth.credenciales (id, parte_id, correo, celular, celular_verificado_en, valor_derivado, estado)
		 VALUES ($1, $2, $3, $4, now(), $5, 'activo')`,
		credencialID, parteID, correo, celular, phc); err != nil {
		t.Fatalf("sembrar la credencial: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("confirmar el sembrado: %v", err)
	}
	return credencialID
}

// peticionAutenticada arma un POST con el access y el cuerpo dados.
func peticionAutenticada(t *testing.T, access, ruta string, cuerpo any) *http.Request {
	t.Helper()

	datos, err := json.Marshal(cuerpo)
	if err != nil {
		t.Fatalf("serializar el cuerpo: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, ruta, bytes.NewReader(datos))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	return req
}

func TestCambioDeNumeroPorHTTPDejaElAccessViejoEn401(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	cliente := clienteRedisDeIntegracion(t)
	tokens := tokensDeIntegracion(t)
	denylistRedis, err := denylist.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("denylist: %v", err)
	}
	contadoresRedis, err := contadores.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("contadores: %v", err)
	}
	claves, err := auth.NuevasClaves([]byte("1111111111111111111111111111111111111111111111111111111111111111"))
	if err != nil {
		t.Fatalf("claves: %v", err)
	}
	derivador, err := auth.NuevoDerivadorArgon2(auth.ParametrosArgon2DePrueba())
	if err != nil {
		t.Fatalf("derivador: %v", err)
	}
	eventos := auth.NuevoRegistradorEventos(pool)
	canal, err := otpsim.Nuevo("123456")
	if err != nil {
		t.Fatalf("canal simulado: %v", err)
	}
	gestor, err := auth.NuevoGestorDesafios(pool, canal, eventos, auth.SiempreOptIn{}, pepperDeCambioHTTP,
		auth.ConRacionamiento(contadoresRedis, claves))
	if err != nil {
		t.Fatalf("gestor de desafíos: %v", err)
	}
	verificador, err := auth.NuevoVerificadorDeCredencialesPostgres(pool, derivador, contadoresRedis, claves, eventos, auth.ParametrosArgon2DePrueba())
	if err != nil {
		t.Fatalf("verificador: %v", err)
	}
	sesiones := auth.NuevoGestorSesiones(pool, eventos)
	login, err := auth.NuevoServicioDeLogin(verificador, pool, sesiones, tokens)
	if err != nil {
		t.Fatalf("servicio de login: %v", err)
	}
	cambio, err := auth.NuevoServicioDeCambioDeNumero(pool, gestor, sesiones, derivador, denylistRedis, eventos, contadoresRedis, claves, pepperDeCambioHTTP, 24*time.Hour)
	if err != nil {
		t.Fatalf("servicio de cambio: %v", err)
	}

	correo := "cambio-http-" + uuid.NewString() + "@centinela.test"
	celular := celularHTTPDePrueba()
	sembrarCredencialHTTP(t, pool, derivador, correo, celular, "contrasena-correcta")
	acceso := iniciarSesionHTTP(t, login, celular)
	nuevo := celularHTTPDePrueba()

	r := NewRouter(pool, nucleo.NewServicio(pool, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad:   adaptadorJWTDePrueba{tokens: tokens, denylist: denylistRedis},
		PhoneChange: NuevoCambioDeNumeroHandler(cambio),
	})

	solicitud := httptest.NewRecorder()
	r.ServeHTTP(solicitud, peticionAutenticada(t, acceso, "/v1/auth/phone/change/request", map[string]any{
		"nuevo_celular": nuevo, "contrasena_actual": "contrasena-correcta",
	}))
	if solicitud.Code != http.StatusOK {
		t.Fatalf("request = %d, quiero 200 (cuerpo %q)", solicitud.Code, solicitud.Body.String())
	}
	env := decodeEnvelope(t, solicitud)
	datos, _ := env["data"].(map[string]any)
	if datos["proposito"] != "cambio_numero_viejo" {
		t.Fatalf("proposito = %v, quiero cambio_numero_viejo", datos["proposito"])
	}
	challengeID, ok := datos["challenge_id"].(string)
	if !ok {
		t.Fatalf("challenge_id ausente en %q", solicitud.Body.String())
	}

	for intento := 1; intento <= 2; intento++ {
		confirm := httptest.NewRecorder()
		r.ServeHTTP(confirm, peticionAutenticada(t, acceso, "/v1/auth/phone/change/confirm", map[string]any{
			"challenge_id": challengeID, "codigo": "123456",
		}))
		if confirm.Code != http.StatusOK {
			t.Fatalf("confirm %d = %d, quiero 200 (cuerpo %q)", intento, confirm.Code, confirm.Body.String())
		}
	}

	viejo := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/saldo", nil)
	req.Header.Set("Authorization", "Bearer "+acceso)
	r.ServeHTTP(viejo, req)
	assertErrorEnvelope(t, viejo, http.StatusUnauthorized, "Unauthorized")
}

// iniciarSesionHTTP corre el login directo por celular y devuelve el access emitido.
func iniciarSesionHTTP(t *testing.T, login *auth.ServicioDeLogin, celular string) string {
	t.Helper()

	emision, err := login.IniciarSesion(t.Context(), celular, "contrasena-correcta", auth.ContextoUsuario, "10.0.0.1", "dispositivo-de-prueba")
	if err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	return emision.AccessToken
}
