package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

type accionesVeraPrueba struct {
	llamadas int
	id       uuid.UUID
	sol      copilot.ConfirmacionAccion
	err      error
}

func (a *accionesVeraPrueba) SolicitarCodigo(_ context.Context, id uuid.UUID, _ string) (auth.Desafio, error) {
	a.llamadas++
	a.id = id
	return auth.Desafio{ID: uuid.New(), Proposito: auth.PropositoConfirmacionTransferencia, ExpiraEnSegundos: 300}, a.err
}
func (a *accionesVeraPrueba) Confirmar(_ context.Context, id uuid.UUID, sol copilot.ConfirmacionAccion) (copilot.AccionResuelta, error) {
	a.llamadas++
	a.id = id
	a.sol = sol
	return a.respuesta(id), a.err
}
func (a *accionesVeraPrueba) Cancelar(_ context.Context, id uuid.UUID) (copilot.AccionResuelta, error) {
	a.llamadas++
	a.id = id
	r := a.respuesta(id)
	r.Accion.Estado = "cancelada"
	r.Transferencia = nil
	return r, a.err
}
func (a *accionesVeraPrueba) respuesta(id uuid.UUID) copilot.AccionResuelta {
	return copilot.AccionResuelta{Accion: copilot.TarjetaTexto{ID: id, Estado: "confirmada", Tipo: "transferencia_interna", Monto: 15000000, Nombre: "Laura R.", RequiereCodigo: true, ExpiraEn: time.Now()}, Transferencia: &nucleo.Transferencia{ID: uuid.New(), LlaveDestino: "3001234567", Monto: 15000000, Moneda: "COP", Estado: "confirmada", CreadaEn: time.Now(), ActualizadaEn: time.Now()}}
}
func TestVeraAccionesHTTPContratos(t *testing.T) {
	id := uuid.New()
	d := uuid.New()
	a := &accionesVeraPrueba{}
	s := servidorV1{Vera: DependenciasVera{Acciones: a}}
	for _, body := range []string{`null`, `{"monto":"1"}`, `{"destino":"otro"}`, `{"codigo":"123456","codigo":"654321"}`, `{"codigo":"no"}`, `{"desafio_id":"no-uuid"}`} {
		w := httptest.NewRecorder()
		s.ConfirmarAccionVera(w, httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(body)), id)
		if w.Code != 422 || a.llamadas != 0 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	body := `{"codigo":"123456","desafio_id":"` + d.String() + `","sin_verificacion_local":true}`
	w := httptest.NewRecorder()
	s.ConfirmarAccionVera(w, httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(body)), id)
	if w.Code != 200 || a.sol.DesafioID != d || a.sol.Codigo != "123456" || !a.sol.SinVerificacionLocal || a.id != id {
		t.Fatalf("%d %s %+v", w.Code, w.Body, a.sol)
	}
	var response struct {
		Data api.AccionResuelta `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.AccionPendiente.Monto != "15000000" || response.Data.Transferencia.Monto != "15000000" {
		t.Fatal(w.Body)
	}
	for _, handler := range []func(http.ResponseWriter, *http.Request, uuid.UUID){s.SolicitarCodigoAccionVera, s.CancelarAccionVera} {
		w = httptest.NewRecorder()
		handler(w, httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(`{}`)), id)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		antes := a.llamadas
		w = httptest.NewRecorder()
		handler(w, httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(`{"monto":"1"}`)), id)
		if w.Code != 422 || a.llamadas != antes {
			t.Fatal(w.Code, w.Body)
		}
	}
}

type identidadDegradadaConfirmacion struct{}

func (identidadDegradadaConfirmacion) Identidad(*http.Request) (identidad.Identidad, error) {
	return identidad.Identidad{UsuarioID: "financiero", ParteID: uuid.NewString()}, auth.ErrAuthNoDisponible
}
func TestVeraAccionesFailClosedEnRutasDinamicas(t *testing.T) {
	a := &accionesVeraPrueba{}
	router := NewRouterConVera(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDegradadaConfirmacion{}}, DependenciasVera{Acciones: a})
	for _, op := range []string{"codigo", "confirmar", "cancelar"} {
		for _, id := range []string{uuid.NewString(), "mal%2Fformado"} {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "POST", "/v1/vera/acciones/"+id+"/"+op, strings.NewReader(`{}`)))
			if w.Code != 503 || !strings.Contains(w.Body.String(), "AuthUnavailable") || a.llamadas != 0 {
				t.Fatalf("%s: %d %s", op, w.Code, w.Body)
			}
		}
	}
}
func TestVeraAccionesErroresPublicos(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		codigo string
	}{
		{copilot.ErrAccionNoEncontrada, 404, "NotFound"}, {copilot.ErrAccionNoPendiente, 409, "ActionNotPending"}, {copilot.ErrAccionExpirada, 410, "ActionExpired"}, {copilot.ErrCodigoAccionRequerido, 403, "SecondFactorRequired"},
		{auth.ErrCodigoInvalido, 400, "OtpInvalid"}, {auth.ErrCodigoExpirado, 400, "OtpExpired"}, {auth.ErrDemasiadosIntentos, 429, "OtpTooManyAttempts"}, {auth.ErrEnfriamientoReenvio, 429, "OtpResendCooldown"}, {auth.ErrEntregaFallida, 503, "OtpDeliveryFailed"},
		{nucleo.ErrSaldoInsuficiente, 422, "InsufficientFunds"}, {nucleo.ErrTopeOperacionExcedido, 422, "OperationLimitExceeded"}, {nucleo.ErrDirectorioSinRespuesta, 503, "DirectoryUnavailable"}, {errors.New("secreto SQL y código OTP"), 503, "AssistantUnavailable"},
	} {
		w := httptest.NewRecorder()
		errorAccionVera(w, tc.err)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.codigo) || strings.Contains(w.Body.String(), "secreto") {
			t.Fatal(w.Code, w.Body)
		}
	}
}
