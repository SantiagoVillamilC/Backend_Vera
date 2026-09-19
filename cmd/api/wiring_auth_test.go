package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/httpserver"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/otpsim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/whatsapp"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestCanalOTPEligenPorOTPCanal(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		cfg       ConfigAuth
		wantErr   string
		comprobar func(t *testing.T, canal auth.CanalOTP)
	}{
		"simulador": {
			cfg: ConfigAuth{OTPCanal: "simulador", OTPSimuladorCodigoFijo: "123456"},
			comprobar: func(t *testing.T, canal auth.CanalOTP) {
				t.Helper()
				sim, ok := canal.(*otpsim.Simulador)
				if !ok {
					t.Fatalf("canal = %T, quiero *otpsim.Simulador", canal)
				}
				if codigo, hay := sim.CodigoFijo(); !hay || codigo != "123456" {
					t.Errorf("CodigoFijo = (%q, %v), quiero (\"123456\", true)", codigo, hay)
				}
			},
		},
		"cloudapi": {
			cfg: ConfigAuth{OTPCanal: "cloudapi", WhatsAppToken: "tok", WhatsAppPhoneID: "123", WhatsAppTemplate: "otp_login"},
			comprobar: func(t *testing.T, canal auth.CanalOTP) {
				t.Helper()
				if _, ok := canal.(*whatsapp.Cliente); !ok {
					t.Fatalf("canal = %T, quiero *whatsapp.Cliente", canal)
				}
			},
		},
		"demo":        {cfg: ConfigAuth{OTPCanal: "demo"}, wantErr: "demo"},
		"desconocido": {cfg: ConfigAuth{OTPCanal: "otro"}, wantErr: "OTP_CANAL desconocido"},
	}

	for name, tc := range casos {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			canal, err := canalOTP(&Config{Auth: tc.cfg})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("canalOTP error = %v, quiero que contenga %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("canalOTP: %v", err)
			}
			if canal == nil {
				t.Fatal("canalOTP devolvió un canal nil sin error")
			}
			if tc.comprobar != nil {
				tc.comprobar(t, canal)
			}
		})
	}
}

func TestContadoresDeAuthRechazaClienteNil(t *testing.T) {
	t.Parallel()

	if _, err := contadoresDeAuth(nil); err == nil {
		t.Fatal("contadoresDeAuth(nil) devolvió nil, quiero error")
	}
}

func TestCanalOTPRechazaElCodigoFijoInvalido(t *testing.T) {
	t.Parallel()

	_, err := canalOTP(&Config{Auth: ConfigAuth{OTPCanal: "simulador", OTPSimuladorCodigoFijo: "abc"}})
	if err == nil {
		t.Fatal("canalOTP con un código fijo inválido devolvió nil, quiero error")
	}
}

// denylistFalsa es la denylist de los tests unitarios, con revocados y fallo de Redis controlados.
type denylistFalsa struct {
	revocados map[uuid.UUID]bool
	falla     bool
}

// nuevaDenylistFalsa arranca la denylist de prueba sin revocados.
func nuevaDenylistFalsa() *denylistFalsa { return &denylistFalsa{revocados: map[uuid.UUID]bool{}} }

// Revocar marca el sid o simula un Redis caído.
func (d *denylistFalsa) Revocar(context.Context, uuid.UUID, time.Duration) error {
	if d.falla {
		return auth.ConFailClosed(errors.New("redis caído"))
	}
	return nil
}

// Revocado responde el estado del sid o simula un Redis caído.
func (d *denylistFalsa) Revocado(_ context.Context, sid uuid.UUID) (bool, error) {
	if d.falla {
		return false, auth.ConFailClosed(errors.New("redis caído"))
	}
	return d.revocados[sid], nil
}

var _ auth.RevocadorDeSesiones = (*denylistFalsa)(nil)

// configDeTokens arma la configuración mínima del firmador para los tests.
func configDeTokens() *Config {
	return &Config{
		JWTSecret: strings.Repeat("k", 32),
		Auth:      ConfigAuth{JWTIssuer: "centinela-auth", JWTAudience: "centinela-api"},
	}
}

// firmarDePrueba firma un access con la configuración de tokens.
func firmarDePrueba(t *testing.T, cfg *Config, claims auth.ClaimsAccess) string {
	t.Helper()
	tokens, err := tokensDeAcceso(cfg)
	if err != nil {
		t.Fatalf("tokensDeAcceso: %v", err)
	}
	access, err := tokens.Firmar(claims)
	if err != nil {
		t.Fatalf("Firmar: %v", err)
	}
	return access
}

// codigoDeError decodifica el code del envelope de una respuesta de error.
func codigoDeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("respuesta no JSON: %v (%q)", err, rec.Body.String())
	}
	if env.Error == nil {
		t.Fatalf("error ausente en %q", rec.Body.String())
	}
	return env.Error.Code
}

func TestIdentidadJWTTraduceElContextoYLaDenylist(t *testing.T) {
	cfg := configDeTokens()
	parte, sesion, empresa := uuid.New(), uuid.New(), uuid.New()

	casos := map[string]struct {
		claims    auth.ClaimsAccess
		revocados map[uuid.UUID]bool
		wantUser  string
		wantCtx   string
		wantRol   string
		wantErr   error
	}{
		"usuario": {
			claims:   auth.ClaimsAccess{ParteID: parte, SesionID: sesion, Contexto: auth.ContextoUsuario},
			wantUser: parte.String(), wantCtx: auth.ContextoUsuario,
		},
		"empresa": {
			claims:   auth.ClaimsAccess{ParteID: parte, SesionID: sesion, Contexto: auth.ContextoDeEmpresa(empresa), Rol: "representante_legal"},
			wantUser: empresa.String(), wantCtx: auth.ContextoEmpresa, wantRol: "representante_legal",
		},
		"sid revocado": {
			claims:    auth.ClaimsAccess{ParteID: parte, SesionID: sesion, Contexto: auth.ContextoUsuario},
			revocados: map[uuid.UUID]bool{sesion: true},
			wantErr:   auth.ErrSesionRevocada,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			den := nuevaDenylistFalsa()
			den.revocados = caso.revocados
			adaptador, err := identidadJWT(cfg, den)
			if err != nil {
				t.Fatalf("identidadJWT: %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "/saldo", nil)
			req.Header.Set("Authorization", "Bearer "+firmarDePrueba(t, cfg, caso.claims))

			id, err := adaptador.Identidad(req)
			if caso.wantErr != nil {
				if !errors.Is(err, caso.wantErr) {
					t.Fatalf("Identidad = %v, quiero %v", err, caso.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Identidad: %v", err)
			}
			if id.UsuarioID != caso.wantUser || id.Contexto != caso.wantCtx || id.Rol != caso.wantRol {
				t.Fatalf("identidad = %+v, quiero usuario %s, ctx %s, rol %q", id, caso.wantUser, caso.wantCtx, caso.wantRol)
			}
			if id.SesionID != sesion.String() {
				t.Errorf("sesion_id = %s, quiero %s", id.SesionID, sesion)
			}
		})
	}
}

func TestIdentidadJWTRechazaBearerAusenteOMalFormado(t *testing.T) {
	cfg := configDeTokens()
	adaptador, err := identidadJWT(cfg, nuevaDenylistFalsa())
	if err != nil {
		t.Fatalf("identidadJWT: %v", err)
	}

	encabezados := map[string]string{
		"ausente":      "",
		"sin esquema":  "cualquier-token",
		"esquema raro": "Basic abc",
		"bearer vacío": "Bearer   ",
	}
	for nombre, encabezado := range encabezados {
		t.Run(nombre, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/saldo", nil)
			if encabezado != "" {
				req.Header.Set("Authorization", encabezado)
			}
			if _, err := adaptador.Identidad(req); !errors.Is(err, auth.ErrSinIdentidad) {
				t.Fatalf("Identidad = %v, quiero ErrSinIdentidad", err)
			}
		})
	}
}

// responderOk es el handler autenticado de sonda para probar RequireAuth.
func responderOk(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

func TestRequireAuthCierraLasRutasDeDineroCuandoLaDenylistFalla(t *testing.T) {
	cfg := configDeTokens()
	sesion := uuid.New()
	access := firmarDePrueba(t, cfg, auth.ClaimsAccess{ParteID: uuid.New(), SesionID: sesion, Contexto: auth.ContextoUsuario})

	casos := map[string]struct {
		falla        bool
		revocado     bool
		statusSaldo  int
		statusDinero int
		codeDinero   string
	}{
		"denylist caida": {falla: true, statusSaldo: http.StatusOK, statusDinero: http.StatusServiceUnavailable, codeDinero: "AuthUnavailable"},
		"sid revocado":   {revocado: true, statusSaldo: http.StatusUnauthorized, statusDinero: http.StatusUnauthorized, codeDinero: "Unauthorized"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			den := nuevaDenylistFalsa()
			den.falla = caso.falla
			den.revocados[sesion] = caso.revocado
			adaptador, err := identidadJWT(cfg, den)
			if err != nil {
				t.Fatalf("identidadJWT: %v", err)
			}
			r := chi.NewRouter()
			r.Use(httpserver.RequireAuth(adaptador, httpserver.RutasDeDinero))
			r.Get("/saldo", responderOk)
			r.Post("/transferencias", responderOk)
			r.Post("/depositos", responderOk)

			for _, ruta := range []struct {
				metodo string
				path   string
				want   int
				code   string
			}{
				{http.MethodPost, "/transferencias", caso.statusDinero, caso.codeDinero},
				{http.MethodPost, "/depositos", caso.statusDinero, caso.codeDinero},
				{http.MethodGet, "/saldo", caso.statusSaldo, ""},
			} {
				req := httptest.NewRequest(ruta.metodo, ruta.path, nil)
				req.Header.Set("Authorization", "Bearer "+access)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				if rec.Code != ruta.want {
					t.Fatalf("%s %s = %d, quiero %d (cuerpo %q)", ruta.metodo, ruta.path, rec.Code, ruta.want, rec.Body.String())
				}
				if ruta.code != "" && codigoDeError(t, rec) != ruta.code {
					t.Fatalf("%s %s code = %s, quiero %s", ruta.metodo, ruta.path, codigoDeError(t, rec), ruta.code)
				}
			}
		})
	}
}

func TestRequireAuthRechazaAccessVencidoAlteradoOAjeno(t *testing.T) {
	cfg := configDeTokens()
	parte, sesion := uuid.New(), uuid.New()

	valido := firmarDePrueba(t, cfg, auth.ClaimsAccess{ParteID: parte, SesionID: sesion, Contexto: auth.ContextoUsuario})
	vencido := firmarDePrueba(t, cfg, auth.ClaimsAccess{
		ParteID:   parte,
		SesionID:  sesion,
		Contexto:  auth.ContextoUsuario,
		EmitidoEn: time.Now().Add(-2 * time.Hour),
		ExpiraEn:  time.Now().Add(-time.Hour),
	})
	partes := strings.Split(valido, ".")
	cambiado := partes[1]
	if cambiado[0] == 'A' {
		cambiado = "B" + cambiado[1:]
	} else {
		cambiado = "A" + cambiado[1:]
	}
	alterado := strings.Join([]string{partes[0], cambiado, partes[2]}, ".")
	otraClave, err := auth.NuevoTokensDeAcceso([]byte(strings.Repeat("o", 32)), "centinela-auth", "centinela-api")
	if err != nil {
		t.Fatalf("NuevoTokensDeAcceso: %v", err)
	}
	ajeno, err := otraClave.Firmar(auth.ClaimsAccess{ParteID: parte, SesionID: sesion, Contexto: auth.ContextoUsuario})
	if err != nil {
		t.Fatalf("Firmar ajeno: %v", err)
	}
	codificar := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	algNone := codificar(`{"alg":"none","typ":"JWT"}`) + "." + codificar(`{"sub":"`+parte.String()+`"}`) + "."

	for nombre, token := range map[string]string{
		"vencido":    vencido,
		"alterado":   alterado,
		"otra clave": ajeno,
		"alg none":   algNone,
	} {
		t.Run(nombre, func(t *testing.T) {
			adaptador, err := identidadJWT(cfg, nuevaDenylistFalsa())
			if err != nil {
				t.Fatalf("identidadJWT: %v", err)
			}
			r := chi.NewRouter()
			r.Use(httpserver.RequireAuth(adaptador, httpserver.RutasDeDinero))
			r.Get("/saldo", responderOk)

			req := httptest.NewRequest(http.MethodGet, "/saldo", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, quiero 401 (cuerpo %q)", rec.Code, rec.Body.String())
			}
			if code := codigoDeError(t, rec); code != "Unauthorized" {
				t.Fatalf("code = %s, quiero Unauthorized", code)
			}
		})
	}
}
