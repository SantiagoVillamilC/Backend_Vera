package avisos

import (
	"math/big"
	"testing"
)

func TestMedianaParEImpar(t *testing.T) {
	if valor := Mediana([]int64{300, 100, 200}); valor.Cmp(Entero(200)) != 0 {
		t.Fatalf("mediana impar = %s", valor.RatString())
	}
	if valor := Mediana([]int64{200, 100}); valor.Cmp(Entero(150)) != 0 {
		t.Fatalf("mediana par = %s, want 150", valor.RatString())
	}
	if valor := Mediana(nil); valor.Sign() != 0 {
		t.Fatalf("mediana vacía = %s", valor.RatString())
	}
}

func TestMediaYMAD(t *testing.T) {
	if valor := Media([]int64{100, 200, 300}); valor.Cmp(Entero(200)) != 0 {
		t.Fatalf("media = %s", valor.RatString())
	}
	if valor := MAD([]int64{100, 200, 300}); valor.Cmp(Entero(100)) != 0 {
		t.Fatalf("MAD = %s, want 100", valor.RatString())
	}
	if valor := MAD([]int64{500, 500, 500}); valor.Sign() != 0 {
		t.Fatalf("MAD constante = %s", valor.RatString())
	}
}

func TestZModificadoExigeMADPositiva(t *testing.T) {
	if _, err := ZModificado(Entero(10), Entero(0), Entero(0), Rat("0.6745")); err == nil {
		t.Fatal("se esperaba error con MAD cero")
	}
	z, err := ZModificado(Entero(10), Entero(0), Entero(2), Rat("0.6745"))
	if err != nil {
		t.Fatalf("z: %v", err)
	}
	esperado := Rat("3.3725")
	if z.Cmp(esperado) != 0 {
		t.Fatalf("z = %s, want %s", z.RatString(), esperado.RatString())
	}
}

func TestRedondeoMitadAPar(t *testing.T) {
	casos := []struct {
		valor    *big.Rat
		esperado int64
	}{
		{Fraccion(1, 2), 0},
		{Fraccion(3, 2), 2},
		{Fraccion(5, 2), 2},
		{Fraccion(7, 2), 4},
		{Fraccion(-1, 2), 0},
		{Fraccion(-3, 2), -2},
		{Fraccion(1, 3), 0},
		{Fraccion(2, 3), 1},
	}
	for _, caso := range casos {
		if obtenido := RedondearMitadAPar(caso.valor); obtenido != caso.esperado {
			t.Fatalf("redondeo de %s = %d, want %d", caso.valor.RatString(), obtenido, caso.esperado)
		}
	}
}

func TestLaComparacionEsExacta(t *testing.T) {
	// 0,6 por 1 más 0,4 por 0,5 es exactamente 0,8 aquí, mientras que en float64 da 0,7999999999999999.
	producto := new(big.Rat).Mul(Rat("0.6"), Entero(1))
	producto.Add(producto, new(big.Rat).Mul(Rat("0.4"), Rat("0.5")))
	if producto.Cmp(Rat("0.8")) != 0 {
		t.Fatalf("0,6 más 0,4 no da 0,8 exacto: %s", producto.RatString())
	}
}

func TestLineaBaseDe(t *testing.T) {
	valores := []int64{100, 200, 300}
	if valor, _ := LineaBaseDe(valores, LineaMediana); valor.Cmp(Entero(200)) != 0 {
		t.Fatalf("mediana = %s", valor.RatString())
	}
	if valor, _ := LineaBaseDe(valores, LineaMedia); valor.Cmp(Entero(200)) != 0 {
		t.Fatalf("media = %s", valor.RatString())
	}
	if _, err := LineaBaseDe(valores, "otra"); err == nil {
		t.Fatal("se esperaba error con una línea base desconocida")
	}
}
