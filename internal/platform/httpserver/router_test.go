package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testJWTSecret = "0123456789abcdef0123456789abcdef"

// newTestRouter arma el router con un pool nil. Ninguna de las rutas que ejercitan estos
// tests toca la base, así que el pool no se usa.
func newTestRouter() http.Handler {
	return NewRouter(nil, testJWTSecret)
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("la respuesta no es JSON: %v (cuerpo %q)", err, rec.Body.String())
	}
	return body
}

func assertErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, quiero %d (cuerpo %q)", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, quiero application/json", ct)
	}
	env := decodeEnvelope(t, rec)
	if success, _ := env["success"].(bool); success {
		t.Fatalf("success = true, quiero false (cuerpo %q)", rec.Body.String())
	}
	apiErr, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error ausente o con forma rara en %q", rec.Body.String())
	}
	if code, _ := apiErr["code"].(string); code != wantCode {
		t.Fatalf("error.code = %q, quiero %q", code, wantCode)
	}
}

func TestLoginWithoutTokenIsPublic(t *testing.T) {
	r := newTestRouter()
	body := `{"email":"user@example.com","password":"secret"}`
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("POST /login devolvió 401, pero es público (cuerpo %q)", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, quiero application/json", ct)
	}
	_ = decodeEnvelope(t, rec)
}

func TestAuthedRouteWithoutTokenReturnsEnvelope401(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/copilot", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
}

func TestUnknownRouteReturnsEnvelope404(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/no-existe", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusNotFound, "NotFound")
}

func TestRateLimitedResponseKeepsCORSHeaders(t *testing.T) {
	r := newTestRouter()

	var rec *httptest.ResponseRecorder
	for i := 0; i <= rateLimitRequests; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.Header.Set("Origin", "https://example.com")
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
	}

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("última respuesta = %d, quiero 429", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("el 429 no trae CORS: Access-Control-Allow-Origin = %q", got)
	}
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, "TooManyRequests")
}
