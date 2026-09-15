package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/httpserver"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
)

// TestRequestIDDeChiLlegaAlLog usa el middleware real que genera el id y verifica que la línea
// emitida dentro de la petición lleve ese mismo id bajo request_id.
func TestRequestIDDeChiLlegaAlLog(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(logging.WithRequestID(slog.NewJSONHandler(&buf, nil)))
	handler := httpserver.RequestID()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		logger.InfoContext(r.Context(), "request")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	id := rec.Header().Get("X-Request-Id")
	if id == "" {
		t.Fatal("el middleware no generó el header X-Request-Id")
	}

	var registro map[string]any
	if err := json.Unmarshal(buf.Bytes(), &registro); err != nil {
		t.Fatalf("registro inválido: %v (%q)", err, buf.String())
	}
	if registro["request_id"] != id {
		t.Errorf("request_id = %v, quiero el generado %q", registro["request_id"], id)
	}
}

func TestRequestIDFueraDePeticionNoSeEstampa(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(logging.WithRequestID(slog.NewJSONHandler(&buf, nil)))

	logger.InfoContext(t.Context(), "sin petición")

	var registro map[string]any
	if err := json.Unmarshal(buf.Bytes(), &registro); err != nil {
		t.Fatalf("registro inválido: %v (%q)", err, buf.String())
	}
	if _, ok := registro["request_id"]; ok {
		t.Errorf("request_id no debería estar fuera de una petición: %v", registro)
	}
}
