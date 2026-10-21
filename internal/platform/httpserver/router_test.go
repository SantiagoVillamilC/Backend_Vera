package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	yaml "go.yaml.in/yaml/v3"
)

// newTestRouter arma el router con el adaptador de usuario de prueba y un servicio de saldo real.
func newTestRouter() http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}})
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
	body := `{"correo":"user@example.com","contrasena":"secret"}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/login", strings.NewReader(body))
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("POST /v1/auth/login devolvió 401, pero es público (cuerpo %q)", rec.Body.String())
	}
	assertErrorEnvelope(t, rec, http.StatusNotImplemented, "NotImplemented")
}

func TestPublicRoutesAreStubsInEnvelope(t *testing.T) {
	rutas := []string{
		"/v1/auth/register",
		"/v1/auth/register/verify-phone",
		"/v1/auth/otp/resend",
		"/v1/auth/login",
		"/v1/auth/refresh",
		"/v1/auth/recovery/request",
		"/v1/auth/recovery/verify",
		"/v1/auth/recovery/reset",
	}

	r := newTestRouter()
	for _, ruta := range rutas {
		t.Run(ruta, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, ruta, strings.NewReader(`{}`))
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			assertErrorEnvelope(t, rec, http.StatusNotImplemented, "NotImplemented")
		})
	}
}

func TestSessionsWithoutCredentialReturnsEnvelope401(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/auth/sessions", nil)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
}

func TestDeleteSessionWithMalformedUUIDReturnsEnvelope400(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "/v1/auth/sessions/no-es-uuid", nil)
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusBadRequest, "BadRequest")
	env := decodeEnvelope(t, rec)
	apiErr, _ := env["error"].(map[string]any)
	details, ok := apiErr["details"].([]any)
	if !ok || len(details) == 0 {
		t.Fatalf("quiero details con el campo session_id, obtuve %q", rec.Body.String())
	}
	primer, _ := details[0].(map[string]any)
	if primer["field"] != "session_id" || primer["rule"] != "format" {
		t.Fatalf("detail = %v, quiero session_id/format", primer)
	}
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

// La consulta del monto máximo del Criterio 14 vive en el grupo autenticado (HU-NF-06).
func TestMontoMaximoRouteRequiresAuth(t *testing.T) {
	r := newTestRouter()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/transferencias/maximo", nil)
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
	r.Use(RequireAuth(auth.UsuarioDePrueba{}, RutasDeDinero))
	r.Get("/quien-soy", func(w http.ResponseWriter, req *http.Request) {
		id, ok := identidad.DeContexto(req.Context())
		if !ok {
			api.WriteError(w, http.StatusUnauthorized, "Unauthorized", "Sin usuario en el contexto.", nil)
			return
		}
		api.WriteData(w, http.StatusOK, map[string]string{
			"user_id":   id.UsuarioID,
			"sesion_id": id.SesionID,
			"contexto":  id.Contexto,
		}, nil)
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
	if got := data["sesion_id"]; got != auth.UsuarioDePruebaSesionID {
		t.Fatalf("sesion_id = %v, quiero %s", got, auth.UsuarioDePruebaSesionID)
	}
	if got := data["contexto"]; got != "usuario" {
		t.Fatalf("contexto = %v, quiero usuario", got)
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
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/auth/login", nil)
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

// contratoRutas modela la parte del contrato que el test necesita: paths y las etiquetas de cada operacion.
type contratoRutas struct {
	Paths map[string]map[string]struct {
		Tags []string `yaml:"tags"`
	} `yaml:"paths"`
}

// TestRutasPublicasCoincidenConElContrato exige que la etiqueta public del YAML y el router no se desincronicen.
func TestRutasPublicasCoincidenConElContrato(t *testing.T) {
	crudo, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("no se pudo leer el contrato: %v", err)
	}
	var contrato contratoRutas
	if err := yaml.Unmarshal(crudo, &contrato); err != nil {
		t.Fatalf("no se pudo parsear el contrato: %v", err)
	}

	metodos := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	r := newTestRouter()
	publicas := 0
	privadas := 0
	for ruta, operaciones := range contrato.Paths {
		for metodo, operacion := range operaciones {
			if !metodos[strings.ToLower(metodo)] {
				continue
			}
			esPublica := slices.Contains(operacion.Tags, "public")
			if esPublica {
				publicas++
			} else {
				privadas++
			}

			camino := strings.ReplaceAll(ruta, "{session_id}", "00000000-0000-4000-8000-000000000000")
			req := httptest.NewRequestWithContext(t.Context(), strings.ToUpper(metodo), camino, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("%s %s: Content-Type = %q, quiero envelope JSON", metodo, ruta, ct)
			}
			if esPublica {
				if rec.Code == http.StatusNotFound {
					t.Fatalf("%s %s: es publica y responde 404", metodo, ruta)
				}
			} else if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s: no es publica y sin credencial responde %d, quiero 401", metodo, ruta, rec.Code)
			}
		}
	}
	if publicas != 8 {
		t.Fatalf("operaciones publicas = %d, quiero 8", publicas)
	}
	if privadas != 36 {
		t.Fatalf("operaciones no publicas = %d, quiero 36", privadas)
	}
}
