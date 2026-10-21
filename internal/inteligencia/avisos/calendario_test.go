package avisos

import (
	"testing"
	"time"
)

func fecha(anio int, mes time.Month, dia int) time.Time {
	return time.Date(anio, mes, dia, 0, 0, 0, 0, Zona)
}

func TestDomingoDePascua(t *testing.T) {
	casos := map[int]string{
		2026: "2026-04-05",
		2027: "2027-03-28",
	}
	for anio, esperado := range casos {
		if obtenido := DomingoDePascua(anio).Format("2006-01-02"); obtenido != esperado {
			t.Fatalf("Pascua %d = %s, want %s", anio, obtenido, esperado)
		}
	}
}

func TestFestivosDeColombia(t *testing.T) {
	// 20 de julio es fijo y 6 de enero se traslada al lunes 12.
	if !EsFestivo(fecha(2026, time.July, 20)) {
		t.Fatal("el 20 de julio debería ser festivo")
	}
	if !EsFestivo(fecha(2026, time.January, 12)) {
		t.Fatal("el 6 de enero de 2026 se traslada al lunes 12")
	}
	if EsFestivo(fecha(2026, time.July, 17)) {
		t.Fatal("el 17 de julio no es festivo")
	}
	if !EsFestivo(fecha(2026, time.April, 2)) || !EsFestivo(fecha(2026, time.April, 3)) {
		t.Fatal("el jueves y el viernes santo son festivos")
	}
}

func TestHabiles(t *testing.T) {
	sabado := fecha(2026, time.July, 18)
	if EsHabil(sabado) {
		t.Fatal("el sábado no es hábil")
	}
	if obtenido := HabilAnterior(sabado); obtenido.Format("2006-01-02") != "2026-07-17" {
		t.Fatalf("hábil anterior del sábado = %s", obtenido.Format("2006-01-02"))
	}
	if obtenido := HabilSiguiente(sabado); obtenido.Format("2006-01-02") != "2026-07-21" {
		t.Fatalf("hábil siguiente del sábado = %s", obtenido.Format("2006-01-02"))
	}
	if obtenido := HabilSiguiente(fecha(2026, time.July, 19)); obtenido.Format("2006-01-02") != "2026-07-21" {
		t.Fatalf("el domingo 19 salta al martes 21: %s", obtenido.Format("2006-01-02"))
	}
}

func TestMesesCompletosAntes(t *testing.T) {
	primero := fecha(2026, time.January, 1)
	meses := MesesCompletosAntes(&primero, Mes{2026, time.July})
	if len(meses) != 6 || meses[0] != (Mes{2026, time.January}) || meses[5] != (Mes{2026, time.June}) {
		t.Fatalf("meses = %+v", meses)
	}
	// Un primer movimiento el día 2 deja el mes de enero fuera.
	segundo := fecha(2026, time.January, 2)
	meses = MesesCompletosAntes(&segundo, Mes{2026, time.July})
	if len(meses) != 5 || meses[0] != (Mes{2026, time.February}) {
		t.Fatalf("meses con primero el día 2 = %+v", meses)
	}
	if meses := MesesCompletosAntes(nil, Mes{2026, time.July}); len(meses) != 0 {
		t.Fatalf("sin primero = %+v", meses)
	}
}

func TestMesSiguienteYAnteriorCruzanElAnio(t *testing.T) {
	if obtenido := (Mes{2026, time.December}).Siguiente(); obtenido != (Mes{2027, time.January}) {
		t.Fatalf("siguiente de diciembre = %+v", obtenido)
	}
	if obtenido := (Mes{2026, time.January}).Anterior(); obtenido != (Mes{2025, time.December}) {
		t.Fatalf("anterior de enero = %+v", obtenido)
	}
	if !(Mes{2026, time.June}).Antes(Mes{2026, time.July}) {
		t.Fatal("junio debería ser anterior a julio")
	}
}
