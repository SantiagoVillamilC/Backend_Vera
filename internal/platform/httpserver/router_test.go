package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/receipts"
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

func TestAuthedRouteRejectsMalformedAuthorization(t *testing.T) {
	tests := map[string]string{
		"basic scheme":       "Basic eA==",
		"bearer no space":    "Bearer",
		"empty bearer token": "Bearer ",
		"lowercase scheme":   "bearer abc",
	}

	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			r := newTestRouter()
			req := httptest.NewRequest(http.MethodPost, "/copilot", nil)
			req.Header.Set("Authorization", header)
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
		})
	}
}

func TestPanicRecoveredInsideEnvelope(t *testing.T) {
	handler := Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusInternalServerError, "InternalError")
}

func TestTimeoutReturnsEnvelope(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	handler := Timeout(20 * time.Millisecond)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusGatewayTimeout, "Timeout")
}

func TestServerIgnoresIncomingRequestID(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("X-Request-Id", "client-controlled")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	got := rec.Header().Get("X-Request-Id")
	if got == "" {
		t.Fatal("la respuesta no trae X-Request-Id")
	}
	if got == "client-controlled" {
		t.Fatalf("el servidor aceptó el X-Request-Id del cliente: %q", got)
	}
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
		req := httptest.NewRequest(http.MethodGet, "/no-existe", nil)
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

func TestProbesAreExemptFromRateLimit(t *testing.T) {
	r := newTestRouter()

	for i := 0; i <= rateLimitRequests+5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("el probe /healthz fue rate-limited en la petición %d", i)
		}
	}
}

func TestMethodNotAllowedKeepsAllowHeader(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusMethodNotAllowed, "MethodNotAllowed")
	if allow := rec.Header().Values("Allow"); !slices.Contains(allow, http.MethodPost) {
		t.Fatalf("Allow = %v, quiero que contenga POST", allow)
	}
}

func TestTimeoutClientCancelWritesNoResponse(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	handler := Timeout(time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusGatewayTimeout {
		t.Fatal("un contexto cancelado por el cliente no debe responder 504")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("no se esperaba cuerpo en un contexto cancelado, body = %q", rec.Body.String())
	}
}

func TestResponseDTOsSerializeWithLowercaseKeys(t *testing.T) {
	tests := map[string]struct {
		value any
		keys  []string
	}{
		"auth.User":            {auth.User{ID: "u1", Email: "a@b.com"}, []string{"id", "email"}},
		"auth.Session":         {auth.Session{Token: "t", User: auth.User{}}, []string{"token", "user"}},
		"auth.MFAConfig":       {auth.MFAConfig{Enabled: true, Secret: "s"}, []string{"enabled", "secret"}},
		"copilot.Response":     {copilot.Response{Intent: copilot.IntentQuery, Message: "m"}, []string{"intent", "message"}},
		"receipts.Receipt":     {receipts.Receipt{ID: "r", UserID: "u", Merchant: "m", Total: 1, Items: []receipts.ReceiptItem{}}, []string{"id", "user_id", "merchant", "total", "items"}},
		"receipts.ReceiptItem": {receipts.ReceiptItem{Name: "n", Quantity: 1, Price: 2}, []string{"name", "quantity", "price"}},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for _, key := range tc.keys {
				if _, ok := got[key]; !ok {
					t.Errorf("falta la clave %q en %s", key, raw)
				}
			}
		})
	}
}
