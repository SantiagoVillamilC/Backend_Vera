// Package avisos contiene la lógica pura de los cinco avisos: estadística exacta, detector de
// recurrencia, detectores, textos y verificador, y el orden con el cupo mensual.
//
// No abre archivos, no mira el reloj del sistema y no llama a la red: todo entra por parámetro. Los
// montos son centavos enteros y las comparaciones usan big.Rat, porque en coma flotante 0,6 por 1
// más 0,4 por 0,5 no vale exactamente 0,8 y una regla que cae justo sobre el umbral cambiaría de
// decisión por un error de redondeo.
package avisos

import (
	"math/big"
	"time"
)

// Zona es el huso fijo de Colombia. No tiene horario de verano, así que un desfase fijo es exacto.
var Zona = time.FixedZone("America/Bogota", -5*60*60)

// Tipo es uno de los cinco avisos del producto.
type Tipo string

// Los cinco tipos de aviso.
const (
	PagoNoAlcanza         Tipo = "pago_no_alcanza"
	CaidaIngreso          Tipo = "caida_ingreso"
	CobroNuevo            Tipo = "cobro_nuevo"
	PresupuestoAnticipado Tipo = "presupuesto_anticipado"
	CategoriaSobreMedia   Tipo = "categoria_sobre_media"
)

// Tipos son los cinco avisos, en el orden de la referencia.
var Tipos = []Tipo{
	CategoriaSobreMedia, PresupuestoAnticipado, CaidaIngreso, PagoNoAlcanza, CobroNuevo,
}

// Causa explica por qué un detector decidió lo que decidió.
type Causa string

// Las causas de la referencia. Un detector que no dispara también informa su causa.
const (
	CausaHistorialInsuficiente Causa = "historial_insuficiente"
	CausaSinCategoriaEvaluable Causa = "sin_categoria_evaluable"
	CausaR1                    Causa = "r1"
	CausaZSobreElUmbral        Causa = "z_sobre_el_umbral"
	CausaZBajoElUmbral         Causa = "z_bajo_el_umbral"
	CausaRespaldoR1MADcero     Causa = "respaldo_r1_mad_cero"
	CausaGuardiaCVRobusto      Causa = "guardia_cv_robusto"
	CausaSinSeries             Causa = "sin_series"
	CausaSinPagoEnLaVentana    Causa = "sin_pago_en_la_ventana"
	CausaPagoEnLaVentana       Causa = "pago_en_la_ventana"
	CausaLlaveNueva            Causa = "llave_nueva"
	CausaLlaveNuevaSinSerie    Causa = "llave_nueva_sin_serie"
	CausaSinLlaveNueva         Causa = "sin_llave_nueva"
)

// Regla y línea base son las dos variantes que la referencia evalúa y compara.
type Regla string

// LineaBase es la referencia contra la que se compara el valor del mes.
type LineaBase string

// Las dos reglas y las dos líneas base que la referencia evalúa.
const (
	ReglaR1 Regla = "R1"
	ReglaR2 Regla = "R2"

	LineaMediana LineaBase = "mediana"
	LineaMedia   LineaBase = "media"
)

// ReglaViva y LineaBaseViva son las que usa el job. R2 queda implementada y probada, pero el punto
// 347 y los textos firmados se calcularon con R1 sobre la mediana.
const (
	ReglaViva     = ReglaR1
	LineaBaseViva = LineaMediana
)

// Estado es el de una serie recurrente del detector.
type Estado string

// Los estados de una serie. El vacío significa que no es recurrente.
const (
	Emergente Estado = "emergente"
	Madura    Estado = "madura"
	Dormida   Estado = "dormida"
	Cancelada Estado = "cancelada"
)

// Mes es un mes calendario. Es comparable, así que sirve de clave de mapa sin orden implícito.
type Mes struct {
	Anio int
	Mes  time.Month
}

// MesDe devuelve el mes al que pertenece un instante, ya en la zona local.
func MesDe(instante time.Time) Mes {
	local := instante.In(Zona)
	return Mes{Anio: local.Year(), Mes: local.Month()}
}

// Fecha devuelve el primer día del mes en la zona local.
func (m Mes) Fecha() time.Time {
	return time.Date(m.Anio, m.Mes, 1, 0, 0, 0, 0, Zona)
}

// Siguiente devuelve el mes siguiente.
func (m Mes) Siguiente() Mes {
	if m.Mes == time.December {
		return Mes{Anio: m.Anio + 1, Mes: time.January}
	}
	return Mes{Anio: m.Anio, Mes: m.Mes + 1}
}

// Anterior devuelve el mes anterior.
func (m Mes) Anterior() Mes {
	if m.Mes == time.January {
		return Mes{Anio: m.Anio - 1, Mes: time.December}
	}
	return Mes{Anio: m.Anio, Mes: m.Mes - 1}
}

// Antes informa si el mes es anterior al otro.
func (m Mes) Antes(other Mes) bool {
	return m.Anio < other.Anio || (m.Anio == other.Anio && m.Mes < other.Mes)
}

// Clave devuelve el mes como YYYY-MM.
func (m Mes) Clave() string {
	return m.Fecha().Format("2006-01")
}

// FinDelDiaLocal devuelve el último segundo de una fecha de Colombia como instante UTC.
func FinDelDiaLocal(fecha time.Time) time.Time {
	local := time.Date(fecha.Year(), fecha.Month(), fecha.Day(), 0, 0, 0, 0, Zona)
	return local.AddDate(0, 0, 1).Add(-time.Second)
}

// Identidad es el destino de un pago: la llave del comercio o el nombre normalizado de la persona.
type Identidad struct {
	Tipo  string
	Valor string
}

// Pago es una salida de la copia enriquecida, ya reducida a lo que los detectores miran.
type Pago struct {
	Identidad     Identidad
	OcurridoEn    time.Time
	FechaLocal    time.Time
	MontoCentavos int64
	TransaccionID string
	Categoria     string
}

// Ingreso es una entrada de dinero del usuario.
type Ingreso struct {
	OcurridoEn    time.Time
	FechaLocal    time.Time
	MontoCentavos int64
}

// Instantaneo es lo único que viene de la base: el gasto ya agregado por categoría y mes, el ingreso
// por mes, los pagos que alimentan las series y el saldo. Las series se calculan aquí.
type Instantaneo struct {
	Pagos          []Pago
	GastoCategoria map[string]map[Mes]int64
	IngresoMes     map[Mes]int64
	Primero        *time.Time
	SaldoCentavos  int64
}

// Indice es el resultado del detector de recurrencia para una identidad.
type Indice struct {
	Ocurrencias  int
	N            *big.Rat
	Monto        *big.Rat
	Fecha        *big.Rat
	R            *big.Rat
	EsRecurrente bool
	CadenciaDias int
}

// ProximoPago es la banda del siguiente pago esperado. Los extremos son inclusivos.
type ProximoPago struct {
	Centro      time.Time
	Desde       time.Time
	Hasta       time.Time
	MontoCentro int64
	MontoDesde  int64
	MontoHasta  int64
}

// Serie es una identidad recurrente con su estado y su próxima banda.
type Serie struct {
	Identidad      Identidad
	Estado         Estado
	Indice         Indice
	Cadencia       string
	CiclosPerdidos int
	Proximo        *ProximoPago
	UltimoPago     time.Time
}

// Contexto es lo que el usuario tenía hasta el instante de la evaluación.
type Contexto struct {
	Ahora          time.Time
	Hoy            time.Time
	MesActual      Mes
	MesEvaluado    Mes
	PrimeroLocal   *time.Time
	Completos      []Mes
	GastoMes       map[Mes]int64
	GastoCategoria map[string]map[Mes]int64
	IngresoMes     map[Mes]int64
	SaldoCentavos  int64
	Pagos          []Pago
	Series         []Serie
	Cfg            Configuracion
}

// Decision es el resultado de un detector. Los montos van en centavos y en exacto.
type Decision struct {
	Tipo        Tipo
	Dispara     bool
	Causa       Causa
	Valor       *big.Rat
	Referencia  *big.Rat
	Estadistico *big.Rat
	Detalle     string
	Fecha       *time.Time
}

// Hecho es la decisión ya con las cifras redondeadas y su identidad de deduplicación.
type Hecho struct {
	Tipo               Tipo
	Causa              Causa
	Clave              string
	MontoCentavos      int64
	ReferenciaCentavos *int64
	Fecha              *time.Time
	Mes                *Mes
	SeveridadCentavos  int64
	Detalle            string
}

// Tarjeta es lo que se muestra: título, texto, porqué y acción.
type Tarjeta struct {
	Tipo               Tipo
	Titulo             string
	Texto              string
	Porque             string
	Accion             string
	MontoCentavos      int64
	ReferenciaCentavos *int64
	Fecha              *time.Time
	Mes                *Mes
	Clave              string
	Periodo            time.Time
	Causa              Causa
}
