package avisos

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// ConfiguracionDeRecurrencia replica el bloque de `analisis/config/recurrencia_estados.json`. Los
// valores son de diseño y no tienen fuente: la referencia los declara propuesta sin medir.
type ConfiguracionDeRecurrencia struct {
	WMonto, WFecha, TolMonto, Umbral, FraccionAnclaQuincena *big.Rat
	CadenciasDias                                           []int
	ToleranciasDias                                         []int
	MesesPorCadencia                                        []int
	NombresCadencias                                        []string
	NMin                                                    int
	TolQuincenaDias                                         int
	CiclosParaCancelar                                      int
}

// Configuracion reúne los índices de las reglas. Los decimales son exactos.
type Configuracion struct {
	MesesMinimos           int
	VentanaPagoDias        int
	VentanaCobroDias       int
	PagosMinimosCobro      int
	MultiplicadorGasto     *big.Rat
	CaidaD                 *big.Rat
	CVRobustoMax           *big.Rat
	ZFactor                *big.Rat
	ZUmbral                *big.Rat
	TolMontoCobro          *big.Rat
	ProrrateoAcumulado     bool
	TopeAvisosMes          int
	TiposAsientoGasto      []string
	TiposAsientoIngreso    []string
	DescripcionesNoIngreso []string
	Regla                  Regla
	LineaBase              LineaBase
	Recurrencia            ConfiguracionDeRecurrencia
}

// Rat construye un racional exacto a partir de su texto. Un error de programación es pánico: la
// configuración es estática y una cifra ilegible no puede degradarse a cero en silencio.
func Rat(texto string) *big.Rat {
	valor, ok := new(big.Rat).SetString(texto)
	if !ok {
		panic(fmt.Sprintf("avisos: %q no es un decimal legible", texto))
	}
	return valor
}

// Entero construye un racional entero.
func Entero(valor int64) *big.Rat {
	return new(big.Rat).SetInt64(valor)
}

// ConfiguracionPorDefecto devuelve los valores de `analisis/config/avisos.json` y de
// `analisis/config/recurrencia_estados.json`, con la regla viva y la línea base viva.
func ConfiguracionPorDefecto() Configuracion {
	return Configuracion{
		MesesMinimos:           3,
		VentanaPagoDias:        7,
		VentanaCobroDias:       35,
		PagosMinimosCobro:      2,
		MultiplicadorGasto:     Rat("1.0"),
		CaidaD:                 Rat("0.3"),
		CVRobustoMax:           Rat("0.25"),
		ZFactor:                Rat("0.6745"),
		ZUmbral:                Rat("3.5"),
		TolMontoCobro:          Rat("0.15"),
		ProrrateoAcumulado:     false,
		TopeAvisosMes:          3,
		TiposAsientoGasto:      []string{"liquidacion_confirmada"},
		TiposAsientoIngreso:    []string{"deposito"},
		DescripcionesNoIngreso: []string{"Saldo inicial"},
		Regla:                  ReglaViva,
		LineaBase:              LineaBaseViva,
		Recurrencia: ConfiguracionDeRecurrencia{
			WMonto:                Rat("0.6"),
			WFecha:                Rat("0.4"),
			TolMonto:              Rat("0.1"),
			Umbral:                Rat("0.5"),
			FraccionAnclaQuincena: Rat("0.8"),
			CadenciasDias:         []int{7, 14, 30, 61, 182},
			ToleranciasDias:       []int{2, 3, 4, 5, 10},
			MesesPorCadencia:      []int{0, 0, 1, 2, 6},
			NombresCadencias:      []string{"semanal", "quincenal_14", "mensual", "bimestral", "semestral"},
			NMin:                  3,
			TolQuincenaDias:       1,
			CiclosParaCancelar:    2,
		},
	}
}

// Validar comprueba lo que la referencia valida y junta todos los problemas en un solo error.
func (c Configuracion) Validar() error {
	var problemas []string
	for nombre, valor := range map[string]int{
		"meses_minimos":       c.MesesMinimos,
		"ventana_pago_dias":   c.VentanaPagoDias,
		"ventana_cobro_dias":  c.VentanaCobroDias,
		"pagos_minimos_cobro": c.PagosMinimosCobro,
		"tope_avisos_mes":     c.TopeAvisosMes,
	} {
		if valor < 1 {
			problemas = append(problemas, fmt.Sprintf("%s debe ser mayor o igual que 1", nombre))
		}
	}
	for nombre, valor := range map[string]*big.Rat{
		"multiplicador_gasto": c.MultiplicadorGasto,
		"caida_d":             c.CaidaD,
		"cv_robusto_max":      c.CVRobustoMax,
		"z_factor":            c.ZFactor,
		"z_umbral":            c.ZUmbral,
		"tol_monto_cobro":     c.TolMontoCobro,
	} {
		if valor == nil || valor.Sign() < 0 {
			problemas = append(problemas, fmt.Sprintf("%s debe ser mayor o igual que 0", nombre))
		}
	}
	if c.Regla != ReglaR1 && c.Regla != ReglaR2 {
		problemas = append(problemas, "regla debe ser R1 o R2")
	}
	if c.LineaBase != LineaMediana && c.LineaBase != LineaMedia {
		problemas = append(problemas, "linea_base debe ser mediana o media")
	}
	if err := c.Recurrencia.Validar(); err != nil {
		problemas = append(problemas, err.Error())
	}
	if len(problemas) > 0 {
		return errors.New("avisos: configuración inválida: " + strings.Join(problemas, ", "))
	}
	return nil
}

// Validar comprueba el bloque de recurrencia.
func (c ConfiguracionDeRecurrencia) Validar() error {
	var problemas []string
	if len(c.CadenciasDias) != len(c.ToleranciasDias) || len(c.CadenciasDias) != len(c.MesesPorCadencia) ||
		len(c.CadenciasDias) != len(c.NombresCadencias) {
		problemas = append(problemas, "las listas de cadencias deben tener el mismo largo")
	}
	if c.NMin < 1 {
		problemas = append(problemas, "n_min debe ser mayor o igual que 1")
	}
	if c.CiclosParaCancelar < 2 {
		problemas = append(problemas, "ciclos_para_cancelar debe ser mayor o igual que 2")
	}
	if c.Umbral == nil || c.Umbral.Sign() <= 0 || c.Umbral.Cmp(Entero(1)) > 0 {
		problemas = append(problemas, "umbral debe estar en (0, 1]")
	}
	if c.WMonto == nil || c.WFecha == nil ||
		new(big.Rat).Add(c.WMonto, c.WFecha).Cmp(Entero(1)) != 0 {
		problemas = append(problemas, "los pesos de monto y fecha deben sumar exactamente 1")
	}
	if len(problemas) > 0 {
		return errors.New(strings.Join(problemas, ", "))
	}
	return nil
}
