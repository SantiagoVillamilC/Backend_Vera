package avisos

import "sort"

// Prioridad es el rango del aviso dentro del cupo. D6 firmó que presupuesto_anticipado compite por
// el mismo cupo de 3. Su rango 4 sale del orden de las tarjetas de la referencia y queda como
// Inferido: si Christian lo mueve, se cambia solo este mapa y las pruebas de orden y de cupo.
func Prioridad(tipo Tipo) int16 {
	switch tipo {
	case PagoNoAlcanza:
		return 1
	case CaidaIngreso:
		return 2
	case CobroNuevo:
		return 3
	case PresupuestoAnticipado:
		return 4
	case CategoriaSobreMedia:
		return 5
	default:
		return 0
	}
}

// Orden ordena por prioridad ascendente, severidad descendente y clave ascendente. La lista ya
// deduplicada se recorta después, nunca antes.
func Orden(tarjetas []Tarjeta) []Tarjeta {
	ordenadas := append([]Tarjeta(nil), tarjetas...)
	sort.SliceStable(ordenadas, func(i, j int) bool {
		pi, pj := Prioridad(ordenadas[i].Tipo), Prioridad(ordenadas[j].Tipo)
		if pi != pj {
			return pi < pj
		}
		if SeveridadDe(ordenadas[i]) != SeveridadDe(ordenadas[j]) {
			return SeveridadDe(ordenadas[i]) > SeveridadDe(ordenadas[j])
		}
		if ordenadas[i].Clave != ordenadas[j].Clave {
			return ordenadas[i].Clave < ordenadas[j].Clave
		}
		return ordenadas[i].Tipo < ordenadas[j].Tipo
	})
	return ordenadas
}

// Tope aplica el cupo mensual a una lista ya ordenada. Un cupo negativo cuenta como cero y el
// resultado es siempre un subconjunto de la lista, de modo que un cupo menor recorta más.
func Tope(tarjetas []Tarjeta, emitidos, tope int) []Tarjeta {
	cupo := tope - emitidos
	if cupo < 0 {
		cupo = 0
	}
	if cupo >= len(tarjetas) {
		return tarjetas
	}
	return tarjetas[:cupo]
}

// SeveridadDe es la diferencia en centavos que ordena un aviso dentro de su prioridad. Nunca es
// negativa, porque la columna de la tabla tiene un CHECK mayor o igual que cero.
func SeveridadDe(tarjeta Tarjeta) int64 {
	referencia := tarjeta.MontoCentavos
	if tarjeta.ReferenciaCentavos != nil {
		referencia = *tarjeta.ReferenciaCentavos
	}
	switch tarjeta.Tipo {
	case PagoNoAlcanza:
		if referencia > tarjeta.MontoCentavos {
			return referencia - tarjeta.MontoCentavos
		}
		return tarjeta.MontoCentavos - referencia
	case CobroNuevo:
		return tarjeta.MontoCentavos
	default:
		if tarjeta.MontoCentavos > referencia {
			return tarjeta.MontoCentavos - referencia
		}
		return referencia - tarjeta.MontoCentavos
	}
}

// Recuento describe qué pasó con las tarjetas de un usuario antes y después del cupo. Es la única
// fuente del número que el cupo recorta: sin él, un aviso suprimido no deja rastro.
type Recuento struct {
	Hechos     int
	YaEmitidas int
	Candidatas int
	Elegidas   int
	Recortadas int
}

// TarjetasDelDia evalúa los hechos, arma las tarjetas, verifica los textos, descarta las ya emitidas,
// ordena y aplica el cupo. Un texto que no reproduce sus cifras es un error de programación, no de
// datos: la corrida falla cerrada y no se escribe nada.
func TarjetasDelDia(hechos []Hecho, yaEmitidas map[ClaveDeAviso]struct{}, emitidos, tope int) ([]Tarjeta, error) {
	tarjetas, _, err := TarjetasDelDiaConRecuento(hechos, yaEmitidas, emitidos, tope)
	return tarjetas, err
}

// TarjetasDelDiaConRecuento hace lo mismo que TarjetasDelDia y además informa cuántas tarjetas
// quedaron fuera por deduplicación y cuántas por el cupo. La lógica de orden y de tope no cambia.
func TarjetasDelDiaConRecuento(hechos []Hecho, yaEmitidas map[ClaveDeAviso]struct{}, emitidos,
	tope int) ([]Tarjeta, Recuento, error) {
	recuento := Recuento{Hechos: len(hechos)}
	tarjetas := make([]Tarjeta, 0, len(hechos))
	for _, hecho := range hechos {
		if _, existe := yaEmitidas[Clave(hecho)]; existe {
			recuento.YaEmitidas++
			continue
		}
		tarjeta, err := TarjetaDe(hecho)
		if err != nil {
			return nil, Recuento{}, err
		}
		if reproduce, motivo := VerificarTarjeta(tarjeta); !reproduce {
			return nil, Recuento{}, &ErrorDeTexto{Tipo: tarjeta.Tipo, Motivo: motivo}
		}
		tarjetas = append(tarjetas, tarjeta)
	}
	recuento.Candidatas = len(tarjetas)
	elegidas := Tope(Orden(tarjetas), emitidos, tope)
	recuento.Elegidas = len(elegidas)
	recuento.Recortadas = recuento.Candidatas - recuento.Elegidas
	return elegidas, recuento, nil
}

// ClaveDeAviso es la identidad de deduplicación de la tabla aviso: tipo, clave y período.
type ClaveDeAviso struct {
	Tipo    Tipo
	Clave   string
	Periodo string
}

// Clave devuelve la identidad de un hecho, con el período en formato de fecha.
func Clave(hecho Hecho) ClaveDeAviso {
	periodo := ""
	if hecho.Mes != nil {
		periodo = hecho.Mes.Fecha().Format("2006-01-02")
	}
	if hecho.Fecha != nil {
		periodo = fechaDe(*hecho.Fecha)
	}
	return ClaveDeAviso{Tipo: hecho.Tipo, Clave: hecho.Clave, Periodo: periodo}
}

// ErrorDeTexto reporta una plantilla que no reproduce sus cifras.
type ErrorDeTexto struct {
	Tipo   Tipo
	Motivo string
}

func (e *ErrorDeTexto) Error() string {
	return "avisos: la plantilla de " + string(e.Tipo) + " no reproduce sus cifras: " + e.Motivo
}
