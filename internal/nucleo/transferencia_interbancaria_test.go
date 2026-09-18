package nucleo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

// HU-NF-10, criterios 1 y 3: llave, tipo, monto y clave son obligatorios y la moneda extraña se rechaza.
func TestCrearTransferenciaInterbancariaRequestValidaLosCampos(t *testing.T) {
	t.Parallel()

	const valido = `{"tipo_llave":"celular","llave":"3001234567","monto":"1000","moneda":"COP","idempotency_key":"k-1"}`
	casos := map[string]struct {
		cuerpo string
		valido bool
	}{
		"completo":               {cuerpo: valido, valido: true},
		"sin tipo de llave":      {cuerpo: `{"llave":"3001234567","monto":"1000","idempotency_key":"k-1"}`, valido: false},
		"sin llave":              {cuerpo: `{"tipo_llave":"celular","monto":"1000","idempotency_key":"k-1"}`, valido: false},
		"sin monto":              {cuerpo: `{"tipo_llave":"celular","llave":"3001234567","idempotency_key":"k-1"}`, valido: false},
		"sin idempotency":        {cuerpo: `{"tipo_llave":"celular","llave":"3001234567","monto":"1000"}`, valido: false},
		"monto cero":             {cuerpo: `{"tipo_llave":"celular","llave":"3001234567","monto":"0","idempotency_key":"k-1"}`, valido: false},
		"monto negativo":         {cuerpo: `{"tipo_llave":"celular","llave":"3001234567","monto":"-5","idempotency_key":"k-1"}`, valido: false},
		"monto con decimales":    {cuerpo: `{"tipo_llave":"celular","llave":"3001234567","monto":"12.50","idempotency_key":"k-1"}`, valido: false},
		"monto como numero json": {cuerpo: `{"tipo_llave":"celular","llave":"3001234567","monto":1000,"idempotency_key":"k-1"}`, valido: false},
		"moneda distinta de COP": {cuerpo: `{"tipo_llave":"celular","llave":"3001234567","monto":"1000","moneda":"USD","idempotency_key":"k-1"}`, valido: false},
		"moneda vacia usa COP":   {cuerpo: `{"tipo_llave":"celular","llave":"3001234567","monto":"1000","moneda":"","idempotency_key":"k-1"}`, valido: true},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			var req crearTransferenciaInterbancariaRequest
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/transferencias/interbancarias", strings.NewReader(caso.cuerpo))
			if got := api.DecodeAndValidate(rec, r, &req); got != caso.valido {
				t.Fatalf("valido = %v (status %d, cuerpo %q), quiero %v", got, rec.Code, rec.Body.String(), caso.valido)
			}
		})
	}
}

// HU-NF-10, criterios 1 a 5: cada rechazo del envío por llave tiene su status y su código estables.
func TestEscribirErrorDeEnvioPorLlaveMapeaCadaRechazo(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		err    error
		status int
		code   string
	}{
		"llave invalida":            {err: ErrLlaveInvalida, status: http.StatusUnprocessableEntity, code: "ValidationError"},
		"monto invalido":            {err: ErrMontoInvalido, status: http.StatusUnprocessableEntity, code: "ValidationError"},
		"llave no encontrada":       {err: ErrLlaveNoEncontrada, status: http.StatusNotFound, code: "RecipientNotFound"},
		"directorio sin respuesta":  {err: ErrDirectorioSinRespuesta, status: http.StatusServiceUnavailable, code: "DirectoryUnavailable"},
		"directorio no disponible":  {err: ErrDirectorioNoDisponible, status: http.StatusServiceUnavailable, code: "DirectoryUnavailable"},
		"saldo insuficiente":        {err: ErrSaldoInsuficiente, status: http.StatusUnprocessableEntity, code: "InsufficientFunds"},
		"tope excedido":             {err: ErrTopeOperacionExcedido, status: http.StatusUnprocessableEntity, code: "OperationLimitExceeded"},
		"cupo mensual excedido":     {err: ErrCupoMensualExcedido, status: http.StatusUnprocessableEntity, code: "MonthlyQuotaExceeded"},
		"idempotencia en conflicto": {err: ErrIdempotenciaEnConflicto, status: http.StatusConflict, code: "IdempotencyConflict"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			escribirErrorDeEnvioPorLlave(rec, httptest.NewRequest(http.MethodPost, "/transferencias/interbancarias", nil), caso.err)
			if rec.Code != caso.status {
				t.Errorf("status = %d, quiero %d", rec.Code, caso.status)
			}
			var envelope api.Envelope[json.RawMessage]
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decodificar el envelope: %v", err)
			}
			if envelope.Error == nil || envelope.Error.Code != caso.code {
				t.Errorf("código = %+v, quiero %q", envelope.Error, caso.code)
			}
		})
	}
}

// HU-NF-10, criterios 1 y 3: el monto y la llave se validan antes de abrir cualquier transacción.
func TestEjecutarEnvioPorLlaveValidaAntesDeLaTransaccion(t *testing.T) {
	t.Parallel()

	directorio := &directorioFalso{fn: func(context.Context, Llave) (ResolucionDirectorio, error) {
		return ResolucionDirectorio{Entidad: "Banco Vecino", Producto: "Cuenta de ahorros"}, nil
	}}

	base := SolicitudEnvioPorLlave{
		UsuarioID:      uuid.NewString(),
		CuentaOrigen:   "pasivo:usuario:x:disponible",
		TipoLlave:      TipoLlaveCelular,
		Llave:          "3001234567",
		Monto:          Centavos(1000),
		Moneda:         MonedaCOP,
		IdempotencyKey: "k-1",
	}
	casos := map[string]struct {
		servicio  *Servicio
		solicitud SolicitudEnvioPorLlave
		esperado  error
	}{
		"monto no positivo": {
			servicio:  NewServicio(nil, nil, nil, nil, nil, ConDirectorio(directorio)),
			solicitud: func() SolicitudEnvioPorLlave { s := base; s.Monto = 0; return s }(),
			esperado:  ErrMontoInvalido,
		},
		"tipo desconocido": {
			servicio:  NewServicio(nil, nil, nil, nil, nil, ConDirectorio(directorio)),
			solicitud: func() SolicitudEnvioPorLlave { s := base; s.TipoLlave = "nip"; return s }(),
			esperado:  ErrLlaveInvalida,
		},
		"sin directorio": {
			servicio:  NewServicio(nil, LectorNivelKYCFijo{}, nil, nil, nil),
			solicitud: base,
			esperado:  ErrDirectorioNoDisponible,
		},
		"sin kyc": {
			servicio:  NewServicio(nil, nil, nil, nil, nil, ConDirectorio(directorio)),
			solicitud: base,
			esperado:  ErrKYCNoDisponible,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			_, err := caso.servicio.ejecutarEnvioPorLlave(t.Context(), caso.solicitud)
			if !errors.Is(err, caso.esperado) {
				t.Fatalf("err = %v, quiero %v", err, caso.esperado)
			}
		})
	}
}

// HU-NF-10, criterio 11: la respuesta dice en proceso con el id, el estado y las fechas, nunca entregado.
func TestRespuestaDeEnvioPorLlaveLlevaElEstadoEnProceso(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	creada := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	actualizada := creada.Add(time.Second)
	respuesta := respuestaDeEnvioPorLlave(Transferencia{
		ID:            id,
		LlaveDestino:  "3001234567",
		Monto:         Centavos(1000),
		Moneda:        MonedaCOP,
		Estado:        EstadoTransferenciaReservada,
		CreadaEn:      creada,
		ActualizadaEn: actualizada,
	}, TipoLlaveCelular)

	if respuesta.ID != id.String() {
		t.Errorf("ID = %q, quiero %q", respuesta.ID, id)
	}
	if respuesta.TipoLlave != TipoLlaveCelular || respuesta.Llave != "3001234567" {
		t.Errorf("llave = %q/%q, quiero celular/3001234567", respuesta.TipoLlave, respuesta.Llave)
	}
	if respuesta.Estado != EstadoTransferenciaReservada {
		t.Errorf("Estado = %q, quiero %q: la petición no afirma entrega", respuesta.Estado, EstadoTransferenciaReservada)
	}
	if !respuesta.CreadaEn.Equal(creada) || !respuesta.ActualizadaEn.Equal(actualizada) {
		t.Errorf("fechas = %s/%s, quiero %s/%s", respuesta.CreadaEn, respuesta.ActualizadaEn, creada, actualizada)
	}
}
