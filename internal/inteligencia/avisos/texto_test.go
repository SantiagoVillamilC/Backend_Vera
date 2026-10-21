package avisos

import (
	"strings"
	"testing"
)

func tarjetaDePrueba(tipo Tipo, texto string) Tarjeta {
	return Tarjeta{
		Tipo: tipo, Texto: texto, Porque: porques[tipo], Accion: acciones[tipo],
		MontoCentavos: 30_000_000, Clave: "gasto-total",
	}
}

func TestPesos(t *testing.T) {
	casos := map[int64]string{
		30_000_000: "$300.000",
		123_456:    "$1.234,56",
		0:          "$0",
		100:        "$1",
		99:         "$0,99",
		1:          "$0,01",
	}
	for centavos, esperado := range casos {
		if obtenido := Pesos(centavos); obtenido != esperado {
			t.Fatalf("Pesos(%d) = %q, want %q", centavos, obtenido, esperado)
		}
	}
}

func TestEtiquetasDeCategoria(t *testing.T) {
	if obtenida := EtiquetaDeCategoria("comisiones_costos_financieros"); obtenida != "Comisiones y costos financieros" {
		t.Fatalf("etiqueta = %q", obtenida)
	}
	if obtenida := EtiquetaDeCategoria("inventada"); obtenida != "inventada" {
		t.Fatalf("un código desconocido se muestra tal cual: %q", obtenida)
	}
}

func TestVerificadorRechazaTextoAjeno(t *testing.T) {
	casos := map[string]string{
		"monto ajeno":   "En julio de 2026 llevas $300.000 en gastos. Lo habitual es $250.000.",
		"monto ausente": "En julio de 2026 llevas $300.000 en gastos.",
		"mes ausente":   "Llevas $300.000 en gastos.",
		"mes repetido":  "En julio de 2026 y en julio de 2026 llevas $300.000. Lo habitual es $200.000.",
		"contador":      "En julio de 2026 llevas $300.000 en 3 gastos. Lo habitual es $200.000.",
		"punto y coma":  "En julio de 2026 llevas $300.000; lo habitual es $200.000.",
		"raya":          "En julio de 2026 llevas $300.000 \u2014 lo habitual es $200.000.",
		"numero raro":   "En julio de 2026 llevas 300.000.5 en gastos. Lo habitual es $200.000.",
	}
	for nombre, texto := range casos {
		t.Run(nombre, func(t *testing.T) {
			tarjeta := tarjetaDePrueba(PresupuestoAnticipado, texto)
			mes := Mes{2026, 7}
			tarjeta.Mes = &mes
			referencia := int64(20_000_000)
			tarjeta.ReferenciaCentavos = &referencia
			if reproduce, _ := VerificarTarjeta(tarjeta); reproduce {
				t.Fatalf("el verificador aceptó %q", texto)
			}
		})
	}
}

func TestVerificadorAceptaElTextoFirmado(t *testing.T) {
	mes := Mes{2026, 7}
	referencia := int64(90_000_000)
	tarjeta := Tarjeta{
		Tipo:   PresupuestoAnticipado,
		Texto:  "En julio de 2026 llevas $1.500.000 en gastos. Lo habitual en tus meses completos es $900.000.",
		Porque: porques[PresupuestoAnticipado], Accion: acciones[PresupuestoAnticipado],
		MontoCentavos: 150_000_000, ReferenciaCentavos: &referencia, Mes: &mes, Clave: "gasto-total",
	}
	if reproduce, motivo := VerificarTarjeta(tarjeta); !reproduce {
		t.Fatalf("no verificó: %s", motivo)
	}
}

func TestVerificadorRechazaLaClaveEnElTexto(t *testing.T) {
	mes := Mes{2026, 7}
	referencia := int64(20_000_000)
	tarjeta := Tarjeta{
		Tipo:   CategoriaSobreMedia,
		Texto:  "En julio de 2026 llevas $300.000 en alimentos_mercado. Lo habitual es $200.000.",
		Porque: porques[CategoriaSobreMedia], Accion: acciones[CategoriaSobreMedia],
		MontoCentavos: 30_000_000, ReferenciaCentavos: &referencia, Mes: &mes, Clave: "alimentos_mercado",
	}
	if reproduce, motivo := VerificarTarjeta(tarjeta); reproduce || motivo != "clave_en_el_texto" {
		t.Fatalf("motivo = %q", motivo)
	}
}

func TestVerificadorExigeLaFechaDelAviso(t *testing.T) {
	referencia := int64(80_000_000)
	fecha := fecha(2026, 7, 17)
	tarjeta := Tarjeta{
		Tipo:   PagoNoAlcanza,
		Texto:  "Tu saldo es $300.000 y los pagos recurrentes que vencen desde 2026-07-18 suman $800.000.",
		Porque: porques[PagoNoAlcanza], Accion: acciones[PagoNoAlcanza],
		MontoCentavos: 30_000_000, ReferenciaCentavos: &referencia, Fecha: &fecha, Clave: "pago-proximo",
	}
	if reproduce, motivo := VerificarTarjeta(tarjeta); reproduce || motivo != "fecha_ajena" {
		t.Fatalf("motivo = %q", motivo)
	}
}

func TestOrdenPorPrioridadYSeveridad(t *testing.T) {
	mes := Mes{2026, 7}
	fecha := fecha(2026, 7, 17)
	alta := int64(80_000_000)
	baja := int64(40_000_000)
	tarjetas := []Tarjeta{
		{Tipo: CategoriaSobreMedia, MontoCentavos: 90_000_000, ReferenciaCentavos: &baja, Mes: &mes, Clave: "b"},
		{Tipo: PresupuestoAnticipado, MontoCentavos: 90_000_000, ReferenciaCentavos: &baja, Mes: &mes, Clave: "a"},
		{Tipo: PagoNoAlcanza, MontoCentavos: 10_000_000, ReferenciaCentavos: &alta, Fecha: &fecha, Clave: "p"},
		{Tipo: CaidaIngreso, MontoCentavos: 10_000_000, ReferenciaCentavos: &baja, Mes: &mes, Clave: "i"},
	}
	ordenadas := Orden(tarjetas)
	esperado := []Tipo{PagoNoAlcanza, CaidaIngreso, PresupuestoAnticipado, CategoriaSobreMedia}
	for i, tipo := range esperado {
		if ordenadas[i].Tipo != tipo {
			t.Fatalf("posición %d = %s, want %s", i, ordenadas[i].Tipo, tipo)
		}
	}
}

func TestTopeConPresupuestoDentroDelCupo(t *testing.T) {
	mes := Mes{2026, 7}
	referencia := int64(10_000_000)
	base := []Tarjeta{
		{Tipo: PagoNoAlcanza, MontoCentavos: 5, ReferenciaCentavos: &referencia, Mes: &mes, Clave: "p"},
		{Tipo: CaidaIngreso, MontoCentavos: 5, ReferenciaCentavos: &referencia, Mes: &mes, Clave: "i"},
		{Tipo: CobroNuevo, MontoCentavos: 5, Mes: &mes, Clave: "c"},
		{Tipo: PresupuestoAnticipado, MontoCentavos: 30_000_000, ReferenciaCentavos: &referencia, Mes: &mes, Clave: "g"},
		{Tipo: CategoriaSobreMedia, MontoCentavos: 30_000_000, ReferenciaCentavos: &referencia, Mes: &mes, Clave: "k"},
	}
	ordenadas := Orden(base)
	if recortadas := Tope(ordenadas, 0, 3); len(recortadas) != 3 || recortadas[2].Tipo != CobroNuevo {
		t.Fatalf("con cupo 3 = %+v", recortadas)
	}
	if recortadas := Tope(ordenadas, 1, 3); len(recortadas) != 2 || recortadas[1].Tipo != CaidaIngreso {
		t.Fatalf("con un emitido = %+v", recortadas)
	}
	if recortadas := Tope(ordenadas, 3, 3); len(recortadas) != 0 {
		t.Fatalf("con el cupo agotado = %+v", recortadas)
	}
	if recortadas := Tope(ordenadas, 5, 3); len(recortadas) != 0 {
		t.Fatalf("con más emitidos que el tope = %+v", recortadas)
	}
	// Con D6, presupuesto compite: con cupo 4 entra y categoria queda fuera.
	if recortadas := Tope(ordenadas, 0, 4); recortadas[3].Tipo != PresupuestoAnticipado {
		t.Fatalf("con cupo 4 = %+v", recortadas)
	}
}

func TestTarjetasDelDiaDescartaLasYaEmitidas(t *testing.T) {
	hechos := []Hecho{{
		Tipo: PresupuestoAnticipado, Causa: CausaR1, Clave: "gasto-total",
		MontoCentavos: 150_000_000, ReferenciaCentavos: puntero(90_000_000), Mes: mesPuntero(Mes{2026, 7}),
		SeveridadCentavos: 60_000_000,
	}}
	clave := Clave(hechos[0])
	tarjetas, err := TarjetasDelDia(hechos, map[ClaveDeAviso]struct{}{clave: {}}, 0, 3)
	if err != nil {
		t.Fatalf("tarjetas: %v", err)
	}
	if len(tarjetas) != 0 {
		t.Fatalf("tarjetas = %+v, want vacío", tarjetas)
	}
}

func TestTarjetasDelDiaFallaSiElTextoNoReproduce(t *testing.T) {
	hechos := []Hecho{{
		Tipo: PresupuestoAnticipado, Causa: CausaR1, Clave: "gasto-total",
		MontoCentavos: 150_000_000, ReferenciaCentavos: puntero(-1), Mes: mesPuntero(Mes{2026, 7}),
	}}
	if _, err := TarjetasDelDia(hechos, nil, 0, 3); err == nil {
		t.Fatal("se esperaba un error de texto")
	} else if !strings.Contains(err.Error(), "no reproduce") {
		t.Fatalf("error = %v", err)
	}
}

func mesPuntero(mes Mes) *Mes {
	return &mes
}
