package avisos

import (
	"regexp"
	"testing"
)

// TestAccionCodigoDeCadaTipo fija el código estable de cada tipo. Cambiar uno es cambiar el contrato
// con la app, así que la tabla va escrita a mano y no se deriva del mapa de producción.
func TestAccionCodigoDeCadaTipo(t *testing.T) {
	t.Parallel()
	esperado := map[Tipo]string{
		PagoNoAlcanza:         "revisar_pagos",
		CaidaIngreso:          "revisar_ingreso",
		CobroNuevo:            "revisar_cobro",
		PresupuestoAnticipado: "revisar_presupuesto",
		CategoriaSobreMedia:   "revisar_categoria",
	}
	if len(esperado) != len(Tipos) {
		t.Fatalf("la tabla cubre %d tipos y hay %d", len(esperado), len(Tipos))
	}
	for tipo, codigo := range esperado {
		if obtenido := AccionCodigoDe(tipo); obtenido != codigo {
			t.Errorf("AccionCodigoDe(%q) = %q, quiero %q", tipo, obtenido, codigo)
		}
	}
	if AccionCodigoDe("inventado") != "" {
		t.Fatal("un tipo desconocido no debería inventar código")
	}
}

// TestAccionCodigosSonSnakeCaseUnicosYUnoPorTipo protege la forma del enum: snake_case, sin
// repeticiones y con un código para cada tipo que existe.
func TestAccionCodigosSonSnakeCaseUnicosYUnoPorTipo(t *testing.T) {
	t.Parallel()
	snake := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	vistos := map[string]Tipo{}
	for _, tipo := range Tipos {
		codigo := AccionCodigoDe(tipo)
		if !snake.MatchString(codigo) {
			t.Errorf("%q: el código %q no es snake_case", tipo, codigo)
		}
		if previo, repetido := vistos[codigo]; repetido {
			t.Errorf("el código %q lo usan %q y %q", codigo, previo, tipo)
		}
		vistos[codigo] = tipo
	}
	codigos := AccionCodigos()
	if len(codigos) != len(Tipos) {
		t.Fatalf("AccionCodigos() = %v, quiero %d valores", codigos, len(Tipos))
	}
	for _, codigo := range codigos {
		if _, existe := vistos[codigo]; !existe {
			t.Errorf("AccionCodigos() trae %q y ningún tipo lo produce", codigo)
		}
	}
}

// TestAccionCodigosNoCambiaLaEtiquetaVisible comprueba que agregar el código no tocó el texto.
func TestAccionCodigosNoCambiaLaEtiquetaVisible(t *testing.T) {
	t.Parallel()
	etiquetas := map[Tipo]string{
		PagoNoAlcanza:         "Ver los pagos que vencen",
		CaidaIngreso:          "Ver mis ingresos",
		CobroNuevo:            "Ver el cobro y su próximo vencimiento",
		PresupuestoAnticipado: "Ver los gastos de este mes",
		CategoriaSobreMedia:   "Ver los movimientos de esta categoría",
	}
	for tipo, etiqueta := range etiquetas {
		if AccionDe(tipo) != etiqueta {
			t.Errorf("AccionDe(%q) = %q, quiero %q", tipo, AccionDe(tipo), etiqueta)
		}
	}
}
