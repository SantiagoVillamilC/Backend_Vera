package avisos

import (
	"testing"
	"time"
)

var (
	ahoraDePrueba   = time.Date(2026, 7, 15, 15, 0, 0, 0, time.UTC)
	primeroDePrueba = time.Date(2026, 1, 1, 14, 0, 0, 0, time.UTC)
)

var mesesDePrueba = []Mes{
	{2026, time.January}, {2026, time.February}, {2026, time.March},
	{2026, time.April}, {2026, time.May}, {2026, time.June},
}

var (
	arriendo   = Identidad{Tipo: "llave", Valor: "L-ARRIENDO"}
	mercado    = Identidad{Tipo: "llave", Valor: "L-MERCADO"}
	transporte = Identidad{Tipo: "llave", Valor: "L-TRANSPORTE"}
	nueva      = Identidad{Tipo: "llave", Valor: "L-NUEVA"}
)

func pagoDe(identidad Identidad, fecha time.Time, monto int64, numero string, categoria string) Pago {
	return Pago{
		Identidad: identidad, OcurridoEn: fecha.Add(15 * time.Hour), FechaLocal: fecha,
		MontoCentavos: monto, TransaccionID: numero, Categoria: categoria,
	}
}

func ingresoDe(fecha time.Time, monto int64) Ingreso {
	return Ingreso{OcurridoEn: fecha.Add(14 * time.Hour), FechaLocal: fecha, MontoCentavos: monto}
}

func contextoDelEscenario(t *testing.T, pagos []Pago, ingresos []Ingreso) Contexto {
	t.Helper()
	saldo := int64(0)
	for _, ingreso := range ingresos {
		saldo += ingreso.MontoCentavos
	}
	for _, pago := range pagos {
		saldo -= pago.MontoCentavos
	}
	primero := primeroDePrueba
	ctx, err := NuevoContexto(Instantaneo{
		Pagos: pagos, GastoCategoria: gastosPorCategoria(pagos), IngresoMes: ingresosPorMes(ingresos),
		Primero: &primero, SaldoCentavos: saldo,
	}, ahoraDePrueba, ConfiguracionPorDefecto())
	if err != nil {
		t.Fatalf("contexto: %v", err)
	}
	return ctx
}

func gastosPorCategoria(pagos []Pago) map[string]map[Mes]int64 {
	agregado := map[string]map[Mes]int64{}
	for _, pago := range pagos {
		if agregado[pago.Categoria] == nil {
			agregado[pago.Categoria] = map[Mes]int64{}
		}
		agregado[pago.Categoria][MesDe(pago.FechaLocal)] += pago.MontoCentavos
	}
	return agregado
}

func ingresosPorMes(ingresos []Ingreso) map[Mes]int64 {
	agregado := map[Mes]int64{}
	for _, ingreso := range ingresos {
		agregado[MesDe(ingreso.FechaLocal)] += ingreso.MontoCentavos
	}
	return agregado
}

func escenarioPresupuesto() ([]Pago, []Ingreso) {
	var pagos []Pago
	var ingresos []Ingreso
	numero := 0
	for _, mes := range mesesDePrueba {
		ingresos = append(ingresos, ingresoDe(mes.Fecha(), 250_000_000))
		numero++
		pagos = append(pagos, pagoDe(mercado, mes.Fecha().AddDate(0, 0, 4), 90_000_000, string(rune('A'+numero)), "alimentos_mercado"))
	}
	pagos = append(pagos, pagoDe(mercado, time.Date(2026, 7, 3, 0, 0, 0, 0, Zona), 150_000_000, "Z", "alimentos_mercado"))
	return pagos, ingresos
}

func escenarioCategoria() ([]Pago, []Ingreso) {
	var pagos []Pago
	var ingresos []Ingreso
	valores := []int64{30_000_000, 40_000_000, 35_000_000, 45_000_000, 40_000_000, 40_000_000}
	numero := 0
	for i, mes := range mesesDePrueba {
		ingresos = append(ingresos, ingresoDe(mes.Fecha(), 250_000_000))
		numero++
		pagos = append(pagos, pagoDe(transporte, mes.Fecha().AddDate(0, 0, 4), valores[i], string(rune('A'+numero)), "transporte"))
		numero++
		pagos = append(pagos, pagoDe(mercado, mes.Fecha().AddDate(0, 0, 5), 60_000_000, string(rune('A'+numero)), "alimentos_mercado"))
	}
	pagos = append(pagos, pagoDe(transporte, time.Date(2026, 7, 4, 0, 0, 0, 0, Zona), 95_000_000, "Y", "transporte"))
	pagos = append(pagos, pagoDe(mercado, time.Date(2026, 7, 5, 0, 0, 0, 0, Zona), 60_000_000, "Z", "alimentos_mercado"))
	return pagos, ingresos
}

func escenarioCaida() ([]Pago, []Ingreso) {
	var pagos []Pago
	var ingresos []Ingreso
	montos := []int64{200_000_000, 200_000_000, 200_000_000, 200_000_000, 200_000_000, 100_000_000}
	numero := 0
	for i, mes := range mesesDePrueba {
		ingresos = append(ingresos, ingresoDe(mes.Fecha(), montos[i]))
		numero++
		pagos = append(pagos, pagoDe(mercado, mes.Fecha().AddDate(0, 0, 4), 30_000_000, string(rune('A'+numero)), "alimentos_mercado"))
	}
	return pagos, ingresos
}

func escenarioPago() ([]Pago, []Ingreso) {
	var pagos []Pago
	var ingresos []Ingreso
	numero := 0
	for _, mes := range mesesDePrueba {
		ingresos = append(ingresos, ingresoDe(mes.Fecha(), 120_000_000))
		numero++
		pagos = append(pagos, pagoDe(arriendo, mes.Fecha().AddDate(0, 0, 16), 80_000_000, string(rune('A'+numero)), "vivienda_servicios"))
		numero++
		pagos = append(pagos, pagoDe(mercado, mes.Fecha().AddDate(0, 0, 9), 35_000_000, string(rune('A'+numero)), "alimentos_mercado"))
	}
	return pagos, ingresos
}

func escenarioCobro() ([]Pago, []Ingreso) {
	var pagos []Pago
	var ingresos []Ingreso
	numero := 0
	for _, mes := range mesesDePrueba {
		ingresos = append(ingresos, ingresoDe(mes.Fecha(), 150_000_000))
		numero++
		pagos = append(pagos, pagoDe(mercado, mes.Fecha().AddDate(0, 0, 5), 80_000_000, string(rune('A'+numero)), "alimentos_mercado"))
	}
	pagos = append(pagos, pagoDe(nueva, time.Date(2026, 6, 20, 0, 0, 0, 0, Zona), 15_000_000, "X", "entretenimiento"))
	pagos = append(pagos, pagoDe(nueva, time.Date(2026, 7, 10, 0, 0, 0, 0, Zona), 15_200_000, "Y", "entretenimiento"))
	return pagos, ingresos
}

func tarjetasDeEscenario(t *testing.T, pagos []Pago, ingresos []Ingreso, emitidos int) []Tarjeta {
	t.Helper()
	ctx := contextoDelEscenario(t, pagos, ingresos)
	hechos, err := HechosDelDia(ctx)
	if err != nil {
		t.Fatalf("hechos: %v", err)
	}
	tarjetas, err := TarjetasDelDia(hechos, map[ClaveDeAviso]struct{}{}, emitidos, 3)
	if err != nil {
		t.Fatalf("tarjetas: %v", err)
	}
	return tarjetas
}

func TestLosCincoEscenariosDanSieteTarjetas(t *testing.T) {
	casos := []struct {
		nombre    string
		pagos     func() ([]Pago, []Ingreso)
		esperados []string
	}{
		{"presupuesto_anticipado", escenarioPresupuesto, []string{
			"En julio de 2026 llevas $1.500.000 en gastos. Lo habitual en tus meses completos es $900.000.",
			"En julio de 2026 llevas $1.500.000 en Alimentos y mercado. Lo habitual en esa categoría es $900.000.",
		}},
		{"categoria_sobre_media", escenarioCategoria, []string{
			"En julio de 2026 llevas $1.550.000 en gastos. Lo habitual en tus meses completos es $1.000.000.",
			"En julio de 2026 llevas $950.000 en Transporte. Lo habitual en esa categoría es $400.000.",
		}},
		{"caida_ingreso", escenarioCaida, []string{
			"El ingreso de junio de 2026 fue $1.000.000. Lo habitual en tus meses anteriores es $2.000.000.",
		}},
		{"pago_no_alcanza", escenarioPago, []string{
			"Tu saldo es $300.000 y los pagos recurrentes que vencen desde 2026-07-17 suman $800.000.",
		}},
		{"cobro_nuevo", escenarioCobro, []string{
			"Apareció un cobro de $151.000 el 2026-06-20 y ya se repite o está declarado como recurrente.",
		}},
	}
	total := 0
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			pagos, ingresos := caso.pagos()
			tarjetas := tarjetasDeEscenario(t, pagos, ingresos, 0)
			if len(tarjetas) != len(caso.esperados) {
				t.Fatalf("tarjetas = %d, want %d: %+v", len(tarjetas), len(caso.esperados), tarjetas)
			}
			for i, esperado := range caso.esperados {
				if tarjetas[i].Texto != esperado {
					t.Fatalf("texto %d = %q, want %q", i, tarjetas[i].Texto, esperado)
				}
				if reproduce, motivo := VerificarTarjeta(tarjetas[i]); !reproduce {
					t.Fatalf("la tarjeta %d no verifica: %s", i, motivo)
				}
			}
			total += len(tarjetas)
		})
	}
	if total != 7 {
		t.Fatalf("tarjetas totales = %d, want 7", total)
	}
}
