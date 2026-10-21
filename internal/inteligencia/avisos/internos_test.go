package avisos

import (
	"math/big"
	"testing"
	"time"
)

// TestMediaDeRatConListaVaciaYConValores fija el promedio exacto del paquete.
func TestMediaDeRatConListaVaciaYConValores(t *testing.T) {
	t.Parallel()
	if media := MediaDeRat(nil); media.Sign() != 0 {
		t.Fatalf("MediaDeRat(nil) = %s, want 0", media.RatString())
	}
	media := MediaDeRat([]*big.Rat{big.NewRat(1, 3), big.NewRat(2, 3), big.NewRat(2, 1)})
	if media.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("MediaDeRat = %s, want 1", media.RatString())
	}
}

// TestEnAnclaDeQuincenaConTolerancia cubre el 15, el último día y un día suelto.
func TestEnAnclaDeQuincenaConTolerancia(t *testing.T) {
	t.Parallel()
	local := func(dia int) time.Time { return time.Date(2026, time.October, dia, 12, 0, 0, 0, Zona) }
	if !enAnclaDeQuincena(local(15), 0) {
		t.Fatal("el 15 debería ser ancla de quincena")
	}
	if !enAnclaDeQuincena(local(31), 0) {
		t.Fatal("el último día debería ser ancla de quincena")
	}
	if enAnclaDeQuincena(local(10), 0) {
		t.Fatal("el 10 no debería ser ancla de quincena")
	}
	if !enAnclaDeQuincena(local(10), 5) {
		t.Fatal("con tolerancia de cinco días el 10 entra en la banda del 15")
	}
}

// TestTituloPorqueYAccionDeCadaTipo evita que un tipo quede sin texto en la bandeja.
func TestTituloPorqueYAccionDeCadaTipo(t *testing.T) {
	t.Parallel()
	for _, tipo := range Tipos {
		if TituloDe(tipo, "salud") == "" {
			t.Errorf("%q no tiene título", tipo)
		}
		if PorqueDe(tipo) == "" {
			t.Errorf("%q no tiene causa", tipo)
		}
		if AccionDe(tipo) == "" {
			t.Errorf("%q no tiene acción", tipo)
		}
	}
	if TituloDe("inventado", "") != "" || PorqueDe("inventado") != "" || AccionDe("inventado") != "" {
		t.Fatal("un tipo desconocido no debería inventar texto")
	}
	if TituloDe(CategoriaSobreMedia, "salud") != "Gastas más en Salud" {
		t.Fatalf("título de categoría = %q", TituloDe(CategoriaSobreMedia, "salud"))
	}
}

// TestClaveYFinDelDiaLocal fijan la frontera del mes en la zona de Colombia.
func TestClaveYFinDelDiaLocal(t *testing.T) {
	t.Parallel()
	if clave := (Mes{Anio: 2026, Mes: time.October}).Clave(); clave != "2026-10" {
		t.Fatalf("Clave = %q, want 2026-10", clave)
	}
	fin := FinDelDiaLocal(time.Date(2026, time.October, 20, 12, 0, 0, 0, time.UTC))
	quiero := time.Date(2026, time.October, 21, 4, 59, 59, 0, time.UTC)
	if !fin.Equal(quiero) {
		t.Fatalf("FinDelDiaLocal = %s, want %s", fin.UTC(), quiero)
	}
}
