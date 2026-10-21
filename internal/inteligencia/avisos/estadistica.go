package avisos

import (
	"errors"
	"math/big"
	"sort"
)

// Mediana devuelve la mediana exacta. Con cantidad par es el promedio de los dos centrales.
func Mediana(valores []int64) *big.Rat {
	if len(valores) == 0 {
		return Entero(0)
	}
	ordenados := append([]int64(nil), valores...)
	sort.Slice(ordenados, func(i, j int) bool { return ordenados[i] < ordenados[j] })
	medio := len(ordenados) / 2
	if len(ordenados)%2 == 1 {
		return Entero(ordenados[medio])
	}
	suma := new(big.Rat).Add(Entero(ordenados[medio-1]), Entero(ordenados[medio]))
	return suma.Quo(suma, Entero(2))
}

// Media devuelve el promedio exacto.
func Media(valores []int64) *big.Rat {
	if len(valores) == 0 {
		return Entero(0)
	}
	suma := Entero(0)
	for _, valor := range valores {
		suma = new(big.Rat).Add(suma, Entero(valor))
	}
	return new(big.Rat).Quo(suma, Entero(int64(len(valores))))
}

// MAD es la mediana de las desviaciones absolutas respecto de la mediana.
func MAD(valores []int64) *big.Rat {
	centro := Mediana(valores)
	desviaciones := make([]*big.Rat, 0, len(valores))
	for _, valor := range valores {
		diferencia := new(big.Rat).Sub(Entero(valor), centro)
		desviaciones = append(desviaciones, Abs(diferencia))
	}
	return MedianaDeRat(desviaciones)
}

// MediaDeRat es el promedio de racionales ya calculados.
func MediaDeRat(valores []*big.Rat) *big.Rat {
	if len(valores) == 0 {
		return Entero(0)
	}
	suma := Entero(0)
	for _, valor := range valores {
		suma = new(big.Rat).Add(suma, valor)
	}
	return suma.Quo(suma, Entero(int64(len(valores))))
}

// MADDeRat es la mediana de las desviaciones absolutas de racionales ya calculados.
func MADDeRat(valores []*big.Rat) *big.Rat {
	centro := MedianaDeRat(valores)
	desviaciones := make([]*big.Rat, 0, len(valores))
	for _, valor := range valores {
		desviaciones = append(desviaciones, Abs(new(big.Rat).Sub(valor, centro)))
	}
	return MedianaDeRat(desviaciones)
}

// MedianaDeRat es la mediana de racionales ya calculados.
func MedianaDeRat(valores []*big.Rat) *big.Rat {
	if len(valores) == 0 {
		return Entero(0)
	}
	ordenados := append([]*big.Rat(nil), valores...)
	sort.Slice(ordenados, func(i, j int) bool { return ordenados[i].Cmp(ordenados[j]) < 0 })
	medio := len(ordenados) / 2
	if len(ordenados)%2 == 1 {
		return new(big.Rat).Set(ordenados[medio])
	}
	suma := new(big.Rat).Add(ordenados[medio-1], ordenados[medio])
	return suma.Quo(suma, Entero(2))
}

// ZModificado es el z de Iglewicz y Hoaglin: factor por (x menos centro) entre la MAD.
func ZModificado(x, centro, desviacion, factor *big.Rat) (*big.Rat, error) {
	if desviacion == nil || desviacion.Sign() <= 0 {
		return nil, errors.New("avisos: la MAD debe ser positiva para calcular el z modificado")
	}
	diferencia := new(big.Rat).Sub(x, centro)
	producto := new(big.Rat).Mul(factor, diferencia)
	return new(big.Rat).Quo(producto, desviacion), nil
}

// LineaBaseDe devuelve la mediana o la media de los valores.
func LineaBaseDe(valores []int64, base LineaBase) (*big.Rat, error) {
	switch base {
	case LineaMediana:
		return Mediana(valores), nil
	case LineaMedia:
		return Media(valores), nil
	default:
		return nil, errors.New("avisos: línea base desconocida")
	}
}

// Abs devuelve el valor absoluto de un racional, en un receptor nuevo.
func Abs(valor *big.Rat) *big.Rat {
	if valor.Sign() < 0 {
		return new(big.Rat).Neg(valor)
	}
	return new(big.Rat).Set(valor)
}

// RedondearMitadAPar redondea un racional al entero más cercano y, en el empate exacto, al par. Es
// el redondeo de `round` de Python, que la referencia usa para las cifras que se muestran.
func RedondearMitadAPar(valor *big.Rat) int64 {
	if valor == nil {
		return 0
	}
	num, den := valor.Num(), valor.Denom()
	if num.Sign() < 0 {
		return -RedondearMitadAPar(new(big.Rat).Neg(valor))
	}
	cociente := new(big.Int).Quo(num, den)
	resto := new(big.Int).Rem(num, den)
	doble := new(big.Int).Lsh(resto, 1)
	switch doble.Cmp(den) {
	case -1:
		return cociente.Int64()
	case 1:
		return new(big.Int).Add(cociente, big.NewInt(1)).Int64()
	default:
		if cociente.Bit(0) == 0 {
			return cociente.Int64()
		}
		return new(big.Int).Add(cociente, big.NewInt(1)).Int64()
	}
}

// Fraccion devuelve el racional de una fracción de enteros.
func Fraccion(numerador, denominador int64) *big.Rat {
	if denominador == 0 {
		return Entero(0)
	}
	return new(big.Rat).SetFrac(big.NewInt(numerador), big.NewInt(denominador))
}
