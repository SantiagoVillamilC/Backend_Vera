package copilot

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCursorHistorialAmbitoYFormato(t *testing.T) {
	c := cursorHistorial{Version: 1, Tipo: "conversaciones", Posicion: time.Now().UnixMicro(), ID: uuid.New(), Ambito: "ambito-propio"}
	token := codificarCursorHistorial(c)
	got, err := leerCursorHistorial(token, c.Tipo, c.Ambito)
	if err != nil || got != c {
		t.Fatalf("cursor: %+v %v", got, err)
	}
	for nombre, dato := range map[string]struct{ token, tipo, ambito string }{
		"roto": {"?", c.Tipo, c.Ambito}, "ajeno": {token, c.Tipo, "otro"},
		"otra lista": {token, "mensajes", c.Ambito}, "vacio explicito": {"", c.Tipo, c.Ambito},
	} {
		t.Run(nombre, func(t *testing.T) {
			if _, err := leerCursorHistorial(dato.token, dato.tipo, dato.ambito); !errors.Is(err, ErrPaginaHistorial) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
