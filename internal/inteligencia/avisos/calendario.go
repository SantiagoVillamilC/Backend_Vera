package avisos

import "time"

// MesesCompletosAntes devuelve los meses completos del usuario anteriores al límite, en orden. Un
// mes es completo si empezó el día 1 y ya terminó. Sin primer movimiento no hay ninguno.
func MesesCompletosAntes(primero *time.Time, limite Mes) []Mes {
	if primero == nil {
		return nil
	}
	local := primero.In(Zona)
	mes := Mes{Anio: local.Year(), Mes: local.Month()}
	if local.Day() != 1 {
		mes = mes.Siguiente()
	}
	var meses []Mes
	for mes.Antes(limite) {
		meses = append(meses, mes)
		mes = mes.Siguiente()
	}
	return meses
}

// UltimoDiaDelMes devuelve el último día del mes en la zona local.
func UltimoDiaDelMes(anio int, mes time.Month) time.Time {
	return time.Date(anio, mes, 1, 0, 0, 0, 0, Zona).AddDate(0, 1, -1)
}

// DomingoDePascua usa el algoritmo gregoriano anónimo de Meeus, Jones y Butcher.
func DomingoDePascua(anio int) time.Time {
	a := anio % 19
	b, c := anio/100, anio%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	ele := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*ele) / 451
	mes, dia := (h+ele-7*m+114)/31, (h+ele-7*m+114)%31
	return time.Date(anio, time.Month(mes), dia+1, 0, 0, 0, 0, Zona)
}

var (
	fijosSinTraslado           = [][2]int{{1, 1}, {5, 1}, {7, 20}, {8, 7}, {12, 8}, {12, 25}}
	fijosAlLunes               = [][2]int{{1, 6}, {3, 19}, {6, 29}, {8, 15}, {10, 12}, {11, 1}, {11, 11}}
	diasDesdePascuaSinTraslado = []int{-3, -2}
	diasDesdePascuaAlLunes     = []int{39, 60, 68}
)

// AlLunesSiguiente devuelve el mismo día si es lunes y, si no, el lunes que sigue.
func AlLunesSiguiente(dia time.Time) time.Time {
	dias := (int(time.Monday) - int(dia.Weekday()) + 7) % 7
	return dia.AddDate(0, 0, dias)
}

// Festivos devuelve los festivos de Colombia de un año, según la Ley 51 de 1983.
func Festivos(anio int) map[string]struct{} {
	festivos := make(map[string]struct{})
	agregar := func(dia time.Time) { festivos[dia.Format("2006-01-02")] = struct{}{} }
	for _, par := range fijosSinTraslado {
		agregar(time.Date(anio, time.Month(par[0]), par[1], 0, 0, 0, 0, Zona))
	}
	for _, par := range fijosAlLunes {
		agregar(AlLunesSiguiente(time.Date(anio, time.Month(par[0]), par[1], 0, 0, 0, 0, Zona)))
	}
	pascua := DomingoDePascua(anio)
	for _, desfase := range diasDesdePascuaSinTraslado {
		agregar(pascua.AddDate(0, 0, desfase))
	}
	for _, desfase := range diasDesdePascuaAlLunes {
		agregar(AlLunesSiguiente(pascua.AddDate(0, 0, desfase)))
	}
	return festivos
}

// EsFestivo informa si el día es festivo en Colombia.
func EsFestivo(dia time.Time) bool {
	_, existe := Festivos(dia.In(Zona).Year())[dia.In(Zona).Format("2006-01-02")]
	return existe
}

// EsHabil informa si el día es de lunes a viernes y no es festivo. El sábado no es hábil.
func EsHabil(dia time.Time) bool {
	local := dia.In(Zona)
	return local.Weekday() != time.Saturday && local.Weekday() != time.Sunday && !EsFestivo(local)
}

// HabilAnterior devuelve el mismo día si es hábil y, si no, el hábil inmediatamente anterior.
func HabilAnterior(dia time.Time) time.Time {
	for !EsHabil(dia) {
		dia = dia.AddDate(0, 0, -1)
	}
	return dia
}

// HabilSiguiente devuelve el mismo día si es hábil y, si no, el hábil inmediatamente siguiente.
func HabilSiguiente(dia time.Time) time.Time {
	for !EsHabil(dia) {
		dia = dia.AddDate(0, 0, 1)
	}
	return dia
}
