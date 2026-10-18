package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// servidorDePrueba registra el Authorization y el cuerpo JSON de la última petición.
func servidorDePrueba(t *testing.T, status int) (*httptest.Server, func() (string, map[string]any)) {
	t.Helper()
	var auth string
	var cuerpo map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		cuerpo = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&cuerpo)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, func() (string, map[string]any) { return auth, cuerpo }
}

// La pata del núcleo manda el token del custodio y el monto como cadena de centavos.
func TestHU_NF_07_Criterio22_LaPataNucleoMandaTokenMontoYCUS(t *testing.T) {
	srv, ultima := servidorDePrueba(t, http.StatusOK)
	cliente := clienteNucleo{url: srv.URL, token: "token-del-custodio", cliente: srv.Client()}
	envio := Envio{
		Referencia: "dep-x", Monto: 150000, Moneda: "COP",
		Estado: estadoAprobado, CUS: "cus-123", OcurridoEn: time.Now().UTC(),
	}

	if err := cliente.Enviar(t.Context(), envio); err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	auth, cuerpo := ultima()
	if auth != "Bearer token-del-custodio" {
		t.Errorf("Authorization = %q, quiero el token del custodio", auth)
	}
	if cuerpo["monto"] != "150000" {
		t.Errorf("monto = %v, quiero la cadena de centavos", cuerpo["monto"])
	}
	if cuerpo["referencia"] != "dep-x" || cuerpo["estado"] != estadoAprobado || cuerpo["cus"] != "cus-123" {
		t.Errorf("cuerpo = %v, quiero referencia, estado y cus del envío", cuerpo)
	}
}

// Sin CUS, la pata del núcleo no incluye la clave, para que la columna quede NULL.
func TestHU_NF_07_Criterio22_SinCUSNoViajaLaClave(t *testing.T) {
	srv, ultima := servidorDePrueba(t, http.StatusOK)
	cliente := clienteNucleo{url: srv.URL, token: "token-del-custodio", cliente: srv.Client()}
	envio := Envio{Referencia: "dep-x", Monto: 1000, Moneda: "COP", Estado: estadoRechazado, OcurridoEn: time.Now().UTC()}

	if err := cliente.Enviar(t.Context(), envio); err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	_, cuerpo := ultima()
	if _, hay := cuerpo["cus"]; hay {
		t.Error("el cuerpo llevó cus con un envío sin CUS")
	}
}

// La pata de custodia notifica el movimiento con el monto en centavos y sin token.
func TestHU_NF_09_PataCustodioNotificaElMovimiento(t *testing.T) {
	srv, ultima := servidorDePrueba(t, http.StatusOK)
	cliente := clienteCustodio{url: srv.URL, cliente: srv.Client()}
	envio := Envio{Referencia: "dep-x", Monto: 150000, Moneda: "COP", Estado: estadoAprobado, OcurridoEn: time.Now().UTC()}

	if err := cliente.Enviar(t.Context(), envio); err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	auth, cuerpo := ultima()
	if auth != "" {
		t.Errorf("Authorization = %q, la pata de custodia no lleva token", auth)
	}
	if cuerpo["monto_centavos"] != "150000" {
		t.Errorf("monto_centavos = %v, quiero la cadena de centavos", cuerpo["monto_centavos"])
	}
	if cuerpo["referencia"] != "dep-x" {
		t.Errorf("referencia = %v, quiero dep-x", cuerpo["referencia"])
	}
}

// La pata de custodia serializa el monto entre comillas para no perder precisión en JavaScript.
func TestHU_NF_09_ElMontoDeCustodiaViajaComoCadena(t *testing.T) {
	var crudo []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		crudo, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cliente := clienteCustodio{url: srv.URL, cliente: srv.Client()}
	envio := Envio{Referencia: "dep-x", Monto: 9007199254740993, Moneda: "COP", Estado: estadoAprobado, OcurridoEn: time.Now().UTC()}
	if err := cliente.Enviar(t.Context(), envio); err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	if !strings.Contains(string(crudo), `"monto_centavos":"9007199254740993"`) {
		t.Errorf("cuerpo = %s, quiero el monto entre comillas", crudo)
	}
}

// Un destino que responde un status no exitoso cuenta como fallo y dispara el reintento.
func TestPataRechazaUnStatusNoExitoso(t *testing.T) {
	srv, _ := servidorDePrueba(t, http.StatusInternalServerError)
	cliente := clienteCustodio{url: srv.URL, cliente: srv.Client()}

	if err := cliente.Enviar(t.Context(), Envio{Monto: 1000, OcurridoEn: time.Now().UTC()}); err == nil {
		t.Fatal("un 500 se tomó como entrega exitosa")
	}
}

// Un fallo de transporte redacta el userinfo de la URL y conserva el host.
func TestErrorDeTransporteNoExponeLasCredenciales(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	cliente := clienteCustodio{
		url:     "http://usuario:secreto@" + host + "/notificaciones/depositos",
		cliente: &http.Client{Timeout: 2 * time.Second},
	}
	err := cliente.Enviar(t.Context(), Envio{Monto: 1000, OcurridoEn: time.Now().UTC()})
	if err == nil {
		t.Fatal("un destino caído debió devolver error")
	}
	for _, prohibido := range []string{"usuario", "secreto"} {
		if strings.Contains(err.Error(), prohibido) {
			t.Errorf("el error expuso %q: %v", prohibido, err)
		}
	}
	var conURL *url.Error
	if errors.As(err, &conURL) && strings.Contains(conURL.URL, "usuario") {
		t.Errorf("el error envuelto arrastra el userinfo: %q", conURL.URL)
	}
	if !strings.Contains(err.Error(), host) {
		t.Errorf("el error no conservó el host %q: %v", host, err)
	}
}

// Un status no exitoso redacta el userinfo y no copia el cuerpo del destino al error.
func TestErrorDeStatusNoExponeCredencialesNiCuerpo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "detalle-interno-del-destino")
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	cliente := clienteCustodio{
		url:     "http://usuario:secreto@" + host + "/notificaciones/depositos",
		cliente: srv.Client(),
	}

	err := cliente.Enviar(t.Context(), Envio{Monto: 1000, OcurridoEn: time.Now().UTC()})
	if err == nil {
		t.Fatal("un 500 se tomó como entrega exitosa")
	}
	for _, prohibido := range []string{"secreto", "detalle-interno-del-destino"} {
		if strings.Contains(err.Error(), prohibido) {
			t.Errorf("el error expuso %q: %v", prohibido, err)
		}
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("el error no reportó el status: %v", err)
	}
}
