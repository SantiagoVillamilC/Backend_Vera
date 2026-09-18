package rielsim

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// respuestaJSON es el cuerpo que el simulador devuelve en el contrato del cable.
func respuestaJSON(id uuid.UUID, resultado nucleo.ResultadoRiel) string {
	cuerpo, _ := json.Marshal(nucleo.RespuestaRiel{
		TransferenciaID: id,
		Resultado:       resultado,
		OcurridoEn:      time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC),
	})
	return string(cuerpo)
}

func TestEnviarEntregaLaOrdenCompletaYLeeLaRespuesta(t *testing.T) {
	t.Parallel()

	orden := nucleo.OrdenRiel{
		TransferenciaID: uuid.New(),
		IdempotencyKey:  "k-1",
		Llave:           "3001234567",
		Monto:           nucleo.Centavos(1000000),
		Moneda:          "COP",
	}

	var crudo map[string]json.RawMessage
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/transferencias" {
			t.Errorf("petición = %s %s, quiero POST /transferencias", r.Method, r.URL.Path)
		}
		datos, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(datos, &crudo); err != nil {
			t.Errorf("cuerpo no es JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respuestaJSON(orden.TransferenciaID, nucleo.ResultadoConfirmado))
	}))
	t.Cleanup(servidor.Close)

	riel := NuevoRiel(servidor.URL, nil)
	respuesta, err := riel.Enviar(t.Context(), orden)
	if err != nil {
		t.Fatalf("Enviar: %v", err)
	}
	if respuesta.TransferenciaID != orden.TransferenciaID || respuesta.Resultado != nucleo.ResultadoConfirmado {
		t.Errorf("respuesta = %+v, quiero confirmada de %s", respuesta, orden.TransferenciaID)
	}

	if got := string(crudo["monto_centavos"]); got != `"1000000"` {
		t.Errorf("monto_centavos = %s, quiero \"1000000\" como cadena", got)
	}
	if got := string(crudo["transferencia_id"]); got != `"`+orden.TransferenciaID.String()+`"` {
		t.Errorf("transferencia_id = %s, quiero %s", got, orden.TransferenciaID)
	}
	if got := string(crudo["idempotency_key"]); got != `"k-1"` {
		t.Errorf("idempotency_key = %s, quiero \"k-1\"", got)
	}
	if got := string(crudo["llave"]); got != `"3001234567"` {
		t.Errorf("llave = %s, quiero \"3001234567\"", got)
	}
	if got := string(crudo["moneda"]); got != `"COP"` {
		t.Errorf("moneda = %s, quiero \"COP\"", got)
	}
}

func TestConsultarEstadoPideLaRutaYLeeLaRespuesta(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/transferencias/"+id.String() {
			t.Errorf("petición = %s %s, quiero GET /transferencias/%s", r.Method, r.URL.Path, id)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respuestaJSON(id, nucleo.ResultadoRechazado))
	}))
	t.Cleanup(servidor.Close)

	respuesta, err := NuevoRiel(servidor.URL, nil).ConsultarEstado(t.Context(), id)
	if err != nil {
		t.Fatalf("ConsultarEstado: %v", err)
	}
	if respuesta.Resultado != nucleo.ResultadoRechazado {
		t.Errorf("resultado = %q, quiero rechazada", respuesta.Resultado)
	}
}

func TestConsultarEstadoTraduceEl404ASinRegistro(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no existe", http.StatusNotFound)
	}))
	t.Cleanup(servidor.Close)

	_, err := NuevoRiel(servidor.URL, nil).ConsultarEstado(t.Context(), uuid.New())
	if !errors.Is(err, nucleo.ErrRielSinRegistro) {
		t.Fatalf("error = %v, quiero ErrRielSinRegistro", err)
	}
}

func TestEnviarDevuelveElErrorDeTransporte(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(servidor.Close)

	riel := NuevoRiel(servidor.URL, &http.Client{Timeout: 2 * time.Second})
	if _, err := riel.Enviar(t.Context(), nucleo.OrdenRiel{TransferenciaID: uuid.New()}); err == nil {
		t.Fatal("Enviar con la conexión cortada devolvió nil, quiero error de transporte")
	}
}

func TestLeerRespuestaRechazaElContratoIncompleto(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	tests := map[string]struct {
		cuerpo   string
		esperado uuid.UUID
	}{
		"sin transferencia_id": {cuerpo: `{"resultado":"confirmada","ocurrido_en":"2026-09-17T12:00:00Z"}`, esperado: id},
		"sin resultado":        {cuerpo: `{"transferencia_id":"` + id.String() + `","ocurrido_en":"2026-09-17T12:00:00Z"}`, esperado: id},
		"desenlace raro":       {cuerpo: `{"transferencia_id":"` + id.String() + `","resultado":"quizas","ocurrido_en":"2026-09-17T12:00:00Z"}`, esperado: id},
		"sin ocurrido_en":      {cuerpo: `{"transferencia_id":"` + id.String() + `","resultado":"confirmada"}`, esperado: id},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := leerRespuesta(strings.NewReader(tc.cuerpo), tc.esperado); err == nil {
				t.Error("leerRespuesta aceptó un contrato incompleto")
			}
		})
	}
}

// M2: una respuesta con otro transferencia_id se rechaza aunque el resto del contrato venga completo.
func TestLeerRespuestaRechazaOtroTransferenciaID(t *testing.T) {
	t.Parallel()

	if _, err := leerRespuesta(strings.NewReader(respuestaJSON(uuid.New(), nucleo.ResultadoConfirmado)), uuid.New()); err == nil {
		t.Fatal("leerRespuesta aceptó una respuesta de otra transferencia")
	}
}

// M2: Enviar rechaza la respuesta cruzada que el servidor devuelve con otro id.
func TestEnviarRechazaLaRespuestaDeOtraTransferencia(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respuestaJSON(uuid.New(), nucleo.ResultadoConfirmado))
	}))
	t.Cleanup(servidor.Close)

	_, err := NuevoRiel(servidor.URL, nil).Enviar(t.Context(), nucleo.OrdenRiel{TransferenciaID: uuid.New()})
	if err == nil {
		t.Fatal("Enviar aceptó una respuesta de otra transferencia")
	}
}

// M2: ConsultarEstado rechaza la respuesta cruzada que el servidor devuelve con otro id.
func TestConsultarEstadoRechazaLaRespuestaDeOtraTransferencia(t *testing.T) {
	t.Parallel()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respuestaJSON(uuid.New(), nucleo.ResultadoRechazado))
	}))
	t.Cleanup(servidor.Close)

	if _, err := NuevoRiel(servidor.URL, nil).ConsultarEstado(t.Context(), uuid.New()); err == nil {
		t.Fatal("ConsultarEstado aceptó una respuesta de otra transferencia")
	}
}

// M1: sin cliente explícito, NuevoRiel usa un cliente con plazo propio.
func TestNuevoRielUsaClienteConPlazoPorDefecto(t *testing.T) {
	t.Parallel()

	riel := NuevoRiel("http://127.0.0.1:8098", nil)
	if riel.cliente.Timeout != tiempoEsperaRiel {
		t.Errorf("plazo por defecto = %v, quiero %v", riel.cliente.Timeout, tiempoEsperaRiel)
	}
}
