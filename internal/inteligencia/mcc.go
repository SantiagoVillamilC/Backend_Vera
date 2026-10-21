package inteligencia

import "strings"

// MCCResuelve es la tabla de códigos que nombran una sola categoría. Copiada de
// analisis/cascada_mcc.py, con la fuente original en el comentario del módulo Python.
var MCCResuelve = map[string]Categoria{
	"5411": "alimentos_mercado",
	"5499": "alimentos_mercado",
	"5812": "restaurantes_domicilios",
	"5814": "restaurantes_domicilios",
	"4111": "transporte",
	"4121": "transporte",
	"4131": "transporte",
	"5541": "transporte",
	"5542": "transporte",
	"4814": "vivienda_servicios",
	"4900": "vivienda_servicios",
	"5912": "salud",
	"8011": "salud",
	"8062": "salud",
	"8099": "salud",
	"8211": "educacion",
	"8220": "educacion",
	"8299": "educacion",
	"7832": "entretenimiento",
	"7922": "entretenimiento",
	"5815": "entretenimiento",
	"5816": "entretenimiento",
	"5651": "compras",
	"5691": "compras",
	"5732": "compras",
}

// MCCGenericos son los códigos que mezclan categorías y por eso no resuelven.
var MCCGenericos = map[string]struct{}{
	"5310": {}, "5311": {}, "5964": {}, "5817": {}, "5818": {},
	"5813": {}, "4899": {}, "5942": {},
	"4789": {}, "4829": {}, "5300": {}, "5399": {}, "5969": {}, "5999": {},
	"6010": {}, "6011": {}, "6012": {}, "6051": {}, "7299": {}, "7399": {},
	"7999": {}, "8999": {},
}

// NormalizarMCC recorta espacios y exige exactamente cuatro dígitos.
func NormalizarMCC(mcc string) (string, bool) {
	codigo := strings.TrimSpace(mcc)
	if len(codigo) != 4 {
		return "", false
	}
	for _, r := range codigo {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return codigo, true
}

// CategoriaPorMCC devuelve la categoría del código o nada si no llega, no es válido, es genérico o
// no está en la tabla.
func CategoriaPorMCC(mcc string) (Categoria, bool) {
	codigo, valido := NormalizarMCC(mcc)
	if !valido {
		return "", false
	}
	if _, generico := MCCGenericos[codigo]; generico {
		return "", false
	}
	categoria, existe := MCCResuelve[codigo]
	return categoria, existe
}
