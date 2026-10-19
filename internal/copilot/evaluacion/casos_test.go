package evaluacion

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestConjuntoInvalidoNuncaProduceInformeAprobado(t *testing.T) {
	for nombre, alterar := range map[string]func(*Conjunto){
		"menos de cuarenta": func(c *Conjunto) { c.Casos = c.Casos[:39] },
		"duplicado":         func(c *Conjunto) { c.Casos[1].ID = c.Casos[0].ID },
		"sin oraculo":       func(c *Conjunto) { c.Casos[0].Restricciones = Restricciones{} },
		"sin expectativas":  func(c *Conjunto) { c.Casos[0].Esperadas = nil },
		"sin categoria": func(c *Conjunto) {
			for i := range c.Casos {
				c.Casos[i].Categoria = "consulta"
			}
		},
		"sin guion":                       func(c *Conjunto) { c.Casos[0].Guion = nil },
		"error desconocido":               func(c *Conjunto) { c.Casos[0].Restricciones.Error = "error-tipeado-mal" },
		"paso ambiguo":                    func(c *Conjunto) { c.Casos[0].Guion[0].Texto = "junto con función" },
		"perfil desconocido":              func(c *Conjunto) { c.Casos[0].Perfil = "error-tipeado-mal" },
		"herramienta omitida desconocida": func(c *Conjunto) { c.Casos[0].OmitirHerramientas = []string{"error-tipeado-mal"} },
		"fallo de herramienta desconocido": func(c *Conjunto) {
			c.Casos[0].ErroresHerramienta = map[string]string{"consultar_saldo": "error-tipeado-mal"}
		},
	} {
		t.Run(nombre, func(t *testing.T) {
			c, err := Cargar()
			if err != nil {
				t.Fatal(err)
			}
			alterar(&c)
			if _, err := EjecutarSimulados(t.Context(), c); err == nil {
				t.Fatal("conjunto inválido aceptado")
			}
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Leer(bytes.NewReader(raw)); err == nil {
				t.Fatal("JSON inválido aceptado")
			}
		})
	}
	for _, raw := range []string{`{}`, `{"desconocido":true}`, string(casosVersionados) + `{}`} {
		if _, err := Leer(strings.NewReader(raw)); err == nil {
			t.Fatal("entrada inválida aceptada")
		}
	}
}

func TestCargarNoComparteMemoria(t *testing.T) {
	a, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	a.Casos[0].Esperadas[0].Argumentos[0] = '['
	b, err := Cargar()
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(b.Casos[0].Esperadas[0].Argumentos) {
		t.Fatal("conjunto compartió buffers mutables")
	}
}
