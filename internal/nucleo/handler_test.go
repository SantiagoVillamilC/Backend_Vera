package nucleo

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// Un Handler sin Servicio es un error de programación: falla al construir, no en la petición.
func TestNewHandlerConServicioNuloFallaAlConstruir(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("NewHandler(nil) no falló")
		}
		err, ok := r.(error)
		if !ok || !errors.Is(err, ErrServicioNulo) {
			t.Fatalf("NewHandler(nil) falló con %v, quiero %v", r, ErrServicioNulo)
		}
		if got := err.Error(); got != "nucleo: NewHandler necesita un Servicio no nulo" {
			t.Fatalf("mensaje = %q, quiero el literal del invariante", got)
		}
		t.Logf("NewHandler(nil) falló como se esperaba: %v", err)
	}()

	NewHandler(nil)
}

// HU-NF-05 Criterio 10: la paginación valida el límite y el cursor, y recorta el límite sobre el tope.
func TestHU_NF_05_Criterio10_PaginacionDeMovimientosValidaLosParametros(t *testing.T) {
	puntero := func(v int64) *int64 { return &v }
	tests := map[string]struct {
		query       string
		wantCursor  *int64
		wantLimite  int
		wantMensaje string
	}{
		"sin parametros usa el limite por defecto": {query: "", wantLimite: limiteHistorialPorDefecto},
		"limite entero valido":                     {query: "?limit=10", wantLimite: 10},
		"limite en el tope":                        {query: "?limit=100", wantLimite: limiteHistorialMaximo},
		"limite sobre el tope se recorta":          {query: "?limit=1000", wantLimite: limiteHistorialMaximo},
		"limite cero se rechaza":                   {query: "?limit=0", wantMensaje: "limit must be a positive integer"},
		"limite negativo se rechaza":               {query: "?limit=-3", wantMensaje: "limit must be a positive integer"},
		"limite no entero se rechaza":              {query: "?limit=abc", wantMensaje: "limit must be a positive integer"},
		"cursor valido":                            {query: "?cursor=42", wantCursor: puntero(42), wantLimite: limiteHistorialPorDefecto},
		"cursor cero se rechaza":                   {query: "?cursor=0", wantMensaje: "cursor must be the anchor entry id of the last movement"},
		"cursor negativo se rechaza":               {query: "?cursor=-1", wantMensaje: "cursor must be the anchor entry id of the last movement"},
		"cursor no entero se rechaza":              {query: "?cursor=abc", wantMensaje: "cursor must be the anchor entry id of the last movement"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/movimientos"+tc.query, nil)
			cursor, limite, mensaje := paginacionDeMovimientos(req)
			if mensaje != tc.wantMensaje {
				t.Fatalf("mensaje = %q, quiero %q", mensaje, tc.wantMensaje)
			}
			if limite != tc.wantLimite {
				t.Errorf("limite = %d, quiero %d", limite, tc.wantLimite)
			}
			if tc.wantCursor == nil {
				if cursor != nil {
					t.Errorf("cursor = %d, quiero nil", *cursor)
				}
				return
			}
			if cursor == nil || *cursor != *tc.wantCursor {
				t.Fatalf("cursor = %v, quiero %d", cursor, *tc.wantCursor)
			}
		})
	}
}

// HU-NF-07 Criterio 17: la señal del custodio y el estado persistido se relacionan en un solo lugar.
func TestEstadoPersistidoTraduceLaSenalDelCustodio(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"aprobado":  EstadoDepositoConfirmada,
		"rechazado": EstadoDepositoRechazada,
	}
	for senal, quiero := range casos {
		if got := estadoPersistido(senal); got != quiero {
			t.Errorf("estadoPersistido(%q) = %q, quiero %q", senal, got, quiero)
		}
	}
}

// HU-NF-07 Criterios 1 y 17: monto y ocurrido_en son obligatorios en la aprobación y opcionales en el rechazo.
func TestConfirmacionValidaMontoYFuenteCondicionales(t *testing.T) {
	t.Parallel()

	const fecha = `"ocurrido_en":"2026-09-16T10:00:00Z"`
	casos := map[string]struct {
		cuerpo string
		valido bool
	}{
		"aprobado sin monto":            {cuerpo: `{"referencia":"dep-x","estado":"aprobado",` + fecha + `}`, valido: false},
		"aprobado sin ocurrido_en":      {cuerpo: `{"referencia":"dep-x","estado":"aprobado","monto":"1000"}`, valido: false},
		"aprobado con monto cero":       {cuerpo: `{"referencia":"dep-x","estado":"aprobado","monto":"0",` + fecha + `}`, valido: false},
		"aprobado completo":             {cuerpo: `{"referencia":"dep-x","estado":"aprobado","monto":"1000",` + fecha + `}`, valido: true},
		"rechazado sin monto ni fuente": {cuerpo: `{"referencia":"dep-x","estado":"rechazado"}`, valido: true},
		"rechazado con monto cero":      {cuerpo: `{"referencia":"dep-x","estado":"rechazado","monto":"0"}`, valido: true},
	}
	for nombre, tc := range casos {
		t.Run(nombre, func(t *testing.T) {
			var req confirmarDepositoRequest
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/depositos/confirmaciones", strings.NewReader(tc.cuerpo))
			if got := api.DecodeAndValidate(rec, r, &req); got != tc.valido {
				t.Fatalf("valido = %v (status %d, cuerpo %q), quiero %v", got, rec.Code, rec.Body.String(), tc.valido)
			}
		})
	}
}
