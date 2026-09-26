//go:build integration

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func crearDeposito(t *testing.T, r http.Handler, body string, conSesion bool) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/depositos", strings.NewReader(body))
	if conSesion {
		req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func consultarDeposito(t *testing.T, r http.Handler, id string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/v1/depositos/"+id, nil)
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func datosDeDeposito(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	data, ok := decodeEnvelope(t, rec)["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente o con forma rara en %q", rec.Body.String())
	}
	return data
}

func contarDepositosDe(t *testing.T, pool *pgxpool.Pool, usuarioID string) int64 {
	t.Helper()

	return contarDepositosCon(t, pool, "SELECT count(*) FROM nucleo.deposito WHERE usuario_id = $1", usuarioID)
}

func contarDepositos(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()

	return contarDepositosCon(t, pool, "SELECT count(*) FROM nucleo.deposito")
}

func contarDepositosCon(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int64 {
	t.Helper()
	ctx := t.Context()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+nucleo.RolNucleo); err != nil {
		t.Fatalf("set role: %v", err)
	}

	var n int64
	if err := tx.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("contar depósitos: %v", err)
	}
	return n
}

// HU-NF-07 Criterio 16: crear la intención devuelve pendiente con su referencia y no escribe el diario.
func TestHU_NF_07_Criterio16_CreacionPendienteConReferenciaSinDiario(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	clave := "k-" + uuid.NewString()

	asientosAntes, lineasAntes := contarDiario(t, pool)
	rec := crearDeposito(t, r, `{"monto":"150000","idempotency_key":"`+clave+`"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quiero 201 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	asientosDespues, lineasDespues := contarDiario(t, pool)
	if asientosAntes != asientosDespues || lineasAntes != lineasDespues {
		t.Errorf("el diario cambió: journal_entry %d->%d, posting %d->%d",
			asientosAntes, asientosDespues, lineasAntes, lineasDespues)
	}

	data := datosDeDeposito(t, rec)
	if data["monto"] != "150000" {
		t.Errorf("monto = %v, quiero %q en centavos", data["monto"], "150000")
	}
	if data["moneda"] != "COP" {
		t.Errorf("moneda = %v, quiero COP", data["moneda"])
	}
	if data["estado"] != nucleo.EstadoDepositoPendiente {
		t.Errorf("estado = %v, quiero %q", data["estado"], nucleo.EstadoDepositoPendiente)
	}
	referencia, _ := data["referencia"].(string)
	if !strings.HasPrefix(referencia, "dep-") {
		t.Errorf("referencia = %q, quiero el prefijo dep-", referencia)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(referencia, "dep-")); err != nil {
		t.Errorf("referencia = %q, quiero un UUID detrás del prefijo", referencia)
	}
	id, ok := data["id"].(string)
	if !ok {
		t.Fatalf("id = %v, quiero una cadena con un UUID", data["id"])
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Errorf("id = %q, quiero un UUID", id)
	}
	creadaEn, ok := data["creada_en"].(string)
	if !ok {
		t.Fatalf("creada_en = %v, quiero una cadena RFC3339", data["creada_en"])
	}
	if _, err := time.Parse(time.RFC3339Nano, creadaEn); err != nil {
		t.Errorf("creada_en = %q, quiero una cadena RFC3339", creadaEn)
	}
	if n := contarDepositosDe(t, pool, usuarioID); n != 1 {
		t.Errorf("depósitos = %d, quiero 1", n)
	}
}

// HU-NF-07 Criterio 2: un monto que no es entero positivo se rechaza sin crear la intención.
func TestHU_NF_07_Criterio2_MontoInvalidoSeRechaza(t *testing.T) {
	tests := map[string]struct {
		body   string
		status int
	}{
		"cero":        {body: `{"monto":"0","idempotency_key":"k"}`, status: http.StatusUnprocessableEntity},
		"negativo":    {body: `{"monto":"-100","idempotency_key":"k"}`, status: http.StatusUnprocessableEntity},
		"decimal":     {body: `{"monto":"12.50","idempotency_key":"k"}`, status: http.StatusBadRequest},
		"texto":       {body: `{"monto":"abc","idempotency_key":"k"}`, status: http.StatusBadRequest},
		"numero json": {body: `{"monto":150000,"idempotency_key":"k"}`, status: http.StatusBadRequest},
	}

	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rec := crearDeposito(t, r, tc.body, true)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, quiero %d (cuerpo %q)", rec.Code, tc.status, rec.Body.String())
			}
			if env := decodeEnvelope(t, rec); env["success"] != false {
				t.Errorf("success = %v, quiero false", env["success"])
			}
		})
	}
	if n := contarDepositosDe(t, pool, usuarioID); n != 0 {
		t.Fatalf("depósitos = %d, quiero 0: un monto inválido no crea intención", n)
	}
}

// HU-NF-07 Criterio 2: una clave de 3000 caracteres se rechaza con 422 y sin crear intención.
func TestHU_NF_07_Criterio2_ClaveDeIdempotenciaDemasiadoLargaSeRechaza(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	clave := strings.Repeat("k", 3000)

	rec := crearDeposito(t, r, `{"monto":"100000","idempotency_key":"`+clave+`"}`, true)
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("status = %d, una clave larga no puede ser un 500 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	if n := contarDepositosDe(t, pool, usuarioID); n != 0 {
		t.Fatalf("depósitos = %d, quiero 0: la clave inválida no crea intención", n)
	}
}

// HU-NF-07 Criterio 3: una moneda distinta de COP se rechaza y sin moneda se registra COP.
func TestHU_NF_07_Criterio3_Moneda(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	ajena := crearDeposito(t, r, `{"monto":"100000","moneda":"USD","idempotency_key":"k-usd"}`, true)
	assertErrorEnvelope(t, ajena, http.StatusUnprocessableEntity, "ValidationError")

	sinMoneda := crearDeposito(t, r, `{"monto":"100000","idempotency_key":"k-cop"}`, true)
	if sinMoneda.Code != http.StatusCreated {
		t.Fatalf("status = %d, quiero 201 (cuerpo %q)", sinMoneda.Code, sinMoneda.Body.String())
	}
	if data := datosDeDeposito(t, sinMoneda); data["moneda"] != "COP" {
		t.Errorf("moneda = %v, quiero COP por defecto", data["moneda"])
	}
	if n := contarDepositosDe(t, pool, usuarioID); n != 1 {
		t.Errorf("depósitos = %d, quiero 1: solo la solicitud sin moneda", n)
	}
}

// HU-NF-07 Criterio 16.3: sin sesión autenticada la respuesta es 401 y no se crea ninguna intención.
func TestHU_NF_07_Criterio16_3_SinSesionNoSeCreaIntencion(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}})

	antes := contarDepositos(t, pool)
	rec := crearDeposito(t, r, `{"monto":"100000","idempotency_key":"k"}`, false)
	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
	if despues := contarDepositos(t, pool); despues != antes {
		t.Fatalf("depósitos %d->%d: sin sesión no debe crearse ninguna intención", antes, despues)
	}
}

// HU-NF-07 Criterio 20: el doble toque con la misma clave deja una sola intención.
func TestHU_NF_07_Criterio20_DobleToqueEsUnaSolaIntencion(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	clave := "k-" + uuid.NewString()

	primera := crearDeposito(t, r, `{"monto":"150000","idempotency_key":"`+clave+`"}`, true)
	if primera.Code != http.StatusCreated {
		t.Fatalf("primera: status = %d, quiero 201 (cuerpo %q)", primera.Code, primera.Body.String())
	}
	guardada := datosDeDeposito(t, primera)

	segunda := crearDeposito(t, r, `{"monto":"150000","idempotency_key":"`+clave+`"}`, true)
	if segunda.Code != http.StatusOK {
		t.Fatalf("segunda: status = %d, quiero 200 (cuerpo %q)", segunda.Code, segunda.Body.String())
	}
	repetida := datosDeDeposito(t, segunda)
	if repetida["id"] != guardada["id"] || repetida["referencia"] != guardada["referencia"] {
		t.Fatalf("segunda intención %v, quiero la misma %v", repetida, guardada)
	}

	otroMonto := crearDeposito(t, r, `{"monto":"999999","idempotency_key":"`+clave+`"}`, true)
	if otroMonto.Code != http.StatusOK {
		t.Fatalf("misma clave con otro monto: status = %d, quiero 200", otroMonto.Code)
	}
	if data := datosDeDeposito(t, otroMonto); data["monto"] != guardada["monto"] {
		t.Errorf("monto = %v, quiero el guardado %v", data["monto"], guardada["monto"])
	}

	if n := contarDepositosDe(t, pool, usuarioID); n != 1 {
		t.Fatalf("depósitos = %d, quiero 1: el doble toque no crea dos intenciones", n)
	}
}

// HU-NF-07 Criterio 20: un doble toque simultáneo con la misma clave deja una sola intención.
func TestHU_NF_07_Criterio20_DobleToqueConcurrenteEsUnaSolaIntencion(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	clave := "k-" + uuid.NewString()
	cuerpo := `{"monto":"250000","idempotency_key":"` + clave + `"}`

	const intentos = 2
	codigos := make(chan int, intentos)
	var wg sync.WaitGroup
	for range intentos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/depositos", strings.NewReader(cuerpo))
			req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			codigos <- rec.Code
		}()
	}
	wg.Wait()
	close(codigos)

	creadas, repetidas := 0, 0
	for codigo := range codigos {
		switch codigo {
		case http.StatusCreated:
			creadas++
		case http.StatusOK:
			repetidas++
		default:
			t.Fatalf("status = %d, quiero 201 o 200", codigo)
		}
	}
	if creadas != 1 || repetidas != 1 {
		t.Fatalf("201 = %d y 200 = %d, quiero una creación y una repetición", creadas, repetidas)
	}
	if n := contarDepositosDe(t, pool, usuarioID); n != 1 {
		t.Fatalf("depósitos = %d, quiero 1", n)
	}
}

// HU-NF-07 Criterio 21: GET /depositos/{id} devuelve el estado actual y no expone el depósito ajeno.
func TestHU_NF_07_Criterio21_ConsultaEstadoYNoExponeElAjeno(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	propio := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	ajeno := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{propio}})
	rAjeno := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{ajeno}})

	creado := crearDeposito(t, r, `{"monto":"300000","idempotency_key":"`+uuid.NewString()+`"}`, true)
	if creado.Code != http.StatusCreated {
		t.Fatalf("status = %d, quiero 201 (cuerpo %q)", creado.Code, creado.Body.String())
	}
	guardado := datosDeDeposito(t, creado)
	id, ok := guardado["id"].(string)
	if !ok {
		t.Fatalf("id = %v, quiero una cadena con un UUID", guardado["id"])
	}

	leido := consultarDeposito(t, r, id)
	if leido.Code != http.StatusOK {
		t.Fatalf("consulta propia: status = %d, quiero 200 (cuerpo %q)", leido.Code, leido.Body.String())
	}
	actual := datosDeDeposito(t, leido)
	if actual["id"] != id || actual["estado"] != guardado["estado"] {
		t.Fatalf("consulta = %v, quiero el mismo id y estado %v", actual, guardado)
	}

	rechazos := map[string]*httptest.ResponseRecorder{
		"depósito ajeno":       consultarDeposito(t, rAjeno, id),
		"depósito inexistente": consultarDeposito(t, r, uuid.NewString()),
		"id que no es uuid":    consultarDeposito(t, r, "no-es-un-uuid"),
	}
	for name, rec := range rechazos {
		assertErrorEnvelope(t, rec, http.StatusNotFound, "NotFound")
		if env := decodeEnvelope(t, rec); env["data"] != nil {
			t.Errorf("%s: data = %v, quiero null", name, env["data"])
		}
	}
}
