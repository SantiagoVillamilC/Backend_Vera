package avisos

import (
	"errors"
	"math/big"
	"sort"
	"time"
)

// NuevoContexto arma el contexto de un usuario en un instante. Normaliza el reloj a la zona local para
// que la frontera de día y de mes no se corra por un instante que llegó en UTC.
func NuevoContexto(instante Instantaneo, ahora time.Time, cfg Configuracion) (Contexto, error) {
	if err := cfg.Validar(); err != nil {
		return Contexto{}, err
	}
	local := ahora.In(Zona)
	hoy := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Zona)
	mesActual := MesDe(hoy)

	pagos := make([]Pago, 0, len(instante.Pagos))
	for _, pago := range instante.Pagos {
		if !pago.OcurridoEn.After(ahora) {
			pagos = append(pagos, pago)
		}
	}
	gastoCategoria := map[string]map[Mes]int64{}
	gastoMes := map[Mes]int64{}
	for categoria, porMes := range instante.GastoCategoria {
		gastoCategoria[categoria] = map[Mes]int64{}
		for mes, total := range porMes {
			gastoCategoria[categoria][mes] = total
			gastoMes[mes] += total
		}
	}
	ingresoMes := map[Mes]int64{}
	for mes, total := range instante.IngresoMes {
		ingresoMes[mes] = total
	}

	var primero *time.Time
	if instante.Primero != nil && !instante.Primero.After(ahora) {
		primero = instante.Primero
	}
	return Contexto{
		Ahora:          local,
		Hoy:            hoy,
		MesActual:      mesActual,
		MesEvaluado:    mesActual.Anterior(),
		PrimeroLocal:   primero,
		Completos:      MesesCompletosAntes(primero, mesActual),
		GastoMes:       gastoMes,
		GastoCategoria: gastoCategoria,
		IngresoMes:     ingresoMes,
		SaldoCentavos:  instante.SaldoCentavos,
		Pagos:          pagos,
		Series:         DetectarSeries(pagos, hoy, cfg.Recurrencia),
		Cfg:            cfg,
	}, nil
}

// Evaluar aplica un detector con la regla y la línea base de la configuración del contexto.
func Evaluar(ctx Contexto, tipo Tipo) (Decision, error) {
	switch tipo {
	case PresupuestoAnticipado:
		return evaluarPresupuesto(ctx), nil
	case CategoriaSobreMedia:
		return evaluarCategoria(ctx), nil
	case CaidaIngreso:
		return evaluarCaida(ctx), nil
	case PagoNoAlcanza:
		return evaluarPago(ctx), nil
	case CobroNuevo:
		return evaluarCobro(ctx), nil
	default:
		return Decision{}, errors.New("avisos: tipo de aviso desconocido")
	}
}

// EvaluarLosCinco devuelve las cinco decisiones en el orden de la referencia.
func EvaluarLosCinco(ctx Contexto) []Decision {
	decisiones := make([]Decision, 0, len(Tipos))
	for _, tipo := range Tipos {
		decision, _ := Evaluar(ctx, tipo)
		decisiones = append(decisiones, decision)
	}
	return decisiones
}

func evaluarPresupuesto(ctx Contexto) Decision {
	x := proyectado(ctx, ctx.GastoMes[ctx.MesActual])
	valores := valoresConGasto(ctx, ctx.GastoMes)
	if len(ctx.Completos) < ctx.Cfg.MesesMinimos || len(valores) < ctx.Cfg.MesesMinimos {
		return Decision{Tipo: PresupuestoAnticipado, Causa: CausaHistorialInsuficiente, Valor: x}
	}
	dispara, centro, z, causa := superior(ctx, x, valores)
	return Decision{
		Tipo: PresupuestoAnticipado, Dispara: dispara, Causa: causa,
		Valor: x, Referencia: centro, Estadistico: z,
	}
}

func evaluarCategoria(ctx Contexto) Decision {
	if len(ctx.Completos) < ctx.Cfg.MesesMinimos {
		return Decision{Tipo: CategoriaSobreMedia, Causa: CausaHistorialInsuficiente}
	}
	categorias := make([]string, 0, len(ctx.GastoCategoria))
	for categoria := range ctx.GastoCategoria {
		if categoria == "sin_categoria" {
			continue
		}
		categorias = append(categorias, categoria)
	}
	sort.Strings(categorias)

	type candidata struct {
		categoria string
		ratio     *big.Rat
		decision  Decision
	}
	var candidatas []candidata
	for _, categoria := range categorias {
		valores := valoresConGasto(ctx, ctx.GastoCategoria[categoria])
		if len(valores) < ctx.Cfg.MesesMinimos {
			continue
		}
		x := proyectado(ctx, ctx.GastoCategoria[categoria][ctx.MesActual])
		dispara, centro, z, causa := superior(ctx, x, valores)
		ratio := Entero(0)
		if centro.Sign() > 0 {
			ratio = new(big.Rat).Quo(x, centro)
		}
		candidatas = append(candidatas, candidata{
			categoria: categoria,
			ratio:     ratio,
			decision: Decision{
				Tipo: CategoriaSobreMedia, Dispara: dispara, Causa: causa,
				Valor: x, Referencia: centro, Estadistico: z, Detalle: categoria,
			},
		})
	}
	if len(candidatas) == 0 {
		return Decision{Tipo: CategoriaSobreMedia, Causa: CausaSinCategoriaEvaluable}
	}
	disparan := make([]candidata, 0, len(candidatas))
	for _, c := range candidatas {
		if c.decision.Dispara {
			disparan = append(disparan, c)
		}
	}
	conjunto := candidatas
	if len(disparan) > 0 {
		conjunto = disparan
	}
	elegida := conjunto[0]
	for _, otra := range conjunto[1:] {
		if otra.ratio.Cmp(elegida.ratio) > 0 {
			elegida = otra
		}
	}
	return elegida.decision
}

func evaluarCaida(ctx Contexto) Decision {
	evaluado := ctx.MesEvaluado
	hastaEvaluado := MesesCompletosAntes(ctx.PrimeroLocal, evaluado.Siguiente())
	previos := make([]Mes, 0, len(hastaEvaluado))
	for _, mes := range hastaEvaluado {
		if mes.Antes(evaluado) {
			previos = append(previos, mes)
		}
	}
	if !contiene(hastaEvaluado, evaluado) || len(previos) < ctx.Cfg.MesesMinimos ||
		len(ctx.Completos) < ctx.Cfg.MesesMinimos {
		return Decision{Tipo: CaidaIngreso, Causa: CausaHistorialInsuficiente}
	}
	valores := make([]*big.Rat, 0, len(previos))
	for _, mes := range previos {
		valores = append(valores, Entero(ctx.IngresoMes[mes]))
	}
	x := Entero(ctx.IngresoMes[evaluado])
	centro := lineaBaseDeRat(valores, ctx.Cfg.LineaBase)
	desviacion := MADDeRat(valores)
	if ctx.Cfg.Regla == ReglaR2 && desviacion.Sign() > 0 {
		z, _ := ZModificado(x, centro, desviacion, ctx.Cfg.ZFactor)
		dispara := z.Cmp(new(big.Rat).Neg(ctx.Cfg.ZUmbral)) < 0
		causa := CausaZBajoElUmbral
		if dispara {
			causa = CausaZSobreElUmbral
		}
		return Decision{
			Tipo: CaidaIngreso, Dispara: dispara, Causa: causa,
			Valor: x, Referencia: centro, Estadistico: z,
		}
	}
	mediana := MedianaDeRat(valores)
	if mediana.Sign() <= 0 {
		return Decision{Tipo: CaidaIngreso, Causa: CausaGuardiaCVRobusto, Valor: x, Referencia: centro}
	}
	cv := new(big.Rat).Quo(desviacion, mediana)
	if cv.Cmp(ctx.Cfg.CVRobustoMax) >= 0 {
		return Decision{
			Tipo: CaidaIngreso, Causa: CausaGuardiaCVRobusto,
			Valor: x, Referencia: centro, Estadistico: cv,
		}
	}
	causa := CausaR1
	if ctx.Cfg.Regla == ReglaR2 {
		causa = CausaRespaldoR1MADcero
	}
	limite := new(big.Rat).Mul(new(big.Rat).Sub(Entero(1), ctx.Cfg.CaidaD), centro)
	return Decision{
		Tipo: CaidaIngreso, Dispara: x.Cmp(limite) < 0, Causa: causa,
		Valor: x, Referencia: centro, Estadistico: cv,
	}
}

func evaluarPago(ctx Contexto) Decision {
	if len(ctx.Completos) < ctx.Cfg.MesesMinimos {
		return Decision{
			Tipo: PagoNoAlcanza, Causa: CausaHistorialInsuficiente,
			Valor: Entero(ctx.SaldoCentavos),
		}
	}
	if len(ctx.Series) == 0 {
		return Decision{
			Tipo: PagoNoAlcanza, Causa: CausaSinSeries,
			Valor: Entero(ctx.SaldoCentavos),
		}
	}
	limite := ctx.Hoy.AddDate(0, 0, ctx.Cfg.VentanaPagoDias)
	var esperados []Serie
	for _, serie := range ctx.Series {
		if serie.Estado != Madura || serie.Proximo == nil {
			continue
		}
		if !serie.Proximo.Desde.After(limite) {
			esperados = append(esperados, serie)
		}
	}
	if len(esperados) == 0 {
		return Decision{
			Tipo: PagoNoAlcanza, Causa: CausaSinPagoEnLaVentana,
			Valor: Entero(ctx.SaldoCentavos),
		}
	}
	suma := int64(0)
	var fecha time.Time
	var detalle string
	for i, serie := range esperados {
		suma += serie.Proximo.MontoCentro
		if i == 0 || serie.Proximo.Centro.Before(fecha) {
			fecha = serie.Proximo.Centro
		}
		if i > 0 {
			detalle += ", "
		}
		detalle += serie.Identidad.Valor
	}
	return Decision{
		Tipo: PagoNoAlcanza, Dispara: ctx.SaldoCentavos < suma, Causa: CausaPagoEnLaVentana,
		Valor: Entero(ctx.SaldoCentavos), Referencia: Entero(suma), Detalle: detalle, Fecha: &fecha,
	}
}

func evaluarCobro(ctx Contexto) Decision {
	if len(ctx.Completos) < ctx.Cfg.MesesMinimos {
		return Decision{Tipo: CobroNuevo, Causa: CausaHistorialInsuficiente}
	}
	corte := ctx.Hoy.AddDate(0, 0, -ctx.Cfg.VentanaCobroDias)
	grupos := map[Identidad][]Pago{}
	for _, pago := range ctx.Pagos {
		grupos[pago.Identidad] = append(grupos[pago.Identidad], pago)
	}
	identidades := make([]Identidad, 0, len(grupos))
	for identidad := range grupos {
		identidades = append(identidades, identidad)
	}
	sort.Slice(identidades, func(i, j int) bool {
		if identidades[i].Tipo != identidades[j].Tipo {
			return identidades[i].Tipo < identidades[j].Tipo
		}
		return identidades[i].Valor < identidades[j].Valor
	})

	nuevas := 0
	type recurrente struct {
		identidad Identidad
		mediana   *big.Rat
		primero   time.Time
		valor     string
	}
	var recurrentes []recurrente
	for _, identidad := range identidades {
		delGrupo := grupos[identidad]
		sort.Slice(delGrupo, func(i, j int) bool {
			return delGrupo[i].OcurridoEn.Before(delGrupo[j].OcurridoEn)
		})
		primero := delGrupo[0].FechaLocal
		if primero.Before(corte) {
			continue
		}
		nuevas++
		montos := make([]int64, 0, len(delGrupo))
		for _, pago := range delGrupo {
			montos = append(montos, pago.MontoCentavos)
		}
		centro := Mediana(montos)
		dentro := 0
		for _, monto := range montos {
			diferencia := Abs(new(big.Rat).Sub(Entero(monto), centro))
			if diferencia.Cmp(new(big.Rat).Mul(ctx.Cfg.TolMontoCobro, centro)) <= 0 {
				dentro++
			}
		}
		if dentro >= ctx.Cfg.PagosMinimosCobro {
			recurrentes = append(recurrentes, recurrente{
				identidad: identidad, mediana: centro, primero: primero, valor: identidad.Valor,
			})
		}
	}
	if len(recurrentes) == 0 {
		causa := CausaSinLlaveNueva
		if nuevas > 0 {
			causa = CausaLlaveNuevaSinSerie
		}
		return Decision{Tipo: CobroNuevo, Causa: causa}
	}
	sort.Slice(recurrentes, func(i, j int) bool {
		comparacion := recurrentes[i].mediana.Cmp(recurrentes[j].mediana)
		if comparacion != 0 {
			return comparacion > 0
		}
		if !recurrentes[i].primero.Equal(recurrentes[j].primero) {
			return recurrentes[i].primero.After(recurrentes[j].primero)
		}
		return recurrentes[i].valor < recurrentes[j].valor
	})
	elegida := recurrentes[0]
	fecha := elegida.primero
	return Decision{
		Tipo: CobroNuevo, Dispara: true, Causa: CausaLlaveNueva,
		Valor: elegida.mediana, Detalle: elegida.valor, Fecha: &fecha,
	}
}

// HechosDelDia evalúa los cinco detectores y devuelve solo los que disparan, con sus cifras listas
// para el texto y para la base.
func HechosDelDia(ctx Contexto) ([]Hecho, error) {
	var hechos []Hecho
	for _, decision := range EvaluarLosCinco(ctx) {
		hecho, err := HechoDe(decision, ctx)
		if err != nil {
			return nil, err
		}
		if hecho != nil {
			hechos = append(hechos, *hecho)
		}
	}
	return hechos, nil
}

// HechoDe convierte una decisión que disparó en su hecho. Una decisión que no disparó devuelve nil.
func HechoDe(decision Decision, ctx Contexto) (*Hecho, error) {
	if !decision.Dispara {
		return nil, nil
	}
	switch decision.Tipo {
	case PagoNoAlcanza:
		if decision.Fecha == nil || decision.Valor == nil || decision.Referencia == nil {
			return nil, errors.New("avisos: pago_no_alcanza disparó sin fecha o sin cifras")
		}
		fecha := *decision.Fecha
		return &Hecho{
			Tipo: decision.Tipo, Causa: decision.Causa, Clave: "pago-proximo",
			MontoCentavos: RedondearMitadAPar(decision.Valor), ReferenciaCentavos: puntero(RedondearMitadAPar(decision.Referencia)),
			Fecha: &fecha, SeveridadCentavos: RedondearMitadAPar(new(big.Rat).Sub(decision.Referencia, decision.Valor)),
			Detalle: decision.Detalle,
		}, nil
	case CobroNuevo:
		if decision.Fecha == nil || decision.Valor == nil {
			return nil, errors.New("avisos: cobro_nuevo disparó sin fecha o sin monto")
		}
		fecha := *decision.Fecha
		monto := RedondearMitadAPar(decision.Valor)
		return &Hecho{
			Tipo: decision.Tipo, Causa: decision.Causa, Clave: "cobro-nuevo",
			MontoCentavos: monto, Fecha: &fecha, SeveridadCentavos: monto, Detalle: decision.Detalle,
		}, nil
	}
	if decision.Valor == nil || decision.Referencia == nil {
		return nil, errors.New("avisos: el aviso disparó sin cifras")
	}
	mes := ctx.MesActual
	if decision.Tipo == CaidaIngreso {
		mes = ctx.MesEvaluado
	}
	clave := "gasto-total"
	if decision.Tipo == CaidaIngreso {
		clave = "ingreso-mensual"
	}
	if decision.Tipo == CategoriaSobreMedia {
		clave = decision.Detalle
	}
	valor := RedondearMitadAPar(decision.Valor)
	referencia := RedondearMitadAPar(decision.Referencia)
	severidad := new(big.Rat).Sub(decision.Valor, decision.Referencia)
	if decision.Tipo == CaidaIngreso {
		severidad = new(big.Rat).Neg(severidad)
	}
	return &Hecho{
		Tipo: decision.Tipo, Causa: decision.Causa, Clave: clave,
		MontoCentavos: valor, ReferenciaCentavos: &referencia, Mes: &mes,
		SeveridadCentavos: RedondearMitadAPar(severidad), Detalle: decision.Detalle,
	}, nil
}

func superior(ctx Contexto, x *big.Rat, valores []*big.Rat) (bool, *big.Rat, *big.Rat, Causa) {
	centro := lineaBaseDeRat(valores, ctx.Cfg.LineaBase)
	desviacion := MADDeRat(valores)
	if ctx.Cfg.Regla == ReglaR2 && desviacion.Sign() > 0 {
		z, _ := ZModificado(x, centro, desviacion, ctx.Cfg.ZFactor)
		if z.Cmp(ctx.Cfg.ZUmbral) > 0 {
			return true, centro, z, CausaZSobreElUmbral
		}
		return false, centro, z, CausaZBajoElUmbral
	}
	causa := CausaR1
	if ctx.Cfg.Regla == ReglaR2 {
		causa = CausaRespaldoR1MADcero
	}
	limite := new(big.Rat).Mul(ctx.Cfg.MultiplicadorGasto, centro)
	return x.Cmp(limite) > 0, centro, nil, causa
}

func lineaBaseDeRat(valores []*big.Rat, base LineaBase) *big.Rat {
	if base == LineaMedia {
		return MediaDeRat(valores)
	}
	return MedianaDeRat(valores)
}

func proyectado(ctx Contexto, valor int64) *big.Rat {
	x := Entero(valor)
	if !ctx.Cfg.ProrrateoAcumulado || valor == 0 {
		return x
	}
	dias := UltimoDiaDelMes(ctx.Hoy.Year(), ctx.Hoy.Month()).Day()
	return new(big.Rat).Mul(x, Fraccion(int64(dias), int64(ctx.Hoy.Day())))
}

func valoresConGasto(ctx Contexto, porMes map[Mes]int64) []*big.Rat {
	var valores []*big.Rat
	for _, mes := range ctx.Completos {
		if porMes[mes] > 0 {
			valores = append(valores, Entero(porMes[mes]))
		}
	}
	return valores
}

func contiene(meses []Mes, buscado Mes) bool {
	for _, mes := range meses {
		if mes == buscado {
			return true
		}
	}
	return false
}

func puntero(valor int64) *int64 {
	return &valor
}
