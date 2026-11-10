package inteligencia

import (
	"math/rand"
	"strings"
	"testing"
)

func TestNormalizarNombre(t *testing.T) {
	casos := []struct {
		entrada string
		querer  string
	}{
		{"Farmacia  La  ñ", "FARMACIA-LA-Ñ"},
		{"Éxito Ñandú", "EXITO-ÑANDU"},
		{"O'Reilly & Cía.", "OREILLY-CIA"},
		{"  A__B--C  ", "A-B-C"},
		{"", ""},
		{"  ", ""},
		{"Tienda   la   80", "TIENDA-LA-80"},
	}
	for _, caso := range casos {
		t.Run(caso.entrada, func(t *testing.T) {
			if obtenido := NormalizarNombre(caso.entrada); obtenido != caso.querer {
				t.Errorf("NormalizarNombre(%q) = %q, want %q", caso.entrada, obtenido, caso.querer)
			}
		})
	}
}

func TestNormalizarNombreEsIdempotente(t *testing.T) {
	for _, entrada := range []string{"Farmacia  La  ñ", "O'Reilly & Cía.", "  A__B--C  ", "Éxito Ñandú"} {
		unaVez := NormalizarNombre(entrada)
		if dosVeces := NormalizarNombre(unaVez); dosVeces != unaVez {
			t.Errorf("no es idempotente en %q: %q y %q", entrada, unaVez, dosVeces)
		}
	}
}

func TestNormalizarNombreTruncaPuntosDeCodigo(t *testing.T) {
	entrada := strings.Repeat("Ñ", LargoMaximoNombre+1)
	salida := NormalizarNombre(entrada)
	if runas := []rune(salida); len(runas) != LargoMaximoNombre {
		t.Errorf("largo = %d puntos de código, want %d", len(runas), LargoMaximoNombre)
	}
	if len(salida) != LargoMaximoNombre*2 {
		t.Errorf("largo en bytes = %d, want %d: el truncamiento no puede ser por bytes", len(salida), LargoMaximoNombre*2)
	}
}

func TestNormalizarNombreQuitaGuionFinalQueDejaElTruncamiento(t *testing.T) {
	entrada := strings.Repeat("A", LargoMaximoNombre-1) + "-B"
	salida := NormalizarNombre(entrada)
	if strings.HasSuffix(salida, "-") {
		t.Errorf("el nombre termina en guion: %q", salida)
	}
	if len([]rune(salida)) > LargoMaximoNombre {
		t.Errorf("el nombre supera el tope: %q", salida)
	}
}

func TestClaveDeCatalogoCortaAlLargoDelEvaluador(t *testing.T) {
	casos := []struct {
		entrada string
		querer  string
	}{
		{"Almacén Éxito Centro", "ALMACEN-EXITO-CENTRO"},
		{"city parking s.a.s.", "CITY-PARKING-SAS"},
		{"Almacén Facol 102 Alquería", "ALMACEN-FACOL-102-ALQUERI"},
		{"  Jardines   del Renacer  ", "JARDINES-DEL-RENACER"},
	}
	for _, caso := range casos {
		t.Run(caso.entrada, func(t *testing.T) {
			if obtenida := ClaveDeCatalogo(caso.entrada); obtenida != caso.querer {
				t.Errorf("ClaveDeCatalogo(%q) = %q, want %q", caso.entrada, obtenida, caso.querer)
			}
		})
	}
}

func TestClaveDeCatalogoCortaAntesDeNormalizar(t *testing.T) {
	nombre := "Almacén Facol 102 Alquería"
	sinCortar := NormalizarNombre(nombre)
	if len([]rune(sinCortar)) <= LargoClaveDeCatalogo {
		t.Fatalf("el caso no supera el largo del evaluador: %q tiene %d puntos de código", sinCortar,
			len([]rune(sinCortar)))
	}
	if ClaveDeCatalogo(nombre) == sinCortar {
		t.Fatalf("ClaveDeCatalogo(%q) no cortó a %d puntos de código", nombre, LargoClaveDeCatalogo)
	}
	if runas := []rune(ClaveDeCatalogo(nombre)); len(runas) > LargoClaveDeCatalogo {
		t.Fatalf("la clave mide %d puntos de código, want %d o menos", len(runas), LargoClaveDeCatalogo)
	}
}

func TestClaveDeCatalogoEsPuntoFijo(t *testing.T) {
	for _, clave := range []string{"ALMACEN-FACOL-102-ALQUERI", "CITY-PARKING-SAS", "ALMACEN-EXITO-CENTRO"} {
		if obtenida := ClaveDeCatalogo(clave); obtenida != clave {
			t.Errorf("ClaveDeCatalogo(%q) = %q, want la misma", clave, obtenida)
		}
	}
}

// TestClaveDeCatalogoNoCoincideConElRecorteDeLaColumna fija el borde de la consulta anterior de
// docs/enriquecimiento-demo.md: la columna guarda el nombre normalizado, así que recortarlo a 25 y
// soltar el separador final solo coincide con ClaveDeCatalogo cuando el nombre original no trae una
// corrida de dos o más separadores ni punto, apóstrofo o ampersand en sus primeros 25 caracteres.
func TestClaveDeCatalogoNoCoincideConElRecorteDeLaColumna(t *testing.T) {
	recorteDeLaColumna := func(nombre string) string {
		runas := []rune(NormalizarNombre(nombre))
		if len(runas) > LargoClaveDeCatalogo {
			runas = runas[:LargoClaveDeCatalogo]
		}
		return strings.TrimRight(string(runas), "-")
	}
	casos := []struct {
		nombre  string
		clave   string
		recorte string
	}{
		{"AAAAAAAAAA__________BBBBBBBBBB", "AAAAAAAAAA-BBBBB", "AAAAAAAAAA-BBBBBBBBBB"},
		{"ABCDEFGHIJKLMNOPQRSTUVWX.YZ", "ABCDEFGHIJKLMNOPQRSTUVWX", "ABCDEFGHIJKLMNOPQRSTUVWXY"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			if obtenida := ClaveDeCatalogo(caso.nombre); obtenida != caso.clave {
				t.Fatalf("ClaveDeCatalogo(%q) = %q, want %q", caso.nombre, obtenida, caso.clave)
			}
			if obtenida := recorteDeLaColumna(caso.nombre); obtenida != caso.recorte {
				t.Fatalf("recorte de la columna = %q, want %q", obtenida, caso.recorte)
			}
		})
	}
}

// TestClaveDeCatalogoEsPrefijoDelNombreNormalizado sostiene la consulta de control de
// docs/enriquecimiento-demo.md: ClaveDeCatalogo recorta antes de normalizar, pero el recorte y la
// normalización son locales, así que la llave siempre queda como prefijo de NormalizarNombre del
// nombre completo. Por eso el cruce por prefijo no puede perder una coincidencia real.
func TestClaveDeCatalogoEsPrefijoDelNombreNormalizado(t *testing.T) {
	casos := []string{
		"",
		"   ",
		"____________",
		"AAAAAAAAAA__________BBBBBBBBBB",
		"ABCDEFGHIJKLMNOPQRSTUVWX.YZ",
		"O'Reilly & Cía. Almacén Éxito Centro",
		"Almacén Facol 102 Alquería",
		"Tienda   la   80",
		strings.Repeat("A", LargoMaximoNombre+LargoClaveDeCatalogo),
		strings.Repeat("A", LargoClaveDeCatalogo) + "____________________BBBB",
	}
	azar := rand.New(rand.NewSource(1))
	alfabeto := []rune("AB _-.&'ÉÑ1")
	for i := 0; i < 5000; i++ {
		runas := make([]rune, azar.Intn(60))
		for j := range runas {
			runas[j] = alfabeto[azar.Intn(len(alfabeto))]
		}
		casos = append(casos, string(runas))
	}
	for _, nombre := range casos {
		clave := ClaveDeCatalogo(nombre)
		columna := NormalizarNombre(nombre)
		if !strings.HasPrefix(columna, clave) {
			t.Fatalf("ClaveDeCatalogo(%q) = %q no es prefijo de NormalizarNombre = %q", nombre, clave,
				columna)
		}
	}
}
