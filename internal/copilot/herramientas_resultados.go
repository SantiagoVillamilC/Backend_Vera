package copilot

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// DatosHerramienta sólo admite las vistas mínimas conocidas por el catálogo.
// Nunca se serializan directamente entidades de auth, SQL o del núcleo.
type DatosHerramienta interface{ datosHerramienta() }

// SaldoHerramienta expresa saldo desde la perspectiva del usuario.
type SaldoHerramienta struct{ Disponible, Retenido nucleo.Centavos }

func (SaldoHerramienta) datosHerramienta() {}

// LimitesHerramienta contiene exclusivamente importes calculados por el núcleo.
type LimitesHerramienta struct {
	MaximoEnviable, SaldoDespuesGMF, TopeOperacion, CupoMensualRestante, GMF nucleo.Centavos
	AplicaGMF                                                                bool
}

func (LimitesHerramienta) datosHerramienta() {}

// MovimientoHerramienta omite IDs de asientos, cuentas y usuarios.
type MovimientoHerramienta struct {
	Fecha       time.Time
	Motivo      string
	Monto       nucleo.Centavos
	Contraparte string
	Componentes []nucleo.LineaMovimiento
}

// MovimientosHerramienta anuncia si existen más registros fuera de la página.
type MovimientosHerramienta struct {
	Movimientos []MovimientoHerramienta
	HayMas      bool
	Total       int64
}

func (MovimientosHerramienta) datosHerramienta() {}

// GrupoHerramienta contiene agregaciones calculadas por el manejador, no por IA.
type GrupoHerramienta struct {
	Nombre                  string
	Entradas, Salidas, Neto nucleo.Centavos
	Cantidad                int
}

// ResumenHerramienta contiene totales globales y un conjunto acotado de grupos.
type ResumenHerramienta struct {
	Entradas, Salidas, Neto nucleo.Centavos
	Cantidad                int
	Grupos                  []GrupoHerramienta
	HayMas                  bool
}

func (ResumenHerramienta) datosHerramienta() {}

// DestinatarioHerramienta recibe datos del resolvedor y los enmascara al salir.
type DestinatarioHerramienta struct{ Nombre, Celular, Referencia string }

// DestinatariosHerramienta omite IDs internos y limita la lista antes de enviarla.
type DestinatariosHerramienta struct {
	Destinatarios []DestinatarioHerramienta
	HayMas        bool
}

func (DestinatariosHerramienta) datosHerramienta() {}

// TransferenciaHerramienta expone el estado sin cuentas, llaves ni titular.
type TransferenciaHerramienta struct {
	Estado                  string
	Monto                   nucleo.Centavos
	CreadaEn, ActualizadaEn time.Time
}

func (TransferenciaHerramienta) datosHerramienta() {}

// AccionHerramienta representa una propuesta pendiente, nunca una transferencia ejecutada.
type AccionHerramienta struct {
	AccionID       uuid.UUID
	Destinatario   DestinatarioHerramienta
	Monto          nucleo.Centavos
	ExpiraEn       time.Time
	RequiereCodigo bool
	Entidad        string
}

func (AccionHerramienta) datosHerramienta() {}

// montoParaModelo no acepta texto formateado del manejador: lo deriva del entero.
func montoParaModelo(monto nucleo.Centavos) map[string]any {
	return map[string]any{"centavos": monto, "pesos": monto.PresentarUnidadesMayores() + " COP"}
}

func serializarDatos(nombre string, args ArgumentosHerramienta, datos DatosHerramienta) (json.RawMessage, error) {
	var vista map[string]any
	switch d := datos.(type) {
	case SaldoHerramienta:
		if nombre != "consultar_saldo" {
			return nil, ErrHerramientaValidacion
		}
		vista = map[string]any{"disponible": montoParaModelo(d.Disponible), "retenido": montoParaModelo(d.Retenido), "moneda": nucleo.MonedaCOP}
	case LimitesHerramienta:
		if nombre != "consultar_limites" {
			return nil, ErrHerramientaValidacion
		}
		vista = map[string]any{"maximo_enviable": montoParaModelo(d.MaximoEnviable), "saldo_despues_gmf": montoParaModelo(d.SaldoDespuesGMF), "tope_operacion": montoParaModelo(d.TopeOperacion), "cupo_mensual_restante": montoParaModelo(d.CupoMensualRestante), "gmf": montoParaModelo(d.GMF), "aplica_gmf": d.AplicaGMF, "moneda": nucleo.MonedaCOP}
	case MovimientosHerramienta:
		if nombre != "listar_movimientos" {
			return nil, ErrHerramientaValidacion
		}
		limite := min(args.Limite, 20)
		filas := make([]map[string]any, 0, min(len(d.Movimientos), limite))
		for _, m := range d.Movimientos[:min(len(d.Movimientos), limite)] {
			if !motivoValido(m.Motivo) || m.Fecha.IsZero() {
				return nil, ErrHerramientaValidacion
			}
			componentes := make([]map[string]any, 0, len(m.Componentes))
			for _, l := range m.Componentes {
				if !motivoValido(string(l.Motivo)) {
					return nil, ErrHerramientaValidacion
				}
				componentes = append(componentes, map[string]any{"motivo": l.Motivo, "monto": montoParaModelo(l.Monto)})
			}
			filas = append(filas, map[string]any{"fecha": fechaColombia(m.Fecha), "motivo": m.Motivo, "monto": montoParaModelo(m.Monto), "contraparte": nombreParaModelo(m.Contraparte), "componentes": componentes})
		}
		vista = map[string]any{"movimientos": filas, "hay_mas": d.HayMas || len(d.Movimientos) > limite, "total": max(d.Total, int64(len(d.Movimientos)))}
	case ResumenHerramienta:
		if nombre != "resumir_movimientos" || d.Cantidad < 0 {
			return nil, ErrHerramientaValidacion
		}
		grupos := make([]map[string]any, 0, min(len(d.Grupos), 10))
		for _, g := range d.Grupos[:min(len(d.Grupos), 10)] {
			if g.Cantidad < 0 {
				return nil, ErrHerramientaValidacion
			}
			etiqueta := nombreParaModelo(g.Nombre)
			if g.Nombre == "Sin contraparte" || g.Nombre == "Contraparte no disponible" {
				etiqueta = g.Nombre
			}
			if args.AgruparPor == "motivo" {
				if !motivoValido(g.Nombre) {
					return nil, ErrHerramientaValidacion
				}
				etiqueta = g.Nombre
			}
			total, err := g.Entradas.Sumar(g.Salidas)
			if err != nil {
				return nil, err
			}
			grupos = append(grupos, map[string]any{"grupo": etiqueta, "entradas": montoParaModelo(g.Entradas), "salidas": montoParaModelo(g.Salidas), "neto": montoParaModelo(g.Neto), "cantidad": g.Cantidad, "total": montoParaModelo(total)})
		}
		vista = map[string]any{"entradas": montoParaModelo(d.Entradas), "salidas": montoParaModelo(d.Salidas), "neto": montoParaModelo(d.Neto), "cantidad": d.Cantidad, "grupos": grupos, "hay_mas": d.HayMas || len(d.Grupos) > 10}
	case DestinatariosHerramienta:
		if nombre != "buscar_destinatario" {
			return nil, ErrHerramientaValidacion
		}
		filas := make([]map[string]any, 0, min(len(d.Destinatarios), 5))
		for _, dest := range d.Destinatarios[:min(len(d.Destinatarios), 5)] {
			if !referenciaValida(dest.Referencia) {
				return nil, ErrHerramientaValidacion
			}
			filas = append(filas, map[string]any{"nombre": nombreParaModelo(dest.Nombre), "celular": celularParaModelo(dest.Celular), "destinatario_ref": dest.Referencia})
		}
		vista = map[string]any{"destinatarios": filas, "hay_mas": d.HayMas || len(d.Destinatarios) > 5}
	case TransferenciaHerramienta:
		if nombre != "consultar_transferencia" || !estadoTransferenciaValido(d.Estado) || d.CreadaEn.IsZero() || d.ActualizadaEn.IsZero() {
			return nil, ErrHerramientaValidacion
		}
		vista = map[string]any{"estado": d.Estado, "monto": montoParaModelo(d.Monto), "creada_en": fechaColombia(d.CreadaEn), "actualizada_en": fechaColombia(d.ActualizadaEn)}
	case AccionHerramienta:
		if nombre != "preparar_transferencia" || d.AccionID == uuid.Nil || d.Monto <= 0 || d.ExpiraEn.IsZero() {
			return nil, ErrHerramientaValidacion
		}
		vista = map[string]any{"accion_id": d.AccionID.String(), "estado": "pendiente", "requiere_confirmacion": true, "requiere_codigo": d.RequiereCodigo, "destinatario": map[string]any{"nombre": nombreParaModelo(d.Destinatario.Nombre), "celular": celularParaModelo(d.Destinatario.Celular)}, "entidad_destino": d.Entidad, "monto": montoParaModelo(d.Monto), "expira_en": fechaColombia(d.ExpiraEn)}
	default:
		return nil, ErrHerramientaValidacion
	}
	b, err := json.Marshal(struct {
		Datos map[string]any `json:"datos"`
	}{Datos: vista})
	if err != nil || len(b) > 16384 {
		return nil, ErrHerramientaValidacion
	}
	return b, nil
}

func fechaColombia(t time.Time) string {
	return t.In(time.FixedZone("Colombia", -5*60*60)).Format(time.RFC3339)
}

func motivoValido(m string) bool {
	switch m {
	case "deposito", "transferencia_recibida", "envio", "gravamen", "devolucion", "devolucion_gravamen":
		return true
	}
	return false
}

func estadoTransferenciaValido(e string) bool {
	switch e {
	case nucleo.EstadoTransferenciaCreada, nucleo.EstadoTransferenciaReservada, nucleo.EstadoTransferenciaDespachada, nucleo.EstadoTransferenciaConfirmada, nucleo.EstadoTransferenciaFallida, nucleo.EstadoTransferenciaIndeterminada, nucleo.EstadoTransferenciaCompensada:
		return true
	}
	return false
}

func referenciaValida(s string) bool {
	if len(s) < 16 || len(s) > 1024 {
		return false
	}
	for _, r := range s {
		valido := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.'
		if !valido {
			return false
		}
	}
	return true
}

func nombreParaModelo(nombre string) string {
	campos := strings.Fields(nombre)
	partes := make([]string, 0, min(len(campos), 2))
	for _, campo := range campos[:min(len(campos), 2)] {
		r, _ := utf8.DecodeRuneInString(campo)
		if unicode.IsLetter(r) {
			partes = append(partes, string(r)+"***")
		} else {
			partes = append(partes, "***")
		}
	}
	return strings.Join(partes, " ")
}

func celularParaModelo(celular string) string {
	if len(celular) == 10 && strings.HasPrefix(celular, "*******") {
		ultimos := celular[7:]
		if ultimos[0] >= '0' && ultimos[0] <= '9' && ultimos[1] >= '0' && ultimos[1] <= '9' && ultimos[2] >= '0' && ultimos[2] <= '9' {
			return celular
		}
	}
	digitos := make([]rune, 0, len(celular))
	for _, r := range celular {
		if r >= '0' && r <= '9' {
			digitos = append(digitos, r)
		}
	}
	if len(digitos) < 4 {
		return "***"
	}
	return "*******" + string(digitos[len(digitos)-3:])
}
