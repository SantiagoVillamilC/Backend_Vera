//go:build integration

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/denylist"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sembrarEmpresaDeIntegracion inserta la empresa representada y el rol del representante.
func sembrarEmpresaDeIntegracion(t *testing.T, pool *pgxpool.Pool, representante uuid.UUID) (uuid.UUID, string, string) {
	t.Helper()
	empresa := uuid.New()
	razonSocial := "empresa-" + empresa.String()
	nit := "nit-" + empresa.String()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE rol_auth"); err != nil {
		t.Fatalf("SET LOCAL ROLE: %v", err)
	}
	empresaPg := pgtype.UUID{Bytes: empresa, Valid: true}
	if _, err := tx.Exec(t.Context(), "INSERT INTO auth.partes (id, tipo) VALUES ($1, 'empresa')", empresaPg); err != nil {
		t.Fatalf("insertar la parte empresa: %v", err)
	}
	if _, err := tx.Exec(t.Context(), "INSERT INTO auth.empresas (parte_id, nit, razon_social) VALUES ($1, $2, $3)", empresaPg, nit, razonSocial); err != nil {
		t.Fatalf("insertar la empresa: %v", err)
	}
	if _, err := tx.Exec(t.Context(), "INSERT INTO auth.roles_de_parte (parte_id, rol, representada_id) VALUES ($1, 'representante_legal', $2)",
		pgtype.UUID{Bytes: representante, Valid: true}, empresaPg); err != nil {
		t.Fatalf("insertar el rol: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return empresa, razonSocial, nit
}

// firmarAccessDeIntegracion firma un access para una familia y contexto.
func firmarAccessDeIntegracion(t *testing.T, tokens *auth.TokensDeAcceso, parteID, familia uuid.UUID, contexto, rol string) string {
	t.Helper()
	access, err := tokens.Firmar(auth.ClaimsAccess{ParteID: parteID, SesionID: familia, Contexto: contexto, Rol: rol})
	if err != nil {
		t.Fatalf("firmar access: %v", err)
	}
	return access
}

// pedirDeDispositivos lanza una petición autenticada con el bearer del access.
func pedirDeDispositivos(r http.Handler, metodo, ruta, access, cuerpo string) *httptest.ResponseRecorder {
	var req *http.Request
	if cuerpo == "" {
		req = httptest.NewRequest(metodo, ruta, nil)
	} else {
		req = httptest.NewRequest(metodo, ruta, strings.NewReader(cuerpo))
	}
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestEndpointsDeDispositivosYContextoDePuntaAPunta(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	parteID, err := uuid.Parse(auth.SembrarParteDePruebaConID(t, pool, uuid.NewString()))
	if err != nil {
		t.Fatalf("uuid de la parte: %v", err)
	}
	empresa, razonSocial, nit := sembrarEmpresaDeIntegracion(t, pool, parteID)

	tokens := tokensDeIntegracion(t)
	d, err := denylist.NuevoRedis(clienteRedisDeIntegracion(t))
	if err != nil {
		t.Fatalf("denylist: %v", err)
	}
	gestor := auth.NuevoGestorSesiones(pool, auth.NuevoRegistradorEventos(pool))
	servicio, err := auth.NuevoServicioDeSesion(pool, gestor, tokens, d)
	if err != nil {
		t.Fatalf("servicio de sesión: %v", err)
	}
	refresh1, sesion1, err := gestor.EmitirSesion(t.Context(), parteID, auth.ContextoUsuario, "primera")
	if err != nil {
		t.Fatalf("emitir la primera sesión: %v", err)
	}
	refresh2, sesion2, err := gestor.EmitirSesion(t.Context(), parteID, auth.ContextoUsuario, "segunda")
	if err != nil {
		t.Fatalf("emitir la segunda sesión: %v", err)
	}
	access2 := firmarAccessDeIntegracion(t, tokens, parteID, sesion2.FamiliaID, auth.ContextoUsuario, "")

	r := NewRouter(pool, nucleo.NewServicio(pool, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: adaptadorJWTDePrueba{tokens: tokens, denylist: d},
		Auth:      NuevoDispositivosHandler(servicio),
		Refresh:   NuevoSesionHandler(servicio),
	})

	if rec := pedirDeDispositivos(r, http.MethodGet, "/v1/auth/sessions", access2, ""); rec.Code != http.StatusOK {
		t.Fatalf("GET sessions = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if rec := pedirDeDispositivos(r, http.MethodDelete, "/v1/auth/sessions/"+sesion1.FamiliaID.String(), access2, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE sessions = %d, quiero 204 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	rec := pedirDeDispositivos(r, http.MethodGet, "/v1/auth/sessions", access2, "")
	var lista struct {
		Data []api.Dispositivo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lista); err != nil {
		t.Fatalf("respuesta no JSON: %v", err)
	}
	if len(lista.Data) != 1 || uuid.UUID(lista.Data[0].Id) != sesion2.FamiliaID {
		t.Fatalf("sesiones tras el DELETE = %+v, quiero solo la segunda %s", lista.Data, sesion2.FamiliaID)
	}
	if rec := pedirDeDispositivos(r, http.MethodPost, "/v1/auth/refresh", "", `{"refresh_token":"`+refresh1+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh de la sesión revocada = %d, quiero 401 (cuerpo %q)", rec.Code, rec.Body.String())
	}

	rec = pedirDeDispositivos(r, http.MethodGet, "/v1/auth/contextos", access2, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET contextos = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	var contextos struct {
		Data []api.Contexto `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &contextos); err != nil {
		t.Fatalf("respuesta no JSON: %v", err)
	}
	if len(contextos.Data) != 2 {
		t.Fatalf("contextos = %+v, quiero personal y empresa", contextos.Data)
	}
	empresaID := auth.ContextoDeEmpresa(empresa)
	if contextos.Data[1].ContextoId != empresaID || contextos.Data[1].Nombre != razonSocial || contextos.Data[1].Nit == nil || *contextos.Data[1].Nit != nit {
		t.Fatalf("contexto de empresa = %+v, quiero %s con nit", contextos.Data[1], empresa)
	}

	rec = pedirDeDispositivos(r, http.MethodPost, "/v1/auth/contexto", access2, `{"contexto_id":"`+empresaID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST contexto = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	var activo struct {
		Data api.ContextoActivo `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &activo); err != nil {
		t.Fatalf("respuesta no JSON: %v", err)
	}
	claims, err := tokens.Verificar(activo.Data.AccessToken)
	if err != nil {
		t.Fatalf("Verificar access nuevo: %v", err)
	}
	if claims.Contexto != empresaID || claims.Rol != "representante_legal" {
		t.Fatalf("claims del access nuevo = %+v, quiero la empresa con rol", claims)
	}
	reqSonda := httptest.NewRequest(http.MethodGet, "/v1/saldo", nil)
	reqSonda.Header.Set("Authorization", "Bearer "+activo.Data.AccessToken)
	sonda, err := (adaptadorJWTDePrueba{tokens: tokens, denylist: d}).Identidad(reqSonda)
	if err != nil {
		t.Fatalf("adaptador sobre el access nuevo: %v", err)
	}
	if sonda.UsuarioID != empresa.String() || sonda.Contexto != auth.ContextoEmpresa || sonda.Rol != "representante_legal" {
		t.Fatalf("el núcleo recibió %+v, quiero el uuid de la empresa %s", sonda, empresa)
	}

	if rec := pedirDeDispositivos(r, http.MethodPost, "/v1/auth/logout-all", access2, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("POST logout-all = %d, quiero 204 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if rec := pedirDeDispositivos(r, http.MethodPost, "/v1/auth/refresh", "", `{"refresh_token":"`+refresh2+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh tras logout-all = %d, quiero 401 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if rec := pedirDeDispositivos(r, http.MethodGet, "/v1/auth/sessions", access2, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET sessions tras logout-all = %d, quiero 401 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}

func TestLogoutConRefreshRevocaLaFamiliaDePuntaAPunta(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	parteID, err := uuid.Parse(auth.SembrarParteDePruebaConID(t, pool, uuid.NewString()))
	if err != nil {
		t.Fatalf("uuid de la parte: %v", err)
	}

	tokens := tokensDeIntegracion(t)
	d, err := denylist.NuevoRedis(clienteRedisDeIntegracion(t))
	if err != nil {
		t.Fatalf("denylist: %v", err)
	}
	gestor := auth.NuevoGestorSesiones(pool, auth.NuevoRegistradorEventos(pool))
	servicio, err := auth.NuevoServicioDeSesion(pool, gestor, tokens, d)
	if err != nil {
		t.Fatalf("servicio de sesión: %v", err)
	}
	refresh1, sesion1, err := gestor.EmitirSesion(t.Context(), parteID, auth.ContextoUsuario, "primera")
	if err != nil {
		t.Fatalf("emitir la primera sesión: %v", err)
	}
	refresh2, sesion2, err := gestor.EmitirSesion(t.Context(), parteID, auth.ContextoUsuario, "segunda")
	if err != nil {
		t.Fatalf("emitir la segunda sesión: %v", err)
	}
	access1 := firmarAccessDeIntegracion(t, tokens, parteID, sesion1.FamiliaID, auth.ContextoUsuario, "")
	access2 := firmarAccessDeIntegracion(t, tokens, parteID, sesion2.FamiliaID, auth.ContextoUsuario, "")

	r := NewRouter(pool, nucleo.NewServicio(pool, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: adaptadorJWTDePrueba{tokens: tokens, denylist: d},
		Auth:      NuevoDispositivosHandler(servicio),
		Refresh:   NuevoSesionHandler(servicio),
	})

	rec := pedirDeDispositivos(r, http.MethodPost, "/v1/auth/logout", access2, `{"refresh_token":"`+refresh1+`"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST logout = %d, quiero 204 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if rec := pedirDeDispositivos(r, http.MethodPost, "/v1/auth/refresh", "", `{"refresh_token":"`+refresh1+`"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh de la familia cerrada = %d, quiero 401 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if rec := pedirDeDispositivos(r, http.MethodGet, "/v1/auth/sessions", access1, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("access de la familia cerrada = %d, quiero 401 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if rec := pedirDeDispositivos(r, http.MethodPost, "/v1/auth/refresh", "", `{"refresh_token":"`+refresh2+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("refresh de la otra familia = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}
