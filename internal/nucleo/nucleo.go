// Package nucleo es el núcleo financiero: dueño exclusivo de las tablas del ledger.
package nucleo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Centavos es un monto en unidades menores; positivo es débito.
type Centavos int64

// ErrDesbordamiento reporta un Sumar o un Restar cuyo resultado exacto no cabe en int64.
var ErrDesbordamiento = errors.New("nucleo: desbordamiento de int64")

// ErrMontoInvalido reporta texto que no es un número entero de unidades menores.
var ErrMontoInvalido = errors.New("nucleo: el monto debe ser un número entero de unidades menores")

// Int64 devuelve las unidades menores crudas.
func (c Centavos) Int64() int64 { return int64(c) }

// PresentarUnidadesMayores rinde el monto en unidades mayores para una persona.
func (c Centavos) PresentarUnidadesMayores() string {
	s := strconv.FormatInt(int64(c), 10)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	for len(s) < 3 {
		s = "0" + s
	}
	return sign + s[:len(s)-2] + "." + s[len(s)-2:]
}

// ParsearCentavos es el único parser de un monto que llega como texto.
func ParsearCentavos(s string) (Centavos, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrMontoInvalido
	}
	return Centavos(v), nil
}

// Sumar devuelve c + o, o ErrDesbordamiento si la suma exacta no cabe.
func (c Centavos) Sumar(o Centavos) (Centavos, error) {
	if (o > 0 && c > math.MaxInt64-o) || (o < 0 && c < math.MinInt64-o) {
		return 0, fmt.Errorf("sumar %d + %d: %w", c, o, ErrDesbordamiento)
	}
	return c + o, nil
}

// Restar devuelve c - o, o ErrDesbordamiento si la resta exacta no cabe.
func (c Centavos) Restar(o Centavos) (Centavos, error) {
	if (o < 0 && c > math.MaxInt64+o) || (o > 0 && c < math.MinInt64+o) {
		return 0, fmt.Errorf("restar %d - %d: %w", c, o, ErrDesbordamiento)
	}
	return c - o, nil
}

// MarshalJSON emite el monto como cadena JSON, nunca como número.
func (c Centavos) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 22)
	b = append(b, '"')
	b = strconv.AppendInt(b, int64(c), 10)
	return append(b, '"'), nil
}

// UnmarshalJSON acepta solo una cadena JSON de unidades menores enteras.
func (c *Centavos) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return ErrMontoInvalido
	}
	v, err := ParsearCentavos(s)
	if err != nil {
		return err
	}
	*c = v
	return nil
}

// LectorNivelKYC es lo que el núcleo necesita de auth para regla_limite.
type LectorNivelKYC interface {
	NivelKYC(ctx context.Context, usuarioID string) (string, error)
}

// OrdenRiel es la instrucción de transferencia que el núcleo le entrega al riel.
type OrdenRiel struct {
	TransferenciaID uuid.UUID `json:"transferencia_id"`
	IdempotencyKey  string    `json:"idempotency_key"`
	Llave           string    `json:"llave"`
	Entidad         string    `json:"entidad"`
	Producto        string    `json:"producto"`
	Monto           Centavos  `json:"monto_centavos"`
	Moneda          string    `json:"moneda"`
}

// ResultadoRiel es el desenlace definitivo que el riel reporta para una orden.
type ResultadoRiel string

// Los dos desenlaces definitivos que el riel reporta.
const (
	ResultadoConfirmado ResultadoRiel = "confirmada"
	ResultadoRechazado  ResultadoRiel = "rechazada"
)

// RespuestaRiel es lo que el riel devuelve al recibir una orden o al consultarle su estado.
type RespuestaRiel struct {
	TransferenciaID uuid.UUID     `json:"transferencia_id"`
	Resultado       ResultadoRiel `json:"resultado"`
	Motivo          string        `json:"motivo,omitempty"`
	OcurridoEn      time.Time     `json:"ocurrido_en"`
}

// ErrRielSinRegistro reporta que el riel no reconoce la transferencia consultada.
var ErrRielSinRegistro = errors.New("nucleo: el riel no tiene registro de la transferencia")

// Riel es la red externa por donde viaja el dinero.
type Riel interface {
	Enviar(ctx context.Context, orden OrdenRiel) (RespuestaRiel, error)
	ConsultarEstado(ctx context.Context, transferenciaID uuid.UUID) (RespuestaRiel, error)
}

// ExtractoCustodio es el corte del banco custodio, no del ledger.
type ExtractoCustodio struct {
	CorteEn time.Time
	Saldo   Centavos
	Origen  string
}

// LectorExtractoCustodio es lo que el núcleo necesita del simulador de custodia; el corte lo pide quien llama.
type LectorExtractoCustodio interface {
	Extracto(ctx context.Context, corteEn time.Time) (ExtractoCustodio, error)
}

// ExtractoRiel es la ventana de operaciones que el riel reporta como ocurridas de su lado.
type ExtractoRiel struct {
	Corte       time.Time
	Operaciones []OperacionDeExtractoRiel
}

// OperacionDeExtractoRiel es una fila del extracto, emparejable solo por el identificador global.
type OperacionDeExtractoRiel struct {
	TransferenciaID uuid.UUID
	Estado          string
	Monto           Centavos
	OcurridoEn      time.Time
}

// LectorExtractoRiel es lo que el núcleo necesita del extracto del riel; el rango lo pide quien llama.
type LectorExtractoRiel interface {
	Extracto(ctx context.Context, desde, hasta time.Time) (ExtractoRiel, error)
}

// Servicio es la única puerta a las tablas del núcleo y a su frontera transaccional.
type Servicio struct {
	pool                      abridorDeTransacciones
	kyc                       LectorNivelKYC
	destinos                  ResolutorDestino
	directorio                DirectorioLlaves
	riel                      Riel
	custodio                  LectorExtractoCustodio
	lectorRiel                LectorExtractoRiel
	detectar                  detectorDeVentana
	ahora                     func() time.Time
	rastreador                RastreadorErrores
	politicaReintento         PoliticaReintentoOutbox
	presupuestoIndeterminadas time.Duration
}

// OpcionServicio ajusta el Servicio en su construcción.
type OpcionServicio func(*Servicio)

// ConLectorExtractoRiel fija el lector del extracto del riel; sin él, la conciliación falla cerrada.
func ConLectorExtractoRiel(lector LectorExtractoRiel) OpcionServicio {
	return func(s *Servicio) { s.lectorRiel = lector }
}

// ConPoliticaReintentoOutbox fija la espera creciente del barrido de la bandeja de salida.
func ConPoliticaReintentoOutbox(politica PoliticaReintentoOutbox) OpcionServicio {
	return func(s *Servicio) { s.politicaReintento = politica }
}

// ConResolutorDestino fija el resolutor de la llave de destino; sin él, Transferir falla cerrado.
func ConResolutorDestino(resolutor ResolutorDestino) OpcionServicio {
	return func(s *Servicio) { s.destinos = resolutor }
}

// ConDirectorio fija el directorio de llaves de Bre-B; sin él, ResolverLlave falla cerrado.
func ConDirectorio(directorio DirectorioLlaves) OpcionServicio {
	return func(s *Servicio) { s.directorio = directorio }
}

// NewServicio arma el Servicio; el Querier se construye por transacción adentro.
func NewServicio(
	pool *pgxpool.Pool,
	kyc LectorNivelKYC,
	riel Riel,
	custodio LectorExtractoCustodio,
	rastreador RastreadorErrores,
	opciones ...OpcionServicio,
) *Servicio {
	servicio := &Servicio{
		pool:                      poolDeNucleo{pool: pool},
		kyc:                       kyc,
		riel:                      riel,
		custodio:                  custodio,
		lectorRiel:                RielNoDisponible{},
		ahora:                     time.Now,
		rastreador:                rastreador,
		politicaReintento:         PoliticaReintentoOutboxPorDefecto(),
		presupuestoIndeterminadas: presupuestoConsultaIndeterminadasDefecto,
	}
	servicio.detectar = servicio.detectarDiferenciasDeVentana
	for _, opcion := range opciones {
		opcion(servicio)
	}
	return servicio
}

// Rellenos para que main arranque antes de que lleguen las piezas reales.

// KYCNoDisponible reemplaza al lector de nivel de KYC hasta que exista.
type KYCNoDisponible struct{}

// NivelKYC siempre devuelve ErrKYCNoDisponible.
func (KYCNoDisponible) NivelKYC(context.Context, string) (string, error) {
	return "", ErrKYCNoDisponible
}

// LectorNivelKYCFijo devuelve el único nivel de KYC del alcance de la tesis.
type LectorNivelKYCFijo struct{}

// NivelKYC devuelve siempre el nivel del depósito de bajo monto.
func (LectorNivelKYCFijo) NivelKYC(context.Context, string) (string, error) {
	return NivelKYCDepositoBajoMonto, nil
}

// RielNoDisponible es el riel que se usa cuando no hay URL configurada.
type RielNoDisponible struct{}

// Enviar siempre devuelve ErrRielNoDisponible.
func (RielNoDisponible) Enviar(context.Context, OrdenRiel) (RespuestaRiel, error) {
	return RespuestaRiel{}, ErrRielNoDisponible
}

// ConsultarEstado siempre devuelve ErrRielNoDisponible.
func (RielNoDisponible) ConsultarEstado(context.Context, uuid.UUID) (RespuestaRiel, error) {
	return RespuestaRiel{}, ErrRielNoDisponible
}

// Extracto siempre devuelve ErrRielNoDisponible.
func (RielNoDisponible) Extracto(context.Context, time.Time, time.Time) (ExtractoRiel, error) {
	return ExtractoRiel{}, ErrRielNoDisponible
}

// CustodioNoDisponible es el relleno del extracto para las pruebas que no levantan el simulador.
type CustodioNoDisponible struct{}

// Extracto siempre devuelve ErrCustodioNoDisponible.
func (CustodioNoDisponible) Extracto(context.Context, time.Time) (ExtractoCustodio, error) {
	return ExtractoCustodio{}, ErrCustodioNoDisponible
}

// Errores de los rellenos.
var (
	// ErrKYCNoDisponible no tiene adaptador real en el YAML.
	ErrKYCNoDisponible = errors.New("nucleo: pendiente: lector de nivel de KYC real")
	// ErrRielNoDisponible reporta que no hay riel configurado.
	ErrRielNoDisponible = errors.New("nucleo: no hay riel configurado")
	// ErrCustodioNoDisponible reporta que no hay lector de extracto inyectado.
	ErrCustodioNoDisponible = errors.New("nucleo: extracto del custodio no disponible")
)

var (
	_ LectorNivelKYC         = KYCNoDisponible{}
	_ LectorNivelKYC         = LectorNivelKYCFijo{}
	_ Riel                   = RielNoDisponible{}
	_ LectorExtractoCustodio = CustodioNoDisponible{}
	_ LectorExtractoRiel     = RielNoDisponible{}
)
