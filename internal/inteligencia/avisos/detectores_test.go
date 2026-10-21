package avisos

import (
	"testing"
	"time"
)

// contextoConMensuales arma un contexto con seis meses completos y el mes en curso, con un gasto
// mensual dado por mes y un saldo dado.
func contextoDe(t *testing.T, constructor func() ([]Pago, []Ingreso)) Contexto {
	t.Helper()
	pagos, ingresos := constructor()
	return contextoDelEscenario(t, pagos, ingresos)
}

func contextoConMensuales(t *testing.T, mensuales map[Mes]int64, actual int64, saldo int64) Contexto {
	t.Helper()
	var pagos []Pago
	numero := 0
	for mes, monto := range mensuales {
		if monto == 0 {
			continue
		}
		numero++
		pagos = append(pagos, pagoDe(mercado, mes.Fecha().AddDate(0, 0, 4), monto, string(rune('A'+numero)), "alimentos_mercado"))
	}
	if actual > 0 {
		pagos = append(pagos, pagoDe(mercado, time.Date(2026, 7, 3, 0, 0, 0, 0, Zona), actual, "Z", "alimentos_mercado"))
	}
	primero := primeroDePrueba
	ctx, err := NuevoContexto(Instantaneo{
		Pagos: pagos, GastoCategoria: gastosPorCategoria(pagos), Primero: &primero, SaldoCentavos: saldo,
	}, ahoraDePrueba, ConfiguracionPorDefecto())
	if err != nil {
		t.Fatalf("contexto: %v", err)
	}
	return ctx
}

func mensualesUniformes(monto int64) map[Mes]int64 {
	mensuales := map[Mes]int64{}
	for _, mes := range mesesDePrueba {
		mensuales[mes] = monto
	}
	return mensuales
}

func TestConMenosDeTresMesesNoDisparaNinguno(t *testing.T) {
	// El primer movimiento es el 1 de mayo, así que solo mayo y junio son meses completos.
	pagos := []Pago{
		pagoDe(mercado, time.Date(2026, 5, 4, 0, 0, 0, 0, Zona), 100_000_000, "A", "alimentos_mercado"),
		pagoDe(mercado, time.Date(2026, 6, 4, 0, 0, 0, 0, Zona), 100_000_000, "B", "alimentos_mercado"),
		pagoDe(mercado, time.Date(2026, 7, 3, 0, 0, 0, 0, Zona), 500_000_000, "C", "alimentos_mercado"),
	}
	primero := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	ctx, err := NuevoContexto(Instantaneo{
		Pagos: pagos, GastoCategoria: gastosPorCategoria(pagos), Primero: &primero, SaldoCentavos: 100_000_000,
	}, ahoraDePrueba, ConfiguracionPorDefecto())
	if err != nil {
		t.Fatalf("contexto: %v", err)
	}
	for _, decision := range EvaluarLosCinco(ctx) {
		if decision.Dispara {
			t.Fatalf("%s disparó con dos meses completos", decision.Tipo)
		}
		if decision.Causa != CausaHistorialInsuficiente {
			t.Fatalf("%s: causa = %q", decision.Tipo, decision.Causa)
		}
	}
}

func TestPresupuestoEnElBordeNoDispara(t *testing.T) {
	// x exactamente igual a la mediana no dispara: la comparación de R1 es estricta.
	ctx := contextoConMensuales(t, mensualesUniformes(100_000_000), 100_000_000, 0)
	if decision, _ := Evaluar(ctx, PresupuestoAnticipado); decision.Dispara {
		t.Fatalf("presupuesto disparó en el borde: %+v", decision)
	}
	ctx = contextoConMensuales(t, mensualesUniformes(100_000_000), 100_000_001, 0)
	if decision, _ := Evaluar(ctx, PresupuestoAnticipado); !decision.Dispara {
		t.Fatalf("presupuesto no disparó un centavo arriba: %+v", decision)
	}
}

func TestPresupuestoConProrrateo(t *testing.T) {
	cfg := ConfiguracionPorDefecto()
	cfg.ProrrateoAcumulado = true
	mensuales := mensualesUniformes(100_000_000)
	var pagos []Pago
	for mes, monto := range mensuales {
		pagos = append(pagos, pagoDe(mercado, mes.Fecha().AddDate(0, 0, 4), monto, "A", "alimentos_mercado"))
	}
	// El 15 de julio, 60 millones acumulados se proyectan a 124 millones por 31 días entre 15.
	pagos = append(pagos, pagoDe(mercado, time.Date(2026, 7, 3, 0, 0, 0, 0, Zona), 60_000_000, "Z", "alimentos_mercado"))
	primero := primeroDePrueba
	ctx, err := NuevoContexto(Instantaneo{
		Pagos: pagos, GastoCategoria: gastosPorCategoria(pagos), Primero: &primero,
	}, ahoraDePrueba, cfg)
	if err != nil {
		t.Fatalf("contexto: %v", err)
	}
	decision, _ := Evaluar(ctx, PresupuestoAnticipado)
	if !decision.Dispara {
		t.Fatalf("con prorrateo debería disparar: %+v", decision)
	}
	if decision.Valor.Cmp(Entero(124_000_000)) != 0 {
		t.Fatalf("valor proyectado = %s, want 124000000", decision.Valor.RatString())
	}
}

func TestCaidaEnElBordeYGuardiaCv(t *testing.T) {
	ctx := contextoDe(t, escenarioCaida)
	decision, _ := Evaluar(ctx, CaidaIngreso)
	if !decision.Dispara || decision.Causa != CausaR1 {
		t.Fatalf("caída = %+v", decision)
	}
	// x exactamente 0,7 por el centro no dispara: la comparación es estricta.
	ctx.IngresoMes[ctx.MesEvaluado] = 140_000_000
	if borde, _ := Evaluar(ctx, CaidaIngreso); borde.Dispara {
		t.Fatalf("caída disparó en el borde: %+v", borde)
	}
	ctx.IngresoMes[ctx.MesEvaluado] = 139_999_999
	if debajo, _ := Evaluar(ctx, CaidaIngreso); !debajo.Dispara {
		t.Fatalf("caída no disparó debajo del borde: %+v", debajo)
	}
}

func TestCaidaConCvAltoNoEvalua(t *testing.T) {
	mensuales := mensualesUniformes(10_000_000)
	pagos, ingresos := func() ([]Pago, []Ingreso) {
		var pagos []Pago
		var ingresos []Ingreso
		montos := map[Mes]int64{
			{2026, time.January}:  100_000_000,
			{2026, time.February}: 400_000_000,
			{2026, time.March}:    150_000_000,
			{2026, time.February}: 400_000_000,
		}
		for mes := range mensuales {
			pagos = append(pagos, pagoDe(mercado, mes.Fecha().AddDate(0, 0, 4), 10_000_000, "A", "alimentos_mercado"))
		}
		for mes, monto := range montos {
			ingresos = append(ingresos, ingresoDe(mes.Fecha(), monto))
		}
		return pagos, ingresos
	}()
	ctx := contextoDelEscenario(t, pagos, ingresos)
	var evaluado *Mes
	for _, mes := range ctx.Completos {
		copia := mes
		evaluado = &copia
	}
	if evaluado == nil {
		t.Fatal("sin meses completos")
	}
	// El mes evaluado es el anterior al actual; la serie previa tiene CV alto.
	ctx.MesEvaluado = *evaluado
	decision, _ := Evaluar(ctx, CaidaIngreso)
	if decision.Dispara {
		t.Fatalf("con cv alto no debe disparar: %+v", decision)
	}
}

func TestCategoriaSinCandidatas(t *testing.T) {
	mensuales := map[Mes]int64{
		{2026, time.January}:  100_000_000,
		{2026, time.February}: 100_000_000,
		{2026, time.March}:    100_000_000,
		{2026, time.April}:    100_000_000,
		{2026, time.May}:      100_000_000,
		{2026, time.June}:     100_000_000,
	}
	ctx := contextoConMensuales(t, mensuales, 0, 0)
	ctx.GastoCategoria = map[string]map[Mes]int64{"transporte": {{2026, time.January}: 1}}
	if decision, _ := Evaluar(ctx, CategoriaSobreMedia); decision.Causa != CausaSinCategoriaEvaluable {
		t.Fatalf("categoría = %+v", decision)
	}
}

func TestCategoriaSinCategoriaNoParticipa(t *testing.T) {
	mensuales := mensualesUniformes(100_000_000)
	ctx := contextoConMensuales(t, mensuales, 100_000_000, 0)
	ctx.GastoCategoria["sin_categoria"] = map[Mes]int64{{2026, time.July}: 900_000_000}
	decision, _ := Evaluar(ctx, CategoriaSobreMedia)
	if decision.Dispara {
		t.Fatalf("sin_categoria disparó: %+v", decision)
	}
}

func TestPagoNoAlcanzaEnElBorde(t *testing.T) {
	ctx := contextoDe(t, escenarioPago)
	ctx.SaldoCentavos = 80_000_000
	if decision, _ := Evaluar(ctx, PagoNoAlcanza); decision.Dispara {
		t.Fatalf("con saldo igual a la suma no debe disparar: %+v", decision)
	}
	ctx.SaldoCentavos = 79_999_999
	if decision, _ := Evaluar(ctx, PagoNoAlcanza); !decision.Dispara {
		t.Fatalf("con saldo menor a la suma debe disparar: %+v", decision)
	}
}

func TestPagoNoAlcanzaNoCuentaEmergentesNiVencidos(t *testing.T) {
	pagos, ingresos := escenarioPago()
	ctx := contextoDelEscenario(t, pagos, ingresos)
	for i := range ctx.Series {
		if ctx.Series[i].Identidad == arriendo {
			ctx.Series[i].Estado = Emergente
		}
	}
	if decision, _ := Evaluar(ctx, PagoNoAlcanza); decision.Causa != CausaSinPagoEnLaVentana {
		t.Fatalf("una serie emergente no debe contar: %+v", decision)
	}
}

func TestCobroNuevoConPrimerPagoViejoNoDispara(t *testing.T) {
	pagos, ingresos := escenarioCobro()
	// Mover el primer pago de la llave nueva a 36 días antes del corte.
	for i := range pagos {
		if pagos[i].Identidad == nueva && pagos[i].FechaLocal.Equal(time.Date(2026, 6, 20, 0, 0, 0, 0, Zona)) {
			pagos[i].FechaLocal = time.Date(2026, 6, 8, 0, 0, 0, 0, Zona)
			pagos[i].OcurridoEn = pagos[i].FechaLocal.Add(15 * time.Hour)
		}
	}
	ctx := contextoDelEscenario(t, pagos, ingresos)
	if decision, _ := Evaluar(ctx, CobroNuevo); decision.Dispara {
		t.Fatalf("un primer pago fuera de la ventana no debe disparar: %+v", decision)
	}
}

func TestCobroNuevoConUnSoloPagoNoDispara(t *testing.T) {
	pagos, ingresos := escenarioCobro()
	filtrados := pagos[:0]
	for _, pago := range pagos {
		if pago.Identidad == nueva && pago.FechaLocal.Equal(time.Date(2026, 7, 10, 0, 0, 0, 0, Zona)) {
			continue
		}
		filtrados = append(filtrados, pago)
	}
	ctx := contextoDelEscenario(t, filtrados, ingresos)
	decision, _ := Evaluar(ctx, CobroNuevo)
	if decision.Dispara || decision.Causa != CausaLlaveNuevaSinSerie {
		t.Fatalf("un pago suelto no debe disparar: %+v", decision)
	}
}

func TestHechoDeCaidaUsaSeveridadPositiva(t *testing.T) {
	ctx := contextoDe(t, escenarioCaida)
	decision, _ := Evaluar(ctx, CaidaIngreso)
	hecho, err := HechoDe(decision, ctx)
	if err != nil || hecho == nil {
		t.Fatalf("hecho: %v", err)
	}
	if hecho.SeveridadCentavos <= 0 {
		t.Fatalf("severidad = %d, want positiva", hecho.SeveridadCentavos)
	}
}

func TestEvaluarTipoDesconocido(t *testing.T) {
	ctx := contextoConMensuales(t, mensualesUniformes(1), 1, 0)
	if _, err := Evaluar(ctx, Tipo("otro")); err == nil {
		t.Fatal("se esperaba error con un tipo desconocido")
	}
}
