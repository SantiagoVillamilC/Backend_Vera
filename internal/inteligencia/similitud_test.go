package inteligencia

import (
	"reflect"
	"testing"
)

func TestPalabrasConservaLaEnieYDescartaGenericos(t *testing.T) {
	casos := map[string][]string{
		"PESCAÑA ALFA":      {"PESCAÑA", "ALFA"},
		"tienda sas":        nil,
		"TIENDA X":          {"X"},
		"ALMACENES TIENDAS": {"ALMACENES", "TIENDAS"},
		"ÑANDU":             {"ÑANDU"},
		"UNO-DOS_TRES":      {"UNO", "DOS", "TRES"},
	}
	for texto, esperadas := range casos {
		if obtenidas := palabras(texto); !reflect.DeepEqual(obtenidas, esperadas) {
			t.Fatalf("palabras(%q) = %v, want %v", texto, obtenidas, esperadas)
		}
	}
}

func TestQuitarGenericosRespetaLaFronteraDePalabra(t *testing.T) {
	casos := map[string]string{
		"TIENDA X":         "X",
		"ALMACEN CASA":     "CASA",
		"SAÑA":             "SAÑA",
		"ALMACENES":        "ALMACENES",
		"A   B":            "A B",
		"SUCURSAL  CENTRO": "CENTRO",
	}
	for texto, esperado := range casos {
		if obtenido := quitarGenericos(texto); obtenido != esperado {
			t.Fatalf("quitarGenericos(%q) = %q, want %q", texto, obtenido, esperado)
		}
	}
}

func TestTrigramasConRellenoYRepetidos(t *testing.T) {
	if obtenidos := trigramas("ab", 3); !reflect.DeepEqual(obtenidos, []string{" ab", "ab "}) {
		t.Fatalf("trigramas = %v", obtenidos)
	}
	if obtenidos := trigramas("aaa", 3); len(obtenidos) != 3 {
		t.Fatalf("trigramas de aaa = %v, want 3 con repetidos", obtenidos)
	}
	obtenidos := trigramas("AÑ", 3)
	if !reflect.DeepEqual(obtenidos, []string{" AÑ", "AÑ "}) {
		t.Fatalf("trigramas con Ñ = %v", obtenidos)
	}
	if len(trigramas("", 0)) != 0 {
		t.Fatalf("trigramas con n=0 debe ser vacío")
	}
}

func TestJaroYWinkler(t *testing.T) {
	if valor := jaro("", "ALFA"); valor != 0 {
		t.Fatalf("jaro con vacío = %v", valor)
	}
	if valor := jaro("ALFA", "ALFA"); valor != 1 {
		t.Fatalf("jaro igual = %v", valor)
	}
	if valor := jaro("MARTHA", "MARHTA"); difiere(valor, 0.9444444444444445) {
		t.Fatalf("jaro = %v", valor)
	}
	if valor := jaroWinkler("MARTHA", "MARHTA"); difiere(valor, 0.9611111111111111) {
		t.Fatalf("jaroWinkler = %v", valor)
	}
	if valor := jaroWinkler("DWAYNE", "DUANE"); difiere(valor, 0.84) {
		t.Fatalf("jaroWinkler con prefijo corto = %v", valor)
	}
	if valor := jaroWinkler("ÑANDU", "ÑANDU"); valor != 1 {
		t.Fatalf("jaroWinkler igual = %v", valor)
	}
}

func difiere(a, b float64) bool {
	diferencia := a - b
	if diferencia < 0 {
		diferencia = -diferencia
	}
	return diferencia > 1e-12
}
