package inteligencia

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

func TestCursorAvisoConSeparadores(t *testing.T) {
	c := CursorDeAviso{1, 500, uuid.New(), "clave|con|separadores"}
	s := CodificarCursorDeAviso(c)
	actual, e := DecodificarCursorDeAviso(s)
	if e != nil || actual != c {
		t.Fatal(actual, e)
	}
	for _, s := range []string{"", "!", CodificarCursorDeAviso(CursorDeAviso{0, -1, uuid.Nil, ""})} {
		if _, e := DecodificarCursorDeAviso(s); e == nil {
			t.Fatal("cursor inválido aceptado")
		}
	}
}

func TestAvisos404UniformeYUUID(t *testing.T) {
	h := NuevoHandler(&servicioDeLecturaFalso{avisoErr: ErrAvisoNoEncontrado})
	var cuerpo string
	for _, ruta := range []func(*httptest.ResponseRecorder, string){
		func(w *httptest.ResponseRecorder, id string) {
			h.DetalleAvisoInteligencia(w, peticionConIdentidad(t, "/"), id)
		},
		func(w *httptest.ResponseRecorder, id string) {
			h.DescartarAvisoInteligencia(w, peticionConIdentidad(t, "/"), id)
		},
	} {
		w := httptest.NewRecorder()
		ruta(w, uuid.NewString())
		if w.Code != 404 {
			t.Fatal(w.Code)
		}
		if cuerpo != "" && cuerpo != w.Body.String() {
			t.Fatal("404 distintos")
		}
		cuerpo = w.Body.String()
		w = httptest.NewRecorder()
		ruta(w, "incorrecto")
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"field":"id"`) {
			t.Fatal(w.Body)
		}
	}
}

func TestPaginaAvisosParametros(t *testing.T) {
	for _, valor := range []string{"0", "51", "abc"} {
		w := httptest.NewRecorder()
		NuevoHandler(&servicioDeLecturaFalso{}).ListarAvisosInteligencia(w, peticionConIdentidad(t, "/"), api.ListarAvisosInteligenciaParams{Limite: &valor})
		if w.Code != 400 {
			t.Fatal(valor, w.Code)
		}
	}
	estado := "otro"
	w := httptest.NewRecorder()
	NuevoHandler(&servicioDeLecturaFalso{}).ListarAvisosInteligencia(w, peticionConIdentidad(t, "/"), api.ListarAvisosInteligenciaParams{Estado: &estado})
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}

// Un descarte anterior al alta es un conflicto con el estado del aviso, no un fallo del servidor.
func TestDescartarAvisoConAltaFuturaResponde409(t *testing.T) {
	envuelto := fmt.Errorf("inteligencia: descartar: %w", ErrDescarteAnteriorAlAlta)
	h := NuevoHandler(&servicioDeLecturaFalso{avisoErr: envuelto})
	w := httptest.NewRecorder()
	h.DescartarAvisoInteligencia(w, peticionConIdentidad(t, "/"), uuid.NewString())
	if w.Code != 409 {
		t.Fatalf("status = %d, want 409: %s", w.Code, w.Body)
	}
	for _, trozo := range []string{`"success":false`, `"code":"DescarteAnteriorAlAlta"`, `"data":null`} {
		if !strings.Contains(w.Body.String(), trozo) {
			t.Fatalf("falta %s en %s", trozo, w.Body)
		}
	}
}
