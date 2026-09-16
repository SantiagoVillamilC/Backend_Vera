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
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/receipts"
	"github.com/go-chi/chi/v5"
)

// newTestRouter arma el router con el adaptador de usuario de prueba y un servicio de saldo real.
func newTestRouter() http.Handler {
	return NewRouter(nil, auth.UsuarioDePrueba{}, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba)
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

func TestAuthedRouteWithoutTestCredentialReturnsEnvelope401(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/copilot", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
}

func TestAuthedRouteRejectsMissingTestCredential(t *testing.T) {
	tests := map[string]func(*http.Request){
		"empty header":  func(req *http.Request) { req.Header.Set(auth.CabeceraUsuarioDePrueba, "") },
		"blank header":  func(req *http.Request) { req.Header.Set(auth.CabeceraUsuarioDePrueba, "  ") },
		"otra cabecera": func(req *http.Request) { req.Header.Set("X-Otra-Cosa", "1") },
	}

	for name, preparar := range tests {
		t.Run(name, func(t *testing.T) {
			r := newTestRouter()
			req := httptest.NewRequest(http.MethodPost, "/copilot", nil)
			preparar(req)
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
		})
	}
}

// Un bearer bien formado no basta en la fase 0: la credencial de prueba es la cabecera X-Test-User.
func TestAuthedRouteRejectsWellFormedBearerWithoutTestHeader(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/copilot", nil)
	req.Header.Set("Authorization", "Bearer cualquier-token")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
}

// Con la credencial de prueba, el handler de /copilot responde 400 por el cuerpo vacío, nunca 401.
func TestAuthedRouteWithTestCredentialReachesHandler(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/copilot", nil)
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("con la credencial de prueba no debe dar 401 (cuerpo %q)", rec.Body.String())
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quiero 400 por el cuerpo vacío (cuerpo %q)", rec.Code, rec.Body.String())
	}
}

// El handler autenticado ve el usuario_id que dejó RequireAuth, resuelto por el adaptador de prueba.
func TestAuthenticatedRouteSeesTestUserID(t *testing.T) {
	r := chi.NewRouter()
	r.Use(RequireAuth(auth.UsuarioDePrueba{}))
	r.Get("/quien-soy", func(w http.ResponseWriter, req *http.Request) {
		usuarioID, ok := identidad.UsuarioIDDeContexto(req.Context())
		if !ok {
			api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Sin usuario en el contexto.", nil)
			return
		}
		api.WriteData(w, http.StatusOK, map[string]string{"user_id": usuarioID}, nil)
	})

	req := httptest.NewRequest(http.MethodGet, "/quien-soy", nil)
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente o con forma rara en %q", rec.Body.String())
	}
	if got := data["user_id"]; got != auth.UsuarioDePruebaID {
		t.Fatalf("user_id = %v, quiero %s", got, auth.UsuarioDePruebaID)
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
