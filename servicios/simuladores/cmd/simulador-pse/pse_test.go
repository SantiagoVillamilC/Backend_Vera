package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// peticionPSE arma una petición de formulario contra una ruta del PSE simulado.
func peticionPSE(t *testing.T, metodo, ruta string, form url.Values) *http.Request {
	req := httptest.NewRequestWithContext(t.Context(), metodo, ruta, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// La redirección simulada muestra la referencia y el monto que viajaron en la URL.
func TestHU_NF_07_Criterio15Y16_1_RedireccionPSELlevaLaReferenciaYElMonto(t *testing.T) {
	almacen := almacenDePrueba(t)
	pagina := NewPaginaPSE(NuevoDespachador(almacen, &enviadorFalso{}, &enviadorFalso{}))

	rec := httptest.NewRecorder()
	pagina.Rutas().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/pse?referencia=dep-abc&monto=150000", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200", rec.Code)
	}
	cuerpo := rec.Body.String()
	if !strings.Contains(cuerpo, "dep-abc") {
		t.Error("la página no mostró la referencia")
	}
	if !strings.Contains(cuerpo, "1500,00") {
		t.Error("la página no mostró el monto presentado")
	}
	if !strings.Contains(cuerpo, `name="monto" value="150000"`) {
		t.Error("el formulario no llevó el monto en centavos")
	}
}

// Una referencia o un monto que no son válidos no sirven la página de PSE.
func TestHU_NF_07_Criterio2_FormularioPSEInvalidoDa400(t *testing.T) {
	almacen := almacenDePrueba(t)
	pagina := NewPaginaPSE(NuevoDespachador(almacen, &enviadorFalso{}, &enviadorFalso{}))

	rutas := map[string]string{
		"sin monto":      "/pse?referencia=dep-abc",
		"monto cero":     "/pse?referencia=dep-abc&monto=0",
		"monto negativo": "/pse?referencia=dep-abc&monto=-5",
		"monto texto":    "/pse?referencia=dep-abc&monto=abc",
		"sin referencia": "/pse?monto=150000",
	}
	for nombre, ruta := range rutas {
		t.Run(nombre, func(t *testing.T) {
			rec := httptest.NewRecorder()
			pagina.Rutas().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, ruta, nil))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, quiero 400", rec.Code)
			}
		})
	}
}

// Autorizar dispara la pata del núcleo contra su endpoint, con el token y el CUS generado.
func TestHU_NF_07_Criterio15_AutorizarDisparaLaConfirmacionAlNucleo(t *testing.T) {
	var (
		mu     sync.Mutex
		auth   string
		cuerpo map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		auth = r.Header.Get("Authorization")
		cuerpo = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&cuerpo)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	almacen := almacenDePrueba(t)
	custodio := &enviadorFalso{}
	despachador := NuevoDespachador(almacen,
		clienteNucleo{url: srv.URL, token: "token-del-custodio", cliente: srv.Client()},
		custodio,
	)
	arrancarDespacho(t, despachador)

	rec := httptest.NewRecorder()
	pagina := NewPaginaPSE(despachador)
	pagina.Rutas().ServeHTTP(rec, peticionPSE(t, http.MethodPost, "/pse/autorizar",
		url.Values{"referencia": {"dep-abc"}, "monto": {"150000"}}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	esperarHasta(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return cuerpo != nil
	})
	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer token-del-custodio" {
		t.Errorf("Authorization = %q, quiero el token del custodio", auth)
	}
	if cuerpo["referencia"] != "dep-abc" || cuerpo["monto"] != "150000" || cuerpo["estado"] != estadoAprobado {
		t.Errorf("cuerpo = %v, quiero la confirmación aprobada del envío", cuerpo)
	}
	cus, _ := cuerpo["cus"].(string)
	if cus == "" {
		t.Error("el CUS generado por la simulación debe viajar en la confirmación")
	}
	esperarHasta(t, func() bool { return custodio.total() == 1 })
}

// Cancelar dispara la señal de rechazo al núcleo y no notifica al simulador de custodia.
func TestHU_NF_07_Criterio17_CancelarDisparaElRechazo(t *testing.T) {
	almacen := almacenDePrueba(t)
	nucleo := &enviadorFalso{}
	custodio := &enviadorFalso{}
	despachador := NuevoDespachador(almacen, nucleo, custodio)
	arrancarDespacho(t, despachador)
	pagina := NewPaginaPSE(despachador)

	rec := httptest.NewRecorder()
	pagina.Rutas().ServeHTTP(rec, peticionPSE(t, http.MethodPost, "/pse/cancelar",
		url.Values{"referencia": {"dep-abc"}, "monto": {"150000"}}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	esperarHasta(t, func() bool { return nucleo.total() == 1 })
	rechazo := nucleo.ultimo(t)
	if rechazo.Estado != estadoRechazado {
		t.Errorf("estado = %q, quiero %q", rechazo.Estado, estadoRechazado)
	}
	if rechazo.CUS != "" {
		t.Errorf("un rechazo no lleva CUS, llegó %q", rechazo.CUS)
	}
	if custodio.total() != 0 {
		t.Errorf("el simulador de custodia se notificó %d veces en un rechazo, quiero 0", custodio.total())
	}
}

// Un Origin que no coincide con el Host de la petición da 403 y no registra el evento.
func TestHU_NF_09_OrigenDeOtroHostDa403SinRegistrar(t *testing.T) {
	almacen := almacenDePrueba(t)
	pagina := NewPaginaPSE(NuevoDespachador(almacen, &enviadorFalso{}, &enviadorFalso{}))

	req := peticionPSE(t, http.MethodPost, "/pse/autorizar",
		url.Values{"referencia": {"dep-abc"}, "monto": {"150000"}})
	req.Header.Set("Origin", "http://otro-host.example")
	rec := httptest.NewRecorder()
	pagina.Rutas().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, quiero 403", rec.Code)
	}
	var total int
	if err := almacen.db.QueryRowContext(t.Context(), "SELECT count(*) FROM envio").Scan(&total); err != nil {
		t.Fatalf("contar envíos: %v", err)
	}
	if total != 0 {
		t.Errorf("envíos registrados = %d, quiero 0", total)
	}
}

// Sin Origin, la petición pasa porque el navegador siempre lo manda en un POST del formulario.
func TestHU_NF_09_SinOriginElPOSTDelPSESeAcepta(t *testing.T) {
	almacen := almacenDePrueba(t)
	pagina := NewPaginaPSE(NuevoDespachador(almacen, &enviadorFalso{}, &enviadorFalso{}))

	rec := httptest.NewRecorder()
	pagina.Rutas().ServeHTTP(rec, peticionPSE(t, http.MethodPost, "/pse/cancelar",
		url.Values{"referencia": {"dep-abc"}, "monto": {"150000"}}))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
}

// Un cuerpo por encima del tope da 413 y no registra el evento.
func TestHU_NF_09_CuerpoGrandeDelPSEDa413SinRegistrar(t *testing.T) {
	almacen := almacenDePrueba(t)
	pagina := NewPaginaPSE(NuevoDespachador(almacen, &enviadorFalso{}, &enviadorFalso{}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/pse/autorizar",
		strings.NewReader("referencia="+strings.Repeat("a", maximoCuerpoPSE+1)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	pagina.Rutas().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, quiero 413", rec.Code)
	}
	var total int
	if err := almacen.db.QueryRowContext(t.Context(), "SELECT count(*) FROM envio").Scan(&total); err != nil {
		t.Fatalf("contar envíos: %v", err)
	}
	if total != 0 {
		t.Errorf("envíos registrados = %d, quiero 0", total)
	}
}

// El PSE simulado corta con 429 cuando la misma IP supera el tope de peticiones.
func TestHU_NF_07_RateLimitDelPSE(t *testing.T) {
	almacen := almacenDePrueba(t)
	pagina := NewPaginaPSE(NuevoDespachador(almacen, &enviadorFalso{}, &enviadorFalso{}))
	rutas := pagina.Rutas()

	var ultimo int
	for range topePeticionesPSE + 1 {
		rec := httptest.NewRecorder()
		rutas.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/pse?referencia=dep-abc&monto=150000", nil))
		ultimo = rec.Code
	}
	if ultimo != http.StatusTooManyRequests {
		t.Errorf("status = %d, quiero 429 al superar el tope", ultimo)
	}
}
