//go:build integration

package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type proveedorContadorConsentimiento struct{ llamadas int }

func (p *proveedorContadorConsentimiento) Generar(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	p.llamadas++
	return copilot.RespuestaIA{}, nil
}

func TestVeraConsentimientoIntegracion(t *testing.T) {
	ctx := t.Context()
	abrir := func(env string) *pgxpool.Pool {
		t.Helper()
		url := os.Getenv(env)
		if url == "" {
			t.Fatalf("falta %s", env)
		}
		p, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	admin, app := abrir("DATABASE_URL"), abrir("APP_DATABASE_URL")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	parte, ajena := uuid.New(), uuid.New()
	exec("INSERT INTO auth.partes(id,tipo) VALUES ($1,'persona'),($2,'persona')", parte, ajena)
	servicio := auth.NuevoServicioDeConsentimientos(app)
	control := copilot.NuevoControlConsentimiento(servicio, copilot.NuevoCanceladorConsentimiento(app))
	id := identidad.Identidad{UsuarioID: uuid.NewString(), ParteID: parte.String(), Contexto: auth.ContextoUsuario}
	router := NewRouterConVera(app, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: id}, Consent: NuevoConsentimientosHandler(servicio)}, DependenciasVera{Consentimiento: control, Uso: &usoVeraPrueba{}})
	request := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	estado := func(version string, autorizado bool) {
		t.Helper()
		w := request(http.MethodGet, "/v1/auth/consents?tipo=asistente_ia", "", 200)
		var body struct {
			Data struct {
				Version    string `json:"version_texto"`
				Texto      string `json:"texto"`
				Autorizado bool   `json:"autorizado"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Data.Version != version || body.Data.Autorizado != autorizado || body.Data.Texto == "" {
			t.Fatalf("estado inesperado: %s", w.Body)
		}
	}
	consentir := func(version string, aceptar bool, status int) {
		t.Helper()
		accion, valor := "revocar", "false"
		if aceptar {
			accion, valor = "otorgar", "true"
		}
		request(http.MethodPost, "/v1/auth/consents", `{"tipo":"asistente_ia","version_texto":"`+version+`","aceptado":`+valor+`,"accion":"`+accion+`","metodo":"app"}`, status)
	}
	proveedor := &proveedorContadorConsentimiento{}
	protegido := control.ProtegerProveedor(proveedor)
	ctxUsuario := identidad.ConIdentidad(ctx, id)
	denegado := func() {
		t.Helper()
		for _, path := range []string{"/v1/vera/mensajes", "/v1/vera/mensajes/voz"} {
			w := request(http.MethodPost, path, `{}`, 403)
			if !strings.Contains(w.Body.String(), "ConsentRequired") {
				t.Fatal(w.Body)
			}
		}
		if _, err := protegido.Generar(ctxUsuario, copilot.SolicitudIA{}); !errors.Is(err, copilot.ErrConsentimientoAsistente) {
			t.Fatal(err)
		}
	}
	estado("v1", false)
	denegado()
	consentir("v0", true, 422)
	consentir("", true, 422)
	consentir("v1", true, 201)
	estado("v1", true)
	if _, err := protegido.Generar(ctxUsuario, copilot.SolicitudIA{}); err != nil {
		t.Fatal(err)
	}
	request(http.MethodPost, "/v1/vera/mensajes", `{"texto":"Hola"}`, 503)
	crearAccion := func(titular uuid.UUID) uuid.UUID {
		t.Helper()
		var conversacion, accion uuid.UUID
		if err := admin.QueryRow(ctx, "INSERT INTO asistente.conversacion(usuario_id,usuario_financiero_id) VALUES ($1,$1::uuid::text) RETURNING id", titular).Scan(&conversacion); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueryRow(ctx, "INSERT INTO asistente.accion_pendiente(usuario_id,conversacion_id,tipo,destino,nombre_mostrado,monto,requiere_codigo,usuario_financiero_id) VALUES ($1,$2,'transferencia_interna','+573001234567','Laura R.',100,false,$1::uuid::text) RETURNING id", titular, conversacion).Scan(&accion); err != nil {
			t.Fatal(err)
		}
		return accion
	}
	propia, otra := crearAccion(parte), crearAccion(ajena)
	consentir("v1", false, 201)
	estado("v1", false)
	denegado()
	for _, accion := range []uuid.UUID{propia, otra} {
		request(http.MethodPost, "/v1/vera/acciones/"+accion.String()+"/confirmar", `{}`, 403)
	}
	for accion, esperado := range map[uuid.UUID]string{propia: "cancelada", otra: "pendiente"} {
		var actual string
		if err := admin.QueryRow(ctx, "SELECT estado FROM asistente.accion_pendiente WHERE id=$1", accion).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if actual != esperado {
			t.Fatalf("estado %s != %s", actual, esperado)
		}
	}
	historial, err := servicio.Listar(ctx, parte)
	if err != nil || len(historial) != 2 {
		t.Fatalf("historial: %v %v", historial, err)
	}
	if proveedor.llamadas != 1 {
		t.Fatalf("proveedor recibió %d llamadas", proveedor.llamadas)
	}
	consentir("v1", true, 201)
	// Una nueva versión invalida la aceptación anterior, sin borrar la evidencia.
	exec("INSERT INTO auth.textos_consentimiento(tipo,version,texto,vigente_desde) VALUES ('asistente_ia','v2-prueba','Texto de prueba',clock_timestamp())")
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DELETE FROM auth.textos_consentimiento WHERE tipo='asistente_ia' AND version IN ('v2-prueba','v3-futura')")
	})
	estado("v2-prueba", false)
	denegado()
	consentir("v1", true, 422)
	consentir("v2-prueba", true, 201)
	estado("v2-prueba", true)
	exec("INSERT INTO auth.textos_consentimiento(tipo,version,texto,vigente_desde) VALUES ('asistente_ia','v3-futura','Texto futuro',clock_timestamp()+interval '1 day')")
	estado("v2-prueba", true)
	consentir("v3-futura", true, 422)
	// Las fechas idénticas o atrasadas no pueden hacer reaparecer un otorgamiento.
	for _, aceptar := range []bool{true, false} {
		accion := "revocar"
		if aceptar {
			accion = "otorgar"
		}
		exec("INSERT INTO auth.consentimientos(id,parte_id,tipo,version_texto,texto,aceptado,accion,actor,origen,user_agent,metodo,ocurrido_en) VALUES ($1,$2,'asistente_ia','v2-prueba','Texto de prueba',$3,$4,$2,'prueba','prueba','app','2020-01-01T00:00:00Z')", uuid.New(), parte, aceptar, accion)
	}
	estado("v2-prueba", false)
	if _, err := app.Exec(ctx, "UPDATE auth.consentimientos SET aceptado=true WHERE parte_id=$1", parte); err == nil {
		t.Fatal("rol_api pudo modificar evidencia")
	}
	var conversaciones int
	if err := admin.QueryRow(ctx, "SELECT count(*) FROM asistente.conversacion WHERE usuario_id=$1", parte).Scan(&conversaciones); err != nil || conversaciones != 1 {
		t.Fatalf("historial de conversaciones: %d, %v", conversaciones, err)
	}
	request(http.MethodGet, "/v1/auth/consents", "", 200)
}
