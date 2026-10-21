package avisos

import (
	"math"
	"math/big"
	"sort"
	"time"
)

// CalcularIndice replica el detector de recurrencia: R = N por (w_monto por Monto más w_fecha por
// Fecha). Todo exacto, porque un R justo sobre el umbral no puede depender del redondeo.
func CalcularIndice(fechas []time.Time, montos []int64, cfg ConfiguracionDeRecurrencia) Indice {
	ocurrencias := len(fechas)
	if ocurrencias < 2 {
		return Indice{
			Ocurrencias: ocurrencias,
			N:           menorUno(Fraccion(int64(ocurrencias), int64(cfg.NMin))),
			Monto:       Entero(0),
			Fecha:       Entero(0),
			R:           Entero(0),
		}
	}
	n := menorUno(Fraccion(int64(ocurrencias), int64(cfg.NMin)))
	monto := fraccionDeMontos(montos, cfg.TolMonto)
	intervalos := make([]int64, 0, len(fechas)-1)
	for i := 1; i < len(fechas); i++ {
		intervalos = append(intervalos, int64(diasEntre(fechas[i-1], fechas[i])))
	}
	fecha, cadencia := fraccionDeFechas(intervalos, cfg)
	producto := new(big.Rat).Mul(cfg.WMonto, monto)
	producto.Add(producto, new(big.Rat).Mul(cfg.WFecha, fecha))
	r := new(big.Rat).Mul(n, producto)
	return Indice{
		Ocurrencias:  ocurrencias,
		N:            n,
		Monto:        monto,
		Fecha:        fecha,
		R:            r,
		EsRecurrente: r.Cmp(cfg.Umbral) >= 0,
		CadenciaDias: cadencia,
	}
}

// EvaluarSerie devuelve el estado y la banda del siguiente pago de una serie ordenada en el tiempo.
func EvaluarSerie(fechas []time.Time, montos []int64, hoy time.Time, cfg ConfiguracionDeRecurrencia) Serie {
	if len(fechas) == 0 {
		return Serie{}
	}
	indice := CalcularIndice(fechas, montos, cfg)
	cadencia := elegirCadencia(fechas, indice, cfg)
	if cadencia == nil {
		return Serie{Indice: indice, UltimoPago: fechas[len(fechas)-1]}
	}
	perdidos, proximo := ciclosYProximo(fechas, montos, hoy, *cadencia, cfg)
	madura := len(fechas) >= cfg.NMin
	var estado Estado
	switch {
	case perdidos == 0 && madura:
		estado = Madura
	case perdidos == 0:
		estado = Emergente
	case !madura:
		estado = ""
	case perdidos >= cfg.CiclosParaCancelar:
		estado = Cancelada
	default:
		estado = Dormida
	}
	serie := Serie{Indice: indice, CiclosPerdidos: perdidos, UltimoPago: fechas[len(fechas)-1]}
	if estado == "" {
		return serie
	}
	serie.Estado = estado
	serie.Cadencia = cadencia.Nombre
	if estado != Cancelada {
		serie.Proximo = proximo
	}
	return serie
}

// DetectarSeries agrupa los pagos por identidad y evalúa cada serie. Devuelve las series ordenadas
// por identidad para que dos corridas con los mismos datos den el mismo resultado.
func DetectarSeries(pagos []Pago, hoy time.Time, cfg ConfiguracionDeRecurrencia) []Serie {
	grupos := map[Identidad][]Pago{}
	for _, pago := range pagos {
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
	series := make([]Serie, 0, len(identidades))
	for _, identidad := range identidades {
		delGrupo := grupos[identidad]
		sort.Slice(delGrupo, func(i, j int) bool {
			if delGrupo[i].OcurridoEn.Equal(delGrupo[j].OcurridoEn) {
				return delGrupo[i].TransaccionID < delGrupo[j].TransaccionID
			}
			return delGrupo[i].OcurridoEn.Before(delGrupo[j].OcurridoEn)
		})
		fechas := make([]time.Time, 0, len(delGrupo))
		montos := make([]int64, 0, len(delGrupo))
		for _, pago := range delGrupo {
			fechas = append(fechas, pago.FechaLocal)
			montos = append(montos, pago.MontoCentavos)
		}
		serie := EvaluarSerie(fechas, montos, hoy, cfg)
		serie.Identidad = identidad
		series = append(series, serie)
	}
	return series
}

type cadencia struct {
	Nombre     string
	Calendario string
	Dias       int
	Meses      int
	Tolerancia int
}

func elegirCadencia(fechas []time.Time, indice Indice, cfg ConfiguracionDeRecurrencia) *cadencia {
	if !indice.EsRecurrente {
		return nil
	}
	posicion := -1
	for i, dias := range cfg.CadenciasDias {
		if dias == indice.CadenciaDias {
			posicion = i
			break
		}
	}
	if posicion < 0 {
		return nil
	}
	nombre := cfg.NombresCadencias[posicion]
	dias := cfg.CadenciasDias[posicion]
	tolerancia := cfg.ToleranciasDias[posicion]
	meses := cfg.MesesPorCadencia[posicion]
	if nombre == "quincenal_14" {
		sobreAncla := 0
		for _, fecha := range fechas {
			if enAnclaDeQuincena(fecha, cfg.TolQuincenaDias) {
				sobreAncla++
			}
		}
		if Fraccion(int64(sobreAncla), int64(len(fechas))).Cmp(cfg.FraccionAnclaQuincena) >= 0 {
			return &cadencia{Nombre: "quincena", Calendario: "quincena", Dias: dias, Tolerancia: cfg.TolQuincenaDias}
		}
	}
	calendario := "dias"
	if meses > 0 {
		calendario = "meses"
	}
	return &cadencia{Nombre: nombre, Calendario: calendario, Dias: dias, Meses: meses, Tolerancia: tolerancia}
}

func fraccionDeMontos(montos []int64, tolerancia *big.Rat) *big.Rat {
	centro := Mediana(montos)
	dentro := 0
	for _, monto := range montos {
		diferencia := Abs(new(big.Rat).Sub(Entero(monto), centro))
		limite := new(big.Rat).Mul(tolerancia, centro)
		if diferencia.Cmp(limite) <= 0 {
			dentro++
		}
	}
	return Fraccion(int64(dentro), int64(len(montos)))
}

func fraccionDeFechas(intervalos []int64, cfg ConfiguracionDeRecurrencia) (*big.Rat, int) {
	mediana := Mediana(intervalos)
	posicion := 0
	for i, dias := range cfg.CadenciasDias {
		actual := Abs(new(big.Rat).Sub(mediana, Entero(int64(dias))))
		mejor := Abs(new(big.Rat).Sub(mediana, Entero(int64(cfg.CadenciasDias[posicion]))))
		if actual.Cmp(mejor) < 0 || (actual.Cmp(mejor) == 0 && dias < cfg.CadenciasDias[posicion]) {
			posicion = i
		}
	}
	cadencia := cfg.CadenciasDias[posicion]
	tolerancia := cfg.ToleranciasDias[posicion]
	dentro := 0
	for _, dias := range intervalos {
		if int(math.Abs(float64(dias)-float64(cadencia))) <= tolerancia {
			dentro++
		}
	}
	return Fraccion(int64(dentro), int64(len(intervalos))), cadencia
}

func ciclosYProximo(fechas []time.Time, montos []int64, hoy time.Time, cad cadencia,
	cfg ConfiguracionDeRecurrencia) (int, *ProximoPago) {
	ancla := anclaDeDia(fechas)
	esperada := fechas[len(fechas)-1]
	perdidos := 0
	var desde, hasta time.Time
	for {
		esperada = siguienteEsperada(esperada, cad, ancla)
		desde, hasta = bandaDeFecha(esperada, cad.Calendario, cad.Tolerancia)
		if !hasta.Before(hoy) {
			break
		}
		perdidos++
		if perdidos >= cfg.CiclosParaCancelar {
			return perdidos, nil
		}
	}
	centro := Mediana(montos)
	desdeMonto := ceilRat(new(big.Rat).Sub(centro, new(big.Rat).Mul(cfg.TolMonto, centro)))
	hastaMonto := floorRat(new(big.Rat).Add(centro, new(big.Rat).Mul(cfg.TolMonto, centro)))
	return perdidos, &ProximoPago{
		Centro:      esperada,
		Desde:       desde,
		Hasta:       hasta,
		MontoCentro: RedondearMitadAPar(centro),
		MontoDesde:  desdeMonto,
		MontoHasta:  hastaMonto,
	}
}

func anclaDeDia(fechas []time.Time) int {
	dias := make([]int64, 0, len(fechas))
	for _, fecha := range fechas {
		dias = append(dias, int64(fecha.In(Zona).Day()))
	}
	return int(RedondearMitadAPar(Mediana(dias)))
}

func siguienteEsperada(dia time.Time, cad cadencia, ancla int) time.Time {
	local := dia.In(Zona)
	switch cad.Calendario {
	case "quincena":
		if local.Day() <= 22 {
			return UltimoDiaDelMes(local.Year(), local.Month())
		}
		siguiente := time.Date(local.Year(), local.Month(), 15, 0, 0, 0, 0, Zona).AddDate(0, 1, 0)
		return siguiente
	case "meses":
		return sumarMeses(local, cad.Meses, ancla)
	default:
		return local.AddDate(0, 0, cad.Dias)
	}
}

func sumarMeses(dia time.Time, meses, ancla int) time.Time {
	total := dia.Year()*12 + int(dia.Month()) - 1 + meses
	anio, mesCero := total/12, total%12
	primero := time.Date(anio, time.Month(mesCero+1), 1, 0, 0, 0, 0, Zona)
	ultimo := UltimoDiaDelMes(anio, primero.Month())
	diaDelMes := ancla
	if diaDelMes > ultimo.Day() {
		diaDelMes = ultimo.Day()
	}
	return time.Date(anio, primero.Month(), diaDelMes, 0, 0, 0, 0, Zona)
}

func bandaDeFecha(esperada time.Time, calendario string, tolerancia int) (time.Time, time.Time) {
	if calendario == "dias" {
		return esperada.AddDate(0, 0, -tolerancia), esperada.AddDate(0, 0, tolerancia)
	}
	return HabilAnterior(esperada).AddDate(0, 0, -tolerancia), HabilSiguiente(esperada).AddDate(0, 0, tolerancia)
}

func enAnclaDeQuincena(dia time.Time, tolerancia int) bool {
	// La comparación es de días civiles: un pago a mediodía del 15 tiene que caer en el ancla del
	// 15, y las bandas de `bandaDeFecha` terminan a medianoche del día hábil.
	local := dia.In(Zona)
	local = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Zona)
	mesAnterior := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, Zona).AddDate(0, -1, 0)
	mesSiguiente := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, Zona).AddDate(0, 1, 0)
	anclas := []time.Time{
		UltimoDiaDelMes(mesAnterior.Year(), mesAnterior.Month()),
		time.Date(local.Year(), local.Month(), 15, 0, 0, 0, 0, Zona),
		UltimoDiaDelMes(local.Year(), local.Month()),
		time.Date(mesSiguiente.Year(), mesSiguiente.Month(), 15, 0, 0, 0, 0, Zona),
	}
	for _, ancla := range anclas {
		desde, hasta := bandaDeFecha(ancla, "quincena", tolerancia)
		if !local.Before(desde) && !local.After(hasta) {
			return true
		}
	}
	return false
}

func diasEntre(a, b time.Time) int {
	inicio := time.Date(a.In(Zona).Year(), a.In(Zona).Month(), a.In(Zona).Day(), 0, 0, 0, 0, Zona)
	fin := time.Date(b.In(Zona).Year(), b.In(Zona).Month(), b.In(Zona).Day(), 0, 0, 0, 0, Zona)
	return int(fin.Sub(inicio) / (24 * time.Hour))
}

func menorUno(valor *big.Rat) *big.Rat {
	if valor.Cmp(Entero(1)) > 0 {
		return Entero(1)
	}
	return valor
}

func floorRat(valor *big.Rat) int64 {
	cociente := new(big.Int).Quo(valor.Num(), valor.Denom())
	if valor.Sign() < 0 && new(big.Int).Rem(valor.Num(), valor.Denom()).Sign() != 0 {
		cociente.Sub(cociente, big.NewInt(1))
	}
	return cociente.Int64()
}

func ceilRat(valor *big.Rat) int64 {
	negado := new(big.Rat).Neg(valor)
	return -floorRat(negado)
}
