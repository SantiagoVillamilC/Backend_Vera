package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

func ipResuelta(remoto string, cabeceras ...string) string {
	var got string
	h := ClientIPConfianzaLocal("X-Real-IP")(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = middleware.GetClientIP(r.Context())
	}))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	req.RemoteAddr = remoto
	for _, v := range cabeceras {
		req.Header.Add("X-Real-IP", v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestClientIPConfianzaLocal(t *testing.T) {
	casos := []struct {
		nombre, remoto string
		cabeceras      []string
		quiero         string
	}{
		{"publica ignora cabecera", "203.0.113.9:4000", []string{"198.51.100.7"}, "203.0.113.9"},
		{"loopback confia", "127.0.0.1:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"privada confia", "10.0.0.5:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"privada 192.168 confia", "192.168.1.2:80", []string{"198.51.100.7"}, "198.51.100.7"},
		{"ipv6 loopback confia", "[::1]:4000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"ipv6 ULA confia", "[fd00::2]:4000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"ipv6 publica ignora", "[2001:db8::99]:4000", []string{"2001:db8::1"}, "2001:db8::99"},
		{"v4 mapeada privada confia", "[::ffff:10.0.0.5]:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"cabecera invalida cae a remoto", "127.0.0.1:4000", []string{"no-es-ip"}, "127.0.0.1"},
		{"sin cabecera cae a remoto", "10.0.0.5:4000", nil, "10.0.0.5"},
		{"ultimo valor invalido cae a remoto", "127.0.0.1:4000", []string{"198.51.100.7", "basura"}, "127.0.0.1"},
		{"remoto sin puerto publico", "203.0.113.9", []string{"198.51.100.7"}, "203.0.113.9"},
	}
	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			if got := ipResuelta(c.remoto, c.cabeceras...); got != c.quiero {
				t.Errorf("ip = %q, quiero %q", got, c.quiero)
			}
		})
	}
}

// El bucket del rate limit sigue la IP resuelta: una cabecera falsa desde fuera no lo cambia.
func TestClientIPConfianzaLocal_Bucket(t *testing.T) {
	chain := ClientIPConfianzaLocal("X-Real-IP")(RateLimitExempting(nil, 1, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	hit := func(remoto, cabecera string) int {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		req.RemoteAddr = remoto
		req.Header.Set("X-Real-IP", cabecera)
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		return rec.Code
	}
	if hit("203.0.113.9:1", "198.51.100.1") != 200 {
		t.Fatal("primera peticion debe pasar")
	}
	if got := hit("203.0.113.9:2", "198.51.100.2"); got != http.StatusTooManyRequests {
		t.Errorf("cabecera falsa desde publica cambio el bucket: %d", got)
	}
	if hit("127.0.0.1:1", "198.51.100.3") != 200 || hit("127.0.0.1:2", "198.51.100.4") != 200 {
		t.Error("desde loopback la cabecera debe separar buckets")
	}
	if got := hit("10.0.0.5:1", "198.51.100.3"); got != http.StatusTooManyRequests {
		t.Errorf("misma IP de cabecera debe compartir bucket: %d", got)
	}
}
