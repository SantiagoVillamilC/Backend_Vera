package custodiosim

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

func TestLectorExtractoLeyElCorteCompleto(t *testing.T) {
	t.Parallel()

	var metodo, ruta string
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metodo, ruta = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"corte_en":"2026-09-17T12:00:00Z","saldo_centavos":"9007199254740993","origen":"simulador-custodia"}`)
	}))
	defer servidor.Close()

	extracto, err := NuevoLectorExtracto(servidor.URL+"/extracto", nil).Extracto(t.Context())
	if err != nil {
		t.Fatalf("Extracto() error = %v", err)
	}
	if metodo != http.MethodGet || ruta != "/extracto" {
		t.Errorf("petición = %s %s, quiero GET /extracto", metodo, ruta)
	}
	if quiero := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC); !extracto.CorteEn.Equal(quiero) {
		t.Errorf("CorteEn = %v, quiero %v", extracto.CorteEn, quiero)
	}
	if extracto.Saldo != nucleo.Centavos(9007199254740993) {
		t.Errorf("Saldo = %d, quiero 9007199254740993 (entero exacto pasado 2^53)", extracto.Saldo)
	}
	if extracto.Origen != "simulador-custodia" {
		t.Errorf("Origen = %q, quiero %q", extracto.Origen, "simulador-custodia")
	}
}

func TestLectorExtractoRechazaRespuestasInvalidas(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		status int
		cuerpo string
	}{
		"status no exitoso": {status: http.StatusInternalServerError, cuerpo: `{}`},
		"saldo numerico":    {status: http.StatusOK, cuerpo: `{"corte_en":"2026-09-17T12:00:00Z","saldo_centavos":700,"origen":"x"}`},
		"json malformado":   {status: http.StatusOK, cuerpo: `{"corte_en":`},
		"sin corte":         {status: http.StatusOK, cuerpo: `{"saldo_centavos":"700","origen":"x"}`},
		"sin saldo":         {status: http.StatusOK, cuerpo: `{"corte_en":"2026-09-17T12:00:00Z","origen":"x"}`},
		"saldo nulo":        {status: http.StatusOK, cuerpo: `{"corte_en":"2026-09-17T12:00:00Z","saldo_centavos":null,"origen":"x"}`},
		"sin origen":        {status: http.StatusOK, cuerpo: `{"corte_en":"2026-09-17T12:00:00Z","saldo_centavos":"700"}`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.cuerpo)
			}))
			defer servidor.Close()

			if _, err := NuevoLectorExtracto(servidor.URL+"/extracto", nil).Extracto(t.Context()); err == nil {
				t.Fatalf("Extracto() con %s = nil, quiero error", name)
			}
		})
	}
}

func TestLectorExtractoRechazaUnCuerpoMasGrandeQueElTope(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", topeCuerpoExtracto+1))
	}))
	defer servidor.Close()

	if _, err := NuevoLectorExtracto(servidor.URL+"/extracto", nil).Extracto(t.Context()); err == nil {
		t.Fatal("Extracto() con un cuerpo mayor al tope = nil, quiero error")
	}
}

// Un fallo de transporte redacta el userinfo de la URL y conserva el host.
func TestLectorExtractoNoExponeElUserinfoDeLaURL(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := strings.TrimPrefix(servidor.URL, "http://")
	servidor.Close()

	lector := NuevoLectorExtracto("http://usuario:secreto@"+host+"/extracto", &http.Client{Timeout: 2 * time.Second})
	_, err := lector.Extracto(t.Context())
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

func TestLectorExtractoRespetaElContexto(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer servidor.Close()

	ctx, cancelar := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancelar()

	if _, err := NuevoLectorExtracto(servidor.URL+"/extracto", nil).Extracto(ctx); err == nil {
		t.Fatal("Extracto() con el contexto vencido = nil, quiero error")
	}
}
