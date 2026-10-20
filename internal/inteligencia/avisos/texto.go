package avisos

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Las etiquetas de categoría y las acciones son las firmadas por D4 el 20 de octubre de 2026. Se
// portan literalmente desde `analisis/avisos_experiencia.py`, con sus tildes.
var etiquetasCategoria = map[string]string{
	"salario":                       "Salario",
	"transferencia_recibida":        "Transferencia recibida",
	"reembolso":                     "Reembolso",
	"otros_ingresos":                "Otros ingresos",
	"alimentos_mercado":             "Alimentos y mercado",
	"restaurantes_domicilios":       "Restaurantes y domicilios",
	"transporte":                    "Transporte",
	"vivienda_servicios":            "Vivienda y servicios",
	"salud":                         "Salud",
	"educacion":                     "Educación",
	"entretenimiento":               "Entretenimiento",
	"compras":                       "Compras",
	"comisiones_costos_financieros": "Comisiones y costos financieros",
	"otros_gastos":                  "Otros gastos",
}

var acciones = map[Tipo]string{
	PagoNoAlcanza:         "Ver los pagos que vencen",
	CaidaIngreso:          "Ver mis ingresos",
	CobroNuevo:            "Ver el cobro y su próximo vencimiento",
	PresupuestoAnticipado: "Ver los gastos de este mes",
	CategoriaSobreMedia:   "Ver los movimientos de esta categoría",
}

var porques = map[Tipo]string{
	PagoNoAlcanza:         "Los pagos recurrentes de la ventana suman más que tu saldo.",
	CaidaIngreso:          "El ingreso del último mes completo quedó por debajo de lo habitual.",
	CobroNuevo:            "Apareció una llave recurrente que no estaba en tu historial.",
	PresupuestoAnticipado: "El gasto acumulado del mes supera lo habitual de tus meses completos.",
	CategoriaSobreMedia:   "El acumulado de esta categoría supera lo habitual de tus meses completos.",
}

var mesesLargos = []string{
	"enero", "febrero", "marzo", "abril", "mayo", "junio",
	"julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre",
}

var (
	fechaISO        = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	numero          = regexp.MustCompile(`\d[\d.,]*`)
	formaNumero     = regexp.MustCompile(`^\d{1,3}(\.\d{3})*(,\d+)?$|^\d+(,\d+)?$`)
	fraseMes        = regexp.MustCompile(`\b(` + strings.Join(mesesLargos, "|") + `) de (\d{4})\b`)
	claveIdentifica = regexp.MustCompile(`[0-9_\-]`)
)

// Pesos escribe los centavos como pesos con punto de miles y coma decimal solo si hay centavos.
func Pesos(centavos int64) string {
	signo := ""
	if centavos < 0 {
		signo = "-"
		centavos = -centavos
	}
	entero, resto := centavos/100, centavos%100
	texto := agruparMiles(entero)
	if resto == 0 {
		return fmt.Sprintf("%s$%s", signo, texto)
	}
	return fmt.Sprintf("%s$%s,%02d", signo, texto, resto)
}

func agruparMiles(valor int64) string {
	digitos := strconv.FormatInt(valor, 10)
	var partes []string
	for len(digitos) > 3 {
		partes = append([]string{digitos[len(digitos)-3:]}, partes...)
		digitos = digitos[:len(digitos)-3]
	}
	partes = append([]string{digitos}, partes...)
	return strings.Join(partes, ".")
}

// EtiquetaDeCategoria devuelve el nombre visible de la categoría. Un código desconocido se muestra
// tal cual, igual que la referencia.
func EtiquetaDeCategoria(codigo string) string {
	if etiqueta, existe := etiquetasCategoria[codigo]; existe {
		return etiqueta
	}
	return codigo
}

// TarjetaDe arma la tarjeta de un hecho. Solo afirma lo calculado.
func TarjetaDe(hecho Hecho) (Tarjeta, error) {
	if hecho.Mes != nil && hecho.Fecha != nil {
		return Tarjeta{}, errors.New("avisos: un aviso se refiere a un mes o a una fecha, no a los dos")
	}
	monto := Pesos(hecho.MontoCentavos)
	var texto, titulo string
	switch hecho.Tipo {
	case PresupuestoAnticipado:
		if hecho.Mes == nil || hecho.ReferenciaCentavos == nil {
			return Tarjeta{}, errors.New("avisos: presupuesto_anticipado sin mes o sin referencia")
		}
		texto = fmt.Sprintf("En %s llevas %s en gastos. Lo habitual en tus meses completos es %s.",
			mesLargo(*hecho.Mes), monto, Pesos(*hecho.ReferenciaCentavos))
		titulo = "Tu gasto va por encima de lo habitual"
	case CategoriaSobreMedia:
		if hecho.Mes == nil || hecho.ReferenciaCentavos == nil {
			return Tarjeta{}, errors.New("avisos: categoria_sobre_media sin mes o sin referencia")
		}
		etiqueta := EtiquetaDeCategoria(hecho.Clave)
		texto = fmt.Sprintf("En %s llevas %s en %s. Lo habitual en esa categoría es %s.",
			mesLargo(*hecho.Mes), monto, etiqueta, Pesos(*hecho.ReferenciaCentavos))
		titulo = fmt.Sprintf("Gastas más en %s", etiqueta)
	case CaidaIngreso:
		if hecho.Mes == nil || hecho.ReferenciaCentavos == nil {
			return Tarjeta{}, errors.New("avisos: caida_ingreso sin mes o sin referencia")
		}
		texto = fmt.Sprintf("El ingreso de %s fue %s. Lo habitual en tus meses anteriores es %s.",
			mesLargo(*hecho.Mes), monto, Pesos(*hecho.ReferenciaCentavos))
		titulo = "Tu ingreso bajó"
	case PagoNoAlcanza:
		if hecho.Fecha == nil || hecho.ReferenciaCentavos == nil {
			return Tarjeta{}, errors.New("avisos: pago_no_alcanza sin fecha o sin referencia")
		}
		texto = fmt.Sprintf("Tu saldo es %s y los pagos recurrentes que vencen desde %s suman %s.",
			monto, fechaDe(*hecho.Fecha), Pesos(*hecho.ReferenciaCentavos))
		titulo = "Tus próximos pagos suman más que tu saldo"
	case CobroNuevo:
		if hecho.Fecha == nil {
			return Tarjeta{}, errors.New("avisos: cobro_nuevo sin fecha")
		}
		texto = fmt.Sprintf("Apareció un cobro de %s el %s y ya se repite o está declarado como recurrente.",
			monto, fechaDe(*hecho.Fecha))
		titulo = "Apareció un cobro nuevo"
	default:
		return Tarjeta{}, errors.New("avisos: tipo de aviso desconocido")
	}
	periodo := time.Time{}
	if hecho.Mes != nil {
		periodo = hecho.Mes.Fecha()
	}
	if hecho.Fecha != nil {
		periodo = inicioDelDia(*hecho.Fecha)
	}
	return Tarjeta{
		Tipo: hecho.Tipo, Titulo: titulo, Texto: texto, Porque: porques[hecho.Tipo],
		Accion: acciones[hecho.Tipo], MontoCentavos: hecho.MontoCentavos,
		ReferenciaCentavos: hecho.ReferenciaCentavos, Fecha: hecho.Fecha, Mes: hecho.Mes,
		Clave: hecho.Clave, Periodo: periodo, Causa: hecho.Causa,
	}, nil
}

// VerificarTarjeta comprueba que el texto solo afirme lo calculado y que no filtre la clave.
func VerificarTarjeta(tarjeta Tarjeta) (bool, string) {
	if tarjeta.Accion == "" || tarjeta.Accion != acciones[tarjeta.Tipo] {
		return false, "accion_ausente"
	}
	if contieneEstiloProhibido(tarjeta.Texto) || contieneEstiloProhibido(tarjeta.Porque) {
		return false, "estilo_prohibido"
	}
	if tarjeta.Clave != "" && claveIdentifica.MatchString(tarjeta.Clave) &&
		strings.Contains(tarjeta.Texto, tarjeta.Clave) {
		return false, "clave_en_el_texto"
	}
	limpio := tarjeta.Texto
	if tarjeta.Mes != nil {
		frases := fraseMes.FindAllStringSubmatch(tarjeta.Texto, -1)
		esperada := []string{mesesLargos[tarjeta.Mes.Mes-1], strconv.Itoa(tarjeta.Mes.Anio)}
		if len(frases) != 1 ||
			frases[0][1] != esperada[0] || frases[0][2] != esperada[1] {
			return false, "mes_ausente_o_ajeno"
		}
		limpio = fraseMes.ReplaceAllString(tarjeta.Texto, " ")
	}
	montos := []int64{tarjeta.MontoCentavos}
	if tarjeta.ReferenciaCentavos != nil {
		montos = append(montos, *tarjeta.ReferenciaCentavos)
	}
	return verificarTexto(limpio, montos, tarjeta.Fecha)
}

func verificarTexto(texto string, montos []int64, fecha *time.Time) (bool, string) {
	fechas := fechaISO.FindAllString(texto, -1)
	if fecha == nil && len(fechas) > 0 {
		return false, "fecha_ajena"
	}
	if fecha != nil && len(fechas) == 0 {
		return false, "fecha_ausente"
	}
	if fecha != nil {
		esperada := fechaDe(*fecha)
		for _, encontrada := range fechas {
			if encontrada != esperada {
				return false, "fecha_ajena"
			}
		}
	}
	sinFechas := fechaISO.ReplaceAllString(texto, " ")
	encontrados := []int64{}
	for _, crudo := range numero.FindAllString(sinFechas, -1) {
		centavos, valido := aCentavos(crudo)
		if !valido {
			return false, "numero_inesperado"
		}
		encontrados = append(encontrados, centavos)
	}
	for _, valor := range encontrados {
		if !contieneMonto(montos, valor) {
			return false, "monto_ajeno"
		}
	}
	for _, monto := range montos {
		if !contieneMonto(encontrados, monto) {
			return false, "monto_ausente"
		}
	}
	return true, "ok"
}

func aCentavos(texto string) (int64, bool) {
	limpio := strings.TrimRight(texto, ".,")
	if limpio == "" || !formaNumero.MatchString(limpio) {
		return 0, false
	}
	sinMiles := strings.ReplaceAll(limpio, ".", "")
	conPunto := strings.ReplaceAll(sinMiles, ",", ".")
	valor, ok := new(big.Rat).SetString(conPunto)
	if !ok {
		return 0, false
	}
	centavos := new(big.Rat).Mul(valor, Entero(100))
	if !centavos.IsInt() {
		return 0, false
	}
	return centavos.Num().Int64(), true
}

func contieneMonto(valores []int64, buscado int64) bool {
	for _, valor := range valores {
		if valor == buscado {
			return true
		}
	}
	return false
}

func contieneEstiloProhibido(texto string) bool {
	return strings.ContainsAny(texto, ";\u2014\u2013")
}

func mesLargo(mes Mes) string {
	return fmt.Sprintf("%s de %d", mesesLargos[mes.Mes-1], mes.Anio)
}

func fechaDe(fecha time.Time) string {
	return fecha.In(Zona).Format("2006-01-02")
}

func inicioDelDia(fecha time.Time) time.Time {
	local := fecha.In(Zona)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Zona)
}

// TituloDe devuelve el título de un aviso sin mirar sus cifras. Se deriva en la lectura y no se
// persiste, para que un cambio de título no altere la deduplicación.
func TituloDe(tipo Tipo, clave string) string {
	switch tipo {
	case PresupuestoAnticipado:
		return "Tu gasto va por encima de lo habitual"
	case CategoriaSobreMedia:
		return fmt.Sprintf("Gastas más en %s", EtiquetaDeCategoria(clave))
	case CaidaIngreso:
		return "Tu ingreso bajó"
	case PagoNoAlcanza:
		return "Tus próximos pagos suman más que tu saldo"
	case CobroNuevo:
		return "Apareció un cobro nuevo"
	default:
		return ""
	}
}

// PorqueDe devuelve la causa legible del tipo.
func PorqueDe(tipo Tipo) string { return porques[tipo] }

// AccionDe devuelve la acción sugerida del tipo.
func AccionDe(tipo Tipo) string { return acciones[tipo] }
