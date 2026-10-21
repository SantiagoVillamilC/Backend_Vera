package inteligencia

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	cuentaGMFPorPagar      = "pasivo:gmf_por_pagar"
	cuentaComision         = "ingreso:comision"
	prefijoCustodia        = "activo:custodia:"
	prefijoLiquidacion     = "pasivo:liquidacion_pendiente:"
	prefijoCuentaDeUsuario = "pasivo:usuario:"

	textoUsuarioDeLaBilletera = "Usuario de la billetera"
	textoComercioExterno      = "Comercio o red externa"
	textoBancoCustodio        = "Banco custodio"
	textoGMF                  = "GMF (impuesto)"
	textoComision             = "Comisión de la billetera"
	textoMovimiento           = "Movimiento"
)

// Apunte es una línea del diario ya leída, con lo mínimo para agrupar y resolver.
type Apunte struct {
	EntryID            int64
	TransferenciaID    uuid.UUID
	TieneTransferencia bool
	OcurridoEn         time.Time
	Tipo               string
	Descripcion        string
	CuentaID           string
	Monto              int64
	Moneda             string
}

// Efecto es el neto de un usuario dentro de una operación.
type Efecto struct {
	UsuarioID   string
	Neto        int64
	Moneda      string
	OcurridoEn  time.Time
	EntryAncla  int64
	Descripcion string
}

// Contraparte es lo que la vista puede mostrar del otro lado de la operación.
type Contraparte struct {
	Texto            string
	Resuelta         bool
	Tipo             TipoDestino
	Llave            string
	MID              string
	Nombre           string
	CategoriaInterna Categoria
}

// UsuarioDeCuenta extrae el identificador de una cuenta pasivo:usuario:{id}:{sub}.
func UsuarioDeCuenta(cuenta string) (string, bool) {
	resto, encontrado := strings.CutPrefix(cuenta, prefijoCuentaDeUsuario)
	if !encontrado {
		return "", false
	}
	corte := strings.LastIndex(resto, ":")
	if corte <= 0 {
		return "", false
	}
	usuario := resto[:corte]
	return usuario, usuario != ""
}

// EfectosPorUsuario suma los apuntes de cada usuario y devuelve un efecto por usuario.
func EfectosPorUsuario(apuntes []Apunte) ([]Efecto, error) {
	porUsuario := make(map[string]*Efecto)
	for _, apunte := range apuntes {
		usuario, esDeUsuario := UsuarioDeCuenta(apunte.CuentaID)
		if !esDeUsuario {
			continue
		}
		efecto, existe := porUsuario[usuario]
		if !existe {
			efecto = &Efecto{UsuarioID: usuario, EntryAncla: apunte.EntryID}
			porUsuario[usuario] = efecto
		}
		suma, desborda := sumarEnteros(efecto.Neto, apunte.Monto)
		if desborda {
			return nil, errors.New("inteligencia: el neto del movimiento desborda int64")
		}
		efecto.Neto = suma
		if apunte.EntryID < efecto.EntryAncla {
			efecto.EntryAncla = apunte.EntryID
		}
		if apunte.OcurridoEn.After(efecto.OcurridoEn) {
			efecto.OcurridoEn = apunte.OcurridoEn
		}
		if efecto.Descripcion == "" && apunte.Descripcion != "" {
			efecto.Descripcion = apunte.Descripcion
		}
		if efecto.Moneda == "" && apunte.Moneda != "" {
			efecto.Moneda = apunte.Moneda
		}
	}
	efectos := make([]Efecto, 0, len(porUsuario))
	for _, efecto := range porUsuario {
		efectos = append(efectos, *efecto)
	}
	sort.Slice(efectos, func(i, j int) bool {
		if efectos[i].EntryAncla != efectos[j].EntryAncla {
			return efectos[i].EntryAncla < efectos[j].EntryAncla
		}
		return efectos[i].UsuarioID < efectos[j].UsuarioID
	})
	return efectos, nil
}

// OtrosUsuarios devuelve los usuarios de la operación distintos del actual, ordenados.
func OtrosUsuarios(apuntes []Apunte, usuarioID string) []string {
	vistos := make(map[string]struct{})
	for _, apunte := range apuntes {
		usuario, esDeUsuario := UsuarioDeCuenta(apunte.CuentaID)
		if !esDeUsuario || usuario == usuarioID {
			continue
		}
		vistos[usuario] = struct{}{}
	}
	usuarios := make([]string, 0, len(vistos))
	for usuario := range vistos {
		usuarios = append(usuarios, usuario)
	}
	sort.Strings(usuarios)
	return usuarios
}

// ResolverContraparte elige el otro lado de la operación por tipo de cuenta y aplica el respaldo
// de tres pasos. Nunca devuelve el identificador de una cuenta y nunca devuelve texto vacío.
func ResolverContraparte(apuntes []Apunte, usuarioID string, nombres map[string]string) Contraparte {
	if otros := OtrosUsuarios(apuntes, usuarioID); len(otros) > 0 {
		contraparte := otros[0]
		nombre := strings.TrimSpace(nombres[contraparte])
		resuelta := nombre != ""
		if !resuelta {
			nombre = textoUsuarioDeLaBilletera
		}
		return Contraparte{
			Texto:    nombre,
			Resuelta: resuelta,
			Tipo:     DestinoPersona,
			Llave:    contraparte,
			Nombre:   nombre,
		}
	}

	cuentas := cuentasDelGrupo(apuntes)
	if _, existe := cuentas[cuentaGMFPorPagar]; existe {
		return Contraparte{
			Texto:            textoGMF,
			Resuelta:         true,
			Tipo:             DestinoComercio,
			Nombre:           textoGMF,
			CategoriaInterna: CategoriaComisiones,
		}
	}
	if _, existe := cuentas[cuentaComision]; existe {
		return Contraparte{
			Texto:            textoComision,
			Resuelta:         true,
			Tipo:             DestinoComercio,
			Nombre:           textoComision,
			CategoriaInterna: CategoriaComisiones,
		}
	}
	for cuenta := range cuentas {
		if strings.HasPrefix(cuenta, prefijoCustodia) {
			return Contraparte{Texto: textoBancoCustodio, Resuelta: true, Tipo: DestinoComercio, Nombre: textoBancoCustodio}
		}
	}
	for cuenta := range cuentas {
		if strings.HasPrefix(cuenta, prefijoLiquidacion) {
			texto := descripcionDelGrupo(apuntes)
			if texto == "" {
				texto = textoComercioExterno
			}
			return Contraparte{Texto: texto, Resuelta: false, Tipo: DestinoComercio, Nombre: texto}
		}
	}

	texto := descripcionDelGrupo(apuntes)
	if texto == "" {
		texto = textoMovimiento
	}
	return Contraparte{Texto: texto, Resuelta: false, Tipo: DestinoComercio, Nombre: texto}
}

// TextoDeComercio es la etiqueta del comercio externo cuando el pago no trae su nombre.
func TextoDeComercio() string { return textoComercioExterno }

func cuentasDelGrupo(apuntes []Apunte) map[string]struct{} {
	cuentas := make(map[string]struct{}, len(apuntes))
	for _, apunte := range apuntes {
		cuentas[apunte.CuentaID] = struct{}{}
	}
	return cuentas
}

func descripcionDelGrupo(apuntes []Apunte) string {
	for _, apunte := range apuntes {
		if texto := strings.TrimSpace(apunte.Descripcion); texto != "" {
			return texto
		}
	}
	return ""
}

func sumarEnteros(a, b int64) (int64, bool) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, true
	}
	return a + b, false
}

// Absoluto devuelve el valor absoluto de un monto, o false si es el mínimo de int64.
func Absoluto(monto int64) (int64, bool) {
	if monto == math.MinInt64 {
		return 0, false
	}
	if monto < 0 {
		return -monto, true
	}
	return monto, true
}
