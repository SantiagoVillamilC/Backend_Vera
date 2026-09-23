//go:build integration

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/denylist"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// sondaOk es el handler autenticado de sonda de las pruebas de RequireAuth.
func sondaOk(w http.ResponseWriter, _ *http.Request) {
	api.WriteData(w, http.StatusOK, map[string]string{"ok": "1"}, nil)
}

// adaptadorJWTDePrueba valida el access y consulta la denylist, calcando el adaptador de main.
type adaptadorJWTDePrueba struct {
	tokens   *auth.TokensDeAcceso
	denylist auth.RevocadorDeSesiones
}

// Identidad traduce el bearer a la identidad de dominio y cierra si el sid está revocado.
func (a adaptadorJWTDePrueba) Identidad(r *http.Request) (identidad.Identidad, error) {
	claims, err := a.tokens.Verificar(strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
	if err != nil {
		return identidad.Identidad{}, err
	}
	id := identidad.Identidad{
		UsuarioID: claims.ParteID.String(),
		SesionID:  claims.SesionID.String(),
		ParteID:   claims.ParteID.String(),
		Contexto:  auth.ContextoUsuario,
		Rol:       claims.Rol,
	}
	if empresaID, esEmpresa := auth.EmpresaDeContexto(claims.Contexto); esEmpresa {
		id.UsuarioID = empresaID.String()
		id.Contexto = auth.ContextoEmpresa
	}
	revocado, err := a.denylist.Revocado(r.Context(), claims.SesionID)
	if err != nil {
		return id, auth.ConFailClosed(err)
	}
	if revocado {
		return id, auth.ErrSesionRevocada
	}
	return id, nil
}

var _ IdentidadDeLaPeticion = adaptadorJWTDePrueba{}

// clienteRedisDeIntegracion abre el cliente de la denylist contra el Redis del carril.
func clienteRedisDeIntegracion(t *testing.T) *redis.Client {
	t.Helper()
	raw := os.Getenv("REDIS_URL")
	if raw == "" {
		t.Fatalf("REDIS_URL es obligatoria bajo el tag integration")
	}
	cliente, err := platformredis.NuevoCliente(raw)
	if err != nil {
		t.Fatalf("cliente Redis: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	if err := cliente.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("ping al Redis: %v", err)
	}
	return cliente
}

// tokensDeIntegracion arma el firmador con el emisor y la audiencia del contrato.
func tokensDeIntegracion(t *testing.T) *auth.TokensDeAcceso {
	t.Helper()
	tokens, err := auth.NuevoTokensDeAcceso([]byte("clave-de-firma-de-al-menos-32-bytes"), "centinela-auth", "centinela-api")
	if err != nil {
		t.Fatalf("tokens: %v", err)
	}
	return tokens
}

func TestSaldoConAccessJWTYFamiliaRevocadaResponde401(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := auth.SembrarParteDePruebaConID(t, pool, uuid.NewString())
	if _, err := servicio.AbrirSubcuentasDeUsuario(t.Context(), usuarioID); err != nil {
		t.Fatalf("abrir subcuentas: %v", err)
	}
	parteID, err := uuid.Parse(usuarioID)
	if err != nil {
		t.Fatalf("uuid del usuario: %v", err)
	}

	tokens := tokensDeIntegracion(t)
	d, err := denylist.NuevoRedis(clienteRedisDeIntegracion(t))
	if err != nil {
		t.Fatalf("denylist: %v", err)
	}
	sesionID := uuid.New()
	access, err := tokens.Firmar(auth.ClaimsAccess{ParteID: parteID, SesionID: sesionID, Contexto: auth.ContextoUsuario})
	if err != nil {
		t.Fatalf("firmar access: %v", err)
	}
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: adaptadorJWTDePrueba{tokens: tokens, denylist: d}})

	pedir := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/saldo", nil)
		req.Header.Set("Authorization", "Bearer "+access)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := pedir(); rec.Code != http.StatusOK {
		t.Fatalf("GET /saldo con el access = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if err := d.Revocar(t.Context(), sesionID, 5*time.Minute); err != nil {
		t.Fatalf("revocar el sid: %v", err)
	}
	assertErrorEnvelope(t, pedir(), http.StatusUnauthorized, "Unauthorized")
}

func TestAccessDeEmpresaLlegaAlNucleoConElUuidDeLaEmpresa(t *testing.T) {
	tokens := tokensDeIntegracion(t)
	d, err := denylist.NuevoRedis(clienteRedisDeIntegracion(t))
	if err != nil {
		t.Fatalf("denylist: %v", err)
	}
	adaptador := adaptadorJWTDePrueba{tokens: tokens, denylist: d}
	empresa := uuid.New()
	access, err := tokens.Firmar(auth.ClaimsAccess{
		ParteID:  uuid.New(),
		SesionID: uuid.New(),
		Contexto: auth.ContextoDeEmpresa(empresa),
		Rol:      "representante_legal",
	})
	if err != nil {
		t.Fatalf("firmar access: %v", err)
	}

	r := chi.NewRouter()
	r.Use(RequireAuth(adaptador, RutasDeDinero))
	r.Get("/quien-soy", func(w http.ResponseWriter, req *http.Request) {
		id, ok := identidad.IdentidadDeContexto(req.Context())
		if !ok {
			api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Sin identidad.", nil)
			return
		}
		api.WriteData(w, http.StatusOK, map[string]string{"usuario_id": id.UsuarioID, "contexto": id.Contexto, "rol": id.Rol}, nil)
	})

	req := httptest.NewRequest(http.MethodGet, "/quien-soy", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	data, _ := env["data"].(map[string]any)
	if data["usuario_id"] != empresa.String() || data["contexto"] != "empresa" || data["rol"] != "representante_legal" {
		t.Fatalf("el núcleo recibió %v, quiero el uuid de la empresa %s con rol", data, empresa)
	}
}

func TestRequireAuthCierraLasRutasDeDineroConRedisCaidoYDejaLasLecturas(t *testing.T) {
	cliente, err := platformredis.NuevoCliente("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("cliente Redis: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	d, err := denylist.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("denylist: %v", err)
	}
	tokens := tokensDeIntegracion(t)
	adaptador := adaptadorJWTDePrueba{tokens: tokens, denylist: d}
	access, err := tokens.Firmar(auth.ClaimsAccess{ParteID: uuid.New(), SesionID: uuid.New(), Contexto: auth.ContextoUsuario})
	if err != nil {
		t.Fatalf("firmar access: %v", err)
	}

	r := chi.NewRouter()
	r.Use(RequireAuth(adaptador, RutasDeDinero))
	r.Get("/saldo", sondaOk)
	r.Post("/transferencias", sondaOk)
	r.Post("/depositos", sondaOk)

	for _, caso := range []struct {
		metodo string
		path   string
		want   int
	}{
		{http.MethodPost, "/transferencias", http.StatusServiceUnavailable},
		{http.MethodPost, "/depositos", http.StatusServiceUnavailable},
		{http.MethodGet, "/saldo", http.StatusOK},
	} {
		req := httptest.NewRequest(caso.metodo, caso.path, nil)
		req.Header.Set("Authorization", "Bearer "+access)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != caso.want {
			t.Fatalf("%s %s = %d, quiero %d (cuerpo %q)", caso.metodo, caso.path, rec.Code, caso.want, rec.Body.String())
		}
	}
}
