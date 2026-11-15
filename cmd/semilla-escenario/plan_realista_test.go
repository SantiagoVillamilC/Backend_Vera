package main

import (
	"fmt"
	"math/big"
	"sort"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
)

// Estas pruebas son puras: no abren ninguna base. Reproducen en memoria lo que el sembrador y el
// servicio de avisos hacen con plan(ahora), usando las mismas funciones de producción (el léxico v2
// embebido, NuevoContexto y los detectores). Lo que no pueden ver es la tabla inteligencia.catalogo_marca
// de la base real, que podría adelantarse al léxico para un nombre del plan.

var zonaPrueba = time.FixedZone("Bogota", -5*3600)

func ahoraDelMes(mes time.Month) time.Time {
	return time.Date(2026, mes, 19, 12, 0, 0, 0, zonaPrueba)
}

// clasificar replica la cascada del sembrador para un movimiento del plan y devuelve la categoría y
// el origen. Las cuatro entradas pequeñas son las que sembrar() prepara en memoria, catálogo y GMF.
func clasificar(lexico *inteligencia.Lexico, m movimiento) (string, string) {
	switch {
	case m.tipo == tipoDepositoDemo:
		// No deducido: la corrida medida de CF-A2-03 clasificó los 8 ingresos por el léxico como
		// comisiones. Este plan conserva el número de ingresos y no cambia esa regla.
		return "comisiones_costos_financieros", inteligencia.OrigenLexico
	case m.mcc != "":
		categoria, _ := inteligencia.CategoriaPorMCC(m.mcc)
		return string(categoria), inteligencia.OrigenMCC
	case m.nombre == "TIENDA CATALOGO DEMO":
		return "compras", inteligencia.OrigenCatalogo
	case m.nombre == "TIENDA MEMORIA DEMO":
		return "entretenimiento", inteligencia.OrigenMemoria
	case m.gmf > 0:
		return "comisiones_costos_financieros", inteligencia.OrigenInterna
	}
	halladas := map[inteligencia.Categoria]struct{}{}
	for _, par := range inteligencia.CoincidenciasDelLexico(lexico.ParesDeLexico(), m.nombre) {
		halladas[par.Categoria] = struct{}{}
	}
	for categoria := range halladas {
		if len(halladas) == 1 {
			return string(categoria), inteligencia.OrigenLexico
		}
	}
	return "sin_categoria", inteligencia.OrigenSinCategoria
}

// instantaneoDelPlan arma lo que las consultas de bd/consultas-inteligencia/avisos.sql le entregan a
// los detectores: gasto por categoría y mes, ingreso por mes sin las descripciones excluidas, pagos
// con su identidad por nombre normalizado, el primer movimiento y el saldo como suma simple.
func instantaneoDelPlan(lexico *inteligencia.Lexico, cfg avisos.Configuracion, movimientos []movimiento) avisos.Instantaneo {
	instante := avisos.Instantaneo{
		GastoCategoria: map[string]map[avisos.Mes]int64{},
		IngresoMes:     map[avisos.Mes]int64{},
	}
	primero := movimientos[0].fecha
	instante.Primero = &primero
	excluidas := map[string]bool{}
	for _, descripcion := range cfg.DescripcionesNoIngreso {
		excluidas[descripcion] = true
	}
	for i, m := range movimientos {
		instante.SaldoCentavos += m.centavos
		mes := avisos.MesDe(m.fecha)
		if m.tipo == tipoDepositoDemo {
			if !excluidas[m.nombre] {
				instante.IngresoMes[mes] += m.centavos
			}
			continue
		}
		categoria, _ := clasificar(lexico, m)
		if instante.GastoCategoria[categoria] == nil {
			instante.GastoCategoria[categoria] = map[avisos.Mes]int64{}
		}
		instante.GastoCategoria[categoria][mes] += -m.centavos
		instante.Pagos = append(instante.Pagos, avisos.Pago{
			Identidad:     avisos.Identidad{Tipo: "nombre", Valor: inteligencia.NormalizarNombre(m.nombre)},
			OcurridoEn:    m.fecha,
			FechaLocal:    m.fecha.In(avisos.Zona),
			MontoCentavos: -m.centavos,
			TransaccionID: fmt.Sprintf("%03d", i),
			Categoria:     categoria,
		})
	}
	return instante
}

func contextoDelPlan(t *testing.T, lexico *inteligencia.Lexico, ahora time.Time) avisos.Contexto {
	t.Helper()
	cfg := avisos.ConfiguracionPorDefecto()
	ctx, e := avisos.NuevoContexto(instantaneoDelPlan(lexico, cfg, plan(ahora)), ahora, cfg)
	if e != nil {
		t.Fatal(e)
	}
	return ctx
}

func lexicoDePrueba(t *testing.T) *inteligencia.Lexico {
	t.Helper()
	lexico, e := inteligencia.CargarLexicoV2()
	if e != nil {
		t.Fatal(e)
	}
	return lexico
}

// TestPlanRealistaSaldoNuncaNegativo recorre la historia en orden cronológico, con los cargos antes
// que los abonos cuando coinciden en el instante, y exige saldo positivo en cada punto.
func TestPlanRealistaSaldoNuncaNegativo(t *testing.T) {
	t.Parallel()
	for mes := time.January; mes <= time.December; mes++ {
		movimientos := plan(ahoraDelMes(mes))
		sort.SliceStable(movimientos, func(i, j int) bool {
			if !movimientos[i].fecha.Equal(movimientos[j].fecha) {
				return movimientos[i].fecha.Before(movimientos[j].fecha)
			}
			return movimientos[i].centavos < movimientos[j].centavos
		})
		saldo := int64(0)
		for _, m := range movimientos {
			saldo += m.centavos
			if saldo < 0 {
				t.Fatalf("mes=%d el saldo cae a %d tras %q el %s", mes, saldo, m.nombre, m.fecha.Format("2006-01-02"))
			}
		}
		if saldo != 18_510_000 {
			t.Fatalf("mes=%d saldo final %d, quiero 18510000 ($185.100)", mes, saldo)
		}
	}
}

func medianaDeMeses(ctx avisos.Contexto, porMes map[avisos.Mes]int64, hasta avisos.Mes) *big.Rat {
	var valores []int64
	for _, mes := range ctx.Completos {
		if mes.Antes(hasta) && porMes[mes] > 0 {
			valores = append(valores, porMes[mes])
		}
	}
	return avisos.Mediana(valores)
}

// TestPlanRealista verifica, para los doce meses de 2026, cada restricción numérica del escenario con
// los detectores de producción sobre un contexto sintético construido desde plan(ahora).
func TestPlanRealista(t *testing.T) {
	t.Parallel()
	lexico := lexicoDePrueba(t)
	for mes := time.January; mes <= time.December; mes++ {
		ctx := contextoDelPlan(t, lexico, ahoraDelMes(mes))
		verificarSinFechasFuturas(t, ahoraDelMes(mes), mes)
		verificarPagoNoAlcanza(t, ctx, mes)
		verificarPresupuesto(t, ctx, mes)
		verificarCaidaDeIngreso(t, ctx, mes)
		verificarStreaming(t, ctx, mes)
		verificarCategoria(t, ctx, mes)
		verificarSeriesYAvisos(t, ctx, mes)
	}
}

// ultimoDiaDelMesEnCurso es el último día con movimientos en el mes en curso. La demo se mira desde
// el día 14 en adelante, así que ningún movimiento de ese mes puede quedar con fecha futura.
const ultimoDiaDelMesEnCurso = 13

func verificarSinFechasFuturas(t *testing.T, ahora time.Time, mes time.Month) {
	t.Helper()
	for _, m := range plan(ahora) {
		enCurso := m.fecha.Year() == ahora.Year() && m.fecha.Month() == ahora.Month()
		if enCurso && m.fecha.Day() > ultimoDiaDelMesEnCurso {
			t.Fatalf("mes=%d %q cae el día %d del mes en curso, después del día %d", mes, m.nombre, m.fecha.Day(), ultimoDiaDelMesEnCurso)
		}
	}
}

func verificarPagoNoAlcanza(t *testing.T, ctx avisos.Contexto, mes time.Month) {
	t.Helper()
	d, _ := avisos.Evaluar(ctx, avisos.PagoNoAlcanza)
	suma := avisos.Entero((pagoEnel + pagoInternet) * 100)
	if !d.Dispara || d.Valor.Sign() <= 0 || d.Valor.Cmp(d.Referencia) >= 0 || d.Referencia.Cmp(suma) != 0 {
		t.Fatalf("mes=%d pago_no_alcanza dispara=%v saldo=%v suma=%v, quiero 0 < saldo < %v", mes, d.Dispara, d.Valor, d.Referencia, suma)
	}
}

func verificarPresupuesto(t *testing.T, ctx avisos.Contexto, mes time.Month) {
	t.Helper()
	acumulado := avisos.Entero(ctx.GastoMes[ctx.MesActual])
	mediana := medianaDeMeses(ctx, ctx.GastoMes, ctx.MesActual)
	limite := new(big.Rat).Mul(ctx.Cfg.MultiplicadorGasto, mediana)
	d, _ := avisos.Evaluar(ctx, avisos.PresupuestoAnticipado)
	if acumulado.Cmp(limite) <= 0 || !d.Dispara {
		t.Fatalf("mes=%d gasto acumulado %v no supera la mediana %v por el factor %v", mes, acumulado, mediana, ctx.Cfg.MultiplicadorGasto)
	}
}

func verificarCaidaDeIngreso(t *testing.T, ctx avisos.Contexto, mes time.Month) {
	t.Helper()
	x := avisos.Entero(ctx.IngresoMes[ctx.MesEvaluado])
	mediana := medianaDeMeses(ctx, ctx.IngresoMes, ctx.MesEvaluado)
	limite := new(big.Rat).Mul(new(big.Rat).Sub(avisos.Entero(1), ctx.Cfg.CaidaD), mediana)
	d, _ := avisos.Evaluar(ctx, avisos.CaidaIngreso)
	if x.Cmp(limite) >= 0 || !d.Dispara {
		t.Fatalf("mes=%d el ingreso %v no cae más de 30 por ciento frente a %v", mes, x, mediana)
	}
}

func verificarStreaming(t *testing.T, ctx avisos.Contexto, mes time.Month) {
	t.Helper()
	corte := ctx.Hoy.AddDate(0, 0, -ctx.Cfg.VentanaCobroDias)
	cargos := 0
	for _, pago := range ctx.Pagos {
		if pago.Identidad.Valor == inteligencia.NormalizarNombre(nombreStreaming) && !pago.FechaLocal.Before(corte) {
			cargos++
		}
	}
	d, _ := avisos.Evaluar(ctx, avisos.CobroNuevo)
	if cargos != 2 || !d.Dispara || d.Detalle != inteligencia.NormalizarNombre(nombreStreaming) {
		t.Fatalf("mes=%d cargos de STREAMING en %d días: %d, cobro_nuevo dispara=%v detalle=%q", mes, ctx.Cfg.VentanaCobroDias, cargos, d.Dispara, d.Detalle)
	}
}

func verificarCategoria(t *testing.T, ctx avisos.Contexto, mes time.Month) {
	t.Helper()
	d, _ := avisos.Evaluar(ctx, avisos.CategoriaSobreMedia)
	if !d.Dispara || d.Detalle != "entretenimiento" || d.Valor.Cmp(d.Referencia) <= 0 {
		t.Fatalf("mes=%d categoria_sobre_media dispara=%v categoría=%q valor=%v referencia=%v", mes, d.Dispara, d.Detalle, d.Valor, d.Referencia)
	}
}

func verificarSeriesYAvisos(t *testing.T, ctx avisos.Contexto, mes time.Month) {
	t.Helper()
	maduras := 0
	for _, serie := range ctx.Series {
		if serie.Estado == avisos.Madura && serie.Indice.EsRecurrente {
			maduras++
		}
	}
	hechos, e := avisos.HechosDelDia(ctx)
	if e != nil {
		t.Fatal(e)
	}
	porTipo := map[string]int{}
	for _, hecho := range hechos {
		porTipo[string(hecho.Tipo)]++
	}
	if maduras != seriesDelEscenario || !censoIgual(porTipo, avisosDelEscenario) {
		t.Fatalf("mes=%d series maduras=%d (quiero %d), avisos=%v (quiero %v)", mes, maduras, seriesDelEscenario, porTipo, avisosDelEscenario)
	}
}

// TestPlanRealistaTextosDeNoviembre fija los textos que ve el usuario con la fecha por defecto de la
// documentación (19 de noviembre de 2026). Si cambia un monto, esta prueba y docs/escenario-demo.md
// deben cambiar juntos.
func TestPlanRealistaTextosDeNoviembre(t *testing.T) {
	t.Parallel()
	ctx := contextoDelPlan(t, lexicoDePrueba(t), ahoraDelMes(time.November))
	hechos, e := avisos.HechosDelDia(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(hechos) != 5 {
		t.Fatalf("hechos de noviembre: %d, quiero 5", len(hechos))
	}
	esperados := map[avisos.Tipo]string{
		avisos.PagoNoAlcanza:         "Tu saldo es $185.100 y los pagos recurrentes que vencen desde 2026-11-20 suman $249.900.",
		avisos.PresupuestoAnticipado: "En noviembre de 2026 llevas $2.723.500 en gastos. Lo habitual en tus meses completos es $2.239.900.",
		avisos.CategoriaSobreMedia:   "En noviembre de 2026 llevas $60.700 en Entretenimiento. Lo habitual en esa categoría es $60.000.",
		avisos.CaidaIngreso:          "El ingreso de octubre de 2026 fue $1.794.000. Lo habitual en tus meses anteriores es $2.600.000.",
		avisos.CobroNuevo:            "Apareció un cobro de $29.900 el 2026-10-28 y ya se repite o está declarado como recurrente.",
	}
	for _, hecho := range hechos {
		tarjeta, e := avisos.TarjetaDe(hecho)
		if e != nil {
			t.Fatal(e)
		}
		if tarjeta.Texto != esperados[tarjeta.Tipo] {
			t.Errorf("%s: texto %q, quiero %q", tarjeta.Tipo, tarjeta.Texto, esperados[tarjeta.Tipo])
		}
	}
}

// TestCensoDelPlanCoincideConElDeclarado deduce el censo de categorías y orígenes con el léxico v2
// real y lo compara con los mapas que exige cumpleAceptacion. Es la deducción en memoria: la verdad
// final la fija el sembrador contra la base, que imprime lo obtenido si no coincide.
func TestCensoDelPlanCoincideConElDeclarado(t *testing.T) {
	t.Parallel()
	lexico := lexicoDePrueba(t)
	categorias, origenes := map[string]int{}, map[string]int{}
	movimientos := plan(ahoraDelMes(time.November))
	for _, m := range movimientos {
		categoria, origen := clasificar(lexico, m)
		categorias[categoria]++
		origenes[origen]++
	}
	if len(movimientos) != asientosDelEscenario || !censoIgual(categorias, categoriasDelEscenario) || !censoIgual(origenes, origenesDelEscenario) {
		t.Fatalf("censo deducido: asientos=%d categorías=%v orígenes=%v", len(movimientos), categorias, origenes)
	}
}
