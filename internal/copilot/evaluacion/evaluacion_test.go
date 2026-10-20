package evaluacion

import (
	"encoding/json"
	"testing"
)

func TestCasosDeterministas(t *testing.T) {
	c, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	for _, caso := range c.Casos {
		t.Run(caso.ID, func(t *testing.T) {
			r, err := EjecutarCaso(t.Context(), c.FechaReferencia, caso)
			if err != nil {
				t.Fatal(err)
			}
			if !r.Aprobado {
				t.Fatalf("restricciones incumplidas: %v", r.Fallos)
			}
		})
	}
}

func TestOraculoDetectaRegresiones(t *testing.T) {
	conjunto, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	for nombre, alterar := range map[string]func(*Caso){
		"funcion equivocada":         func(c *Caso) { c.Guion[0].Llamadas[0].Nombre = "consultar_limites" },
		"argumentos equivocados":     func(c *Caso) { c.Guion[0].Llamadas[0].Argumentos = json.RawMessage(`{"usuario_id":"ajeno"}`) },
		"cifra inventada":            func(c *Caso) { c.Guion[1].Texto = "Disponible 999999.00 COP." },
		"codigo esperado incorrecto": func(c *Caso) { c.Esperadas[0].Codigo = "NotFound" },
		"texto esperado ausente":     func(c *Caso) { c.Restricciones.Contiene = []string{"esto no aparece"} },
		"accion inventada":           func(c *Caso) { c.Restricciones.Accion = true },
	} {
		t.Run(nombre, func(t *testing.T) {
			datos, _ := json.Marshal(conjunto.Casos[0])
			var caso Caso
			if err := json.Unmarshal(datos, &caso); err != nil {
				t.Fatal(err)
			}
			alterar(&caso)
			r, err := EjecutarCaso(t.Context(), conjunto.FechaReferencia, caso)
			if err != nil || r.Aprobado {
				t.Fatalf("regresión aceptada: %+v %v", r, err)
			}
		})
	}
}

func TestMontosSinFlotantesYSinDatosInventados(t *testing.T) {
	datos := []json.RawMessage{json.RawMessage(`{"datos":{"saldo":{"centavos":"9007199254740993","pesos":"90071992547409.93 COP"},"negativo":{"centavos":"-1","pesos":"-0.01 COP"}}}`)}
	for texto, correcto := range map[string]bool{
		"Tu saldo es 90071992547409.93 COP.":   true,
		"Tu saldo es $90.071.992.547.409,93.":  true,
		"Tu saldo es $90,071,992,547,409.93.":  true,
		"Tu saldo es $90,071,992,547,409.92.":  false,
		"Tu saldo es $90,071,992,547,40.93.":   false,
		"Tu saldo es $90.071,992.547,409.93.":  false,
		"Tu saldo es 90071992547409.92 COP.":   false,
		"Tu saldo es 9223372036854775808 COP.": false,
		"Ajuste -0.01 COP.":                    true,
		"Saldo 0.01 COP.":                      false,
	} {
		t.Run(texto, func(t *testing.T) {
			if got := montosVerificables(texto, datos); got != correcto {
				t.Fatalf("verificable=%v esperado=%v", got, correcto)
			}
		})
	}
	if montosVerificables("Disponible 120000.00 COP.", nil) {
		t.Fatal("aceptó saldo sin función")
	}
}
