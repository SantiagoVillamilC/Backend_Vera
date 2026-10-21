package inteligencia

import (
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
