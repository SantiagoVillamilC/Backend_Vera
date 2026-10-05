package receipts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type envelopeError struct {
	Success bool `json:"success"`
	Error   *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func publicar(cuerpo []byte) *httptest.ResponseRecorder {
	h := NewHandler(NewService(nil))
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/receipts", bytes.NewReader(cuerpo))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	return rec
}

func TestCreate_SinBackendResponde501ConEnvelope(t *testing.T) {
	rec := publicar([]byte("{}"))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, quiero 501", rec.Code)
	}
	var env envelopeError
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("cuerpo no es JSON: %v", err)
	}
	if env.Success || env.Error == nil || env.Error.Code != "NotImplemented" {
		t.Fatalf("envelope inesperado: %s", rec.Body.String())
	}
}

func TestCreate_CuerpoVacioSigueDando400(t *testing.T) {
	if rec := publicar(nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quiero 400", rec.Code)
	}
}

func TestCreate_MasDe1MiBSigueDando413(t *testing.T) {
	rec := publicar([]byte(strings.Repeat("a", 1<<20+1)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, quiero 413", rec.Code)
	}
}

func TestProcess_SinBackendDevuelveCentinela(t *testing.T) {
	_, err := NewService(nil).Process(t.Context(), []byte("x"))
	if !errors.Is(err, ErrSinBackend) {
		t.Fatalf("err = %v, quiero ErrSinBackend", err)
	}
}
