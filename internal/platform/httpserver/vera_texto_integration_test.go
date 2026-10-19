//go:build integration

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestVeraTextoIntegracion(t *testing.T) {
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	app, err := pgxpool.New(ctx, os.Getenv("APP_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	parte := uuid.New()
	if _, err := admin.Exec(ctx, "INSERT INTO auth.partes(id,tipo) VALUES($1,'persona')", parte); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DELETE FROM asistente.solicitud_texto WHERE usuario_id=$1", parte)
		_, _ = admin.Exec(context.Background(), "DELETE FROM asistente.conversacion WHERE usuario_id=$1", parte)
		_, _ = admin.Exec(context.Background(), "DELETE FROM auth.consentimientos WHERE parte_id=$1", parte)
		_, _ = admin.Exec(context.Background(), "DELETE FROM auth.partes WHERE id=$1", parte)
	})
	opts, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(opts)
	defer func() { _ = redisClient.Close() }()
	contador, err := contadores.NuevoRedis(redisClient)
	if err != nil {
		t.Fatal(err)
	}
	claves, err := auth.NuevasClaves([]byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	presupuesto, err := copilot.NuevoPresupuestoRedis(redisClient)
	if err != nil {
		t.Fatal(err)
	}
	uso, err := copilot.NuevoControlUso(contador, claves, presupuesto, copilot.LimitesUso{TextoHora: 20, TextoDia: 40, VozHora: 10, TokensDiarios: 100000, ReservaTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	consent := auth.NuevoServicioDeConsentimientos(app)
	control := copilot.NuevoControlConsentimiento(consent, copilot.NuevoCanceladorConsentimiento(app))
	proveedor := control.ProtegerProveedor(uso.ProtegerProveedor(provsim.Nuevo(
		provsim.Paso{Error: copilot.ErrNoDisponibleIA},
		provsim.Paso{Respuesta: copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "Respuesta de integración."}}}}},
	)))
	catalogo, err := copilot.NuevoCatalogo(nil, nil, copilot.ConAuditoriaLlamadas(copilot.NuevaAuditoriaPostgres(app)))
	if err != nil {
		t.Fatal(err)
	}
	orquestador, err := copilot.NuevoOrquestador(proveedor, catalogo, copilot.NuevaMemoriaCicloPostgres(app), control, "simulado", nil)
	if err != nil {
		t.Fatal(err)
	}
	id := identidad.Identidad{ParteID: parte.String(), UsuarioID: uuid.NewString(), Contexto: auth.ContextoUsuario}
	router := NewRouterConVera(app, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: id}, Consent: NuevoConsentimientosHandler(consent)}, DependenciasVera{Consentimiento: control, Uso: uso, Texto: copilot.NuevoServicioTexto(app, orquestador)})
	clave := uuid.NewString()
	request := func(path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Idempotency-Key", clave)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		return w
	}
	request("/v1/vera/mensajes", `{"texto":"Hola"}`, 403)
	request("/v1/auth/consents", `{"tipo":"asistente_ia","version_texto":"v1","aceptado":true,"accion":"otorgar","metodo":"app"}`, 201)
	w := request("/v1/vera/mensajes", `{"texto":"Hola"}`, 503)
	conv, user := w.Header().Get("X-Conversacion-Id"), w.Header().Get("X-Mensaje-Usuario-Id")
	if conv == "" || user == "" {
		t.Fatal("no dejó identificadores para recuperar el envío")
	}
	w = request("/v1/vera/mensajes", `{"texto":"Hola"}`, 200)
	var result struct {
		Data api.RespuestaAsistente `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Data.ConversacionId.String() != conv || result.Data.Texto != "Respuesta de integración." {
		t.Fatal(w.Body)
	}
	primera := w.Body.String()
	w = request("/v1/vera/mensajes", `{"texto":"Hola"}`, 200)
	if w.Body.String() != primera {
		t.Fatal("replay diferente")
	}
	request("/v1/vera/mensajes", `{"texto":"Otro"}`, 409)
	var total int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM asistente.mensaje WHERE conversacion_id=$1", conv).Scan(&total); err != nil || total != 2 {
		t.Fatalf("mensajes=%d %v", total, err)
	}
}
