// Package nucleo es el núcleo financiero de Centinela: un solo módulo Go que es dueño
// exclusivo de las tablas del ledger y de la frontera transaccional que las escribe.
//
// Lo que el núcleo necesita de afuera lo declara acá y main le inyecta la implementación
// (el patrón de LectorNivelKYC del capítulo 5 de frontera-modulos-nucleo-financiero.md).
// El acceso a las tablas vive en internal/repositorio, un paquete dentro del segundo
// internal de Go, de modo que ningún paquete de fuera del núcleo puede importarlo.
//
// Este archivo es el esqueleto de la fase 0: el tipo del dinero, las interfaces hacia
// afuera y el Servicio con sus dependencias. Los métodos de negocio (Transferir, Saldo,
// Conciliar, DespacharOutbox) los agrega cada unidad posterior.
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

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Centavos es un monto en la unidad mínima de la única moneda del ledger: $10.000 es
// Centavos(1000000). Positivo es un débito, negativo un crédito, igual que lo almacena
// posting.monto. Es un int64 con nombre, nunca un flotante ni un decimal, así que el
// compilador rechaza un int64 suelto (un id, un conteo) donde se espera dinero.
type Centavos int64

// ErrDesbordamiento reporta un Sumar o un Restar cuyo resultado exacto no cabe en int64.
var ErrDesbordamiento = errors.New("nucleo: desbordamiento de int64")

// ErrMontoInvalido reporta texto que no es un número entero de unidades menores.
var ErrMontoInvalido = errors.New("nucleo: el monto debe ser un número entero de unidades menores")

// Int64 devuelve las unidades menores crudas: el valor de toda regla, comparación,
// parámetro de consulta y asiento. El ledger nunca ve otra cosa.
func (c Centavos) Int64() int64 { return int64(c) }

// PresentarUnidadesMayores rinde el monto en unidades mayores ("10000.00", "-0.05") para
// que lo lea una persona: una notificación, un recibo, una exportación. Devuelve texto,
// nunca un float64, y es el único lugar de este lado donde se convierte la unidad menor.
// Nada vuelve a parsear esta cadena.
func (c Centavos) PresentarUnidadesMayores() string {
	s := strconv.FormatInt(int64(c), 10) // sin negar: -math.MinInt64 desbordaría
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	for len(s) < 3 { // "5" se vuelve "005" para que el corte de abajo rinda "0.05"
		s = "0" + s
	}
	return sign + s[:len(s)-2] + "." + s[len(s)-2:]
}

// ParsearCentavos es el único parser de un monto que llega como texto: una cadena JSON, un
// parámetro de consulta, una celda de CSV. Acepta un signo opcional y dígitos enteros, nada
// más: "12.50", "1e3", " 5" y cualquier valor que no quepa en int64 son ErrMontoInvalido.
func ParsearCentavos(s string) (Centavos, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrMontoInvalido
	}
	return Centavos(v), nil
}

// Sumar devuelve c + o, o ErrDesbordamiento cuando la suma exacta no cabe. Go envuelve
// int64 en silencio (go.dev/ref/spec, "Integer overflow") y math/bits solo cubre uint64,
// así que el chequeo se escribe a mano, antes de la suma.
func (c Centavos) Sumar(o Centavos) (Centavos, error) {
	if (o > 0 && c > math.MaxInt64-o) || (o < 0 && c < math.MinInt64-o) {
		return 0, fmt.Errorf("sumar %d + %d: %w", c, o, ErrDesbordamiento)
	}
	return c + o, nil
}

// Restar devuelve c - o con la misma garantía. No es c.Sumar(-o): negar math.MinInt64
// desborda antes de que Sumar pueda verlo, que es también por lo que una negación se
// escribe Centavos(0).Restar(c).
func (c Centavos) Restar(o Centavos) (Centavos, error) {
	if (o < 0 && c > math.MaxInt64+o) || (o > 0 && c < math.MinInt64+o) {
		return 0, fmt.Errorf("restar %d - %d: %w", c, o, ErrDesbordamiento)
	}
	return c - o, nil
}

// MarshalJSON emite el monto como cadena JSON ("1000000"), nunca como número: un int64
// completo no cabe en el rango de 2^53 que RFC 8259 y JavaScript comparten, así que los
// montos viajan como texto.
func (c Centavos) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, 22) // comilla + signo + 19 dígitos + comilla
	b = append(b, '"')
	b = strconv.AppendInt(b, int64(c), 10)
	return append(b, '"'), nil
}

// UnmarshalJSON acepta una sola forma: una cadena JSON de unidades menores enteras. Un
// número JSON se rechaza aunque quepa, porque un cliente que manda un número tiene el monto
// como número. Un monto opcional se declara *Centavos, donde el decoder nunca llama a este
// método y null se vuelve nil.
func (c *Centavos) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return ErrMontoInvalido // un número, bool, objeto o arreglo JSON
	}
	// json.Unmarshal trata null como no-op y deja s vacía, así que null también se rechaza.
	v, err := ParsearCentavos(s)
	if err != nil {
		return err
	}
	*c = v
	return nil
}

// LectorNivelKYC es lo que el núcleo necesita de auth para evaluar regla_limite. Lo declara
// el núcleo, lo implementa el módulo dueño de la identidad y main lo inyecta.
type LectorNivelKYC interface {
	NivelKYC(ctx context.Context, usuarioID string) (string, error)
}

// OrdenRiel es lo que el núcleo le manda al riel. Sus campos los agrega NF-A2-06 junto con
// la implementación real; en la fase 0 solo existe para dar forma a la interfaz.
type OrdenRiel struct{}

// RespuestaRiel es lo que el riel devuelve al aceptar o rechazar una orden. Sus campos los
// agrega NF-A2-06 por la misma razón.
type RespuestaRiel struct{}

// Riel es la red externa por donde viaja el dinero. Nace solo con Enviar: la consulta de
// estado se agrega como segundo método en NF-A2-06.
type Riel interface {
	Enviar(ctx context.Context, orden OrdenRiel) (RespuestaRiel, error)
}

// ExtractoCustodio es el corte que reporta el banco custodio. La cifra no sale de leer el
// ledger: si saliera, la conciliación compararía un número contra sí mismo.
type ExtractoCustodio struct {
	CorteEn time.Time
	Saldo   Centavos
	Origen  string
}

// LectorExtractoCustodio es lo que el núcleo necesita del simulador de custodia para
// conciliar. Lo declara el núcleo, lo implementa el cliente HTTP del simulador y main lo
// inyecta (capítulo 10 de la frontera).
type LectorExtractoCustodio interface {
	Extracto(ctx context.Context) (ExtractoCustodio, error)
}

// Servicio es el núcleo financiero completo: la única puerta a sus tablas y a su frontera
// transaccional. No expone ningún tipo de pgx ni del repositorio en sus firmas.
type Servicio struct {
	pool     *pgxpool.Pool
	q        *repositorio.Queries
	kyc      LectorNivelKYC
	riel     Riel
	custodio LectorExtractoCustodio
}

// NewServicio arma el Servicio con sus dependencias. El Querier del núcleo se construye acá
// a partir del pool: main no puede nombrar internal/repositorio porque el segundo internal
// de Go se lo prohíbe, y esa es justamente la pared que protege las tablas del núcleo.
func NewServicio(
	pool *pgxpool.Pool,
	kyc LectorNivelKYC,
	riel Riel,
	custodio LectorExtractoCustodio,
) *Servicio {
	return &Servicio{
		pool:     pool,
		q:        repositorio.New(pool),
		kyc:      kyc,
		riel:     riel,
		custodio: custodio,
	}
}

// Las implementaciones de relleno existen para que main arranque antes de que lleguen las
// piezas reales. Cada unidad que implementa una cambia una línea en el wiring.

// KYCNoDisponible reemplaza al lector de nivel de KYC hasta que exista el adaptador real
// (la tabla users todavía no tiene columna de nivel de KYC).
type KYCNoDisponible struct{}

// NivelKYC siempre devuelve ErrKYCNoDisponible.
func (KYCNoDisponible) NivelKYC(context.Context, string) (string, error) {
	return "", ErrKYCNoDisponible
}

// RielNoDisponible reemplaza al riel hasta que NF-A2-06 inyecte el adaptador HTTP contra el
// simulador.
type RielNoDisponible struct{}

// Enviar siempre devuelve ErrRielNoDisponible.
func (RielNoDisponible) Enviar(context.Context, OrdenRiel) (RespuestaRiel, error) {
	return RespuestaRiel{}, ErrRielNoDisponible
}

// CustodioNoDisponible reemplaza al lector de extracto hasta que NF-A5-01 inyecte el
// cliente HTTP contra el simulador de custodia.
type CustodioNoDisponible struct{}

// Extracto siempre devuelve ErrCustodioNoDisponible.
func (CustodioNoDisponible) Extracto(context.Context) (ExtractoCustodio, error) {
	return ExtractoCustodio{}, ErrCustodioNoDisponible
}

// Errores de las implementaciones de relleno. Cada uno nombra la unidad que lo reemplaza.
var (
	// ErrKYCNoDisponible lo devuelve KYCNoDisponible. No hay unidad del YAML dueña del
	// adaptador real de nivel de KYC.
	ErrKYCNoDisponible = errors.New("nucleo: pendiente: lector de nivel de KYC real")
	// ErrRielNoDisponible lo devuelve RielNoDisponible hasta NF-A2-06.
	ErrRielNoDisponible = errors.New("nucleo: pendiente: NF-A2-06")
	// ErrCustodioNoDisponible lo devuelve CustodioNoDisponible hasta NF-A5-01.
	ErrCustodioNoDisponible = errors.New("nucleo: pendiente: NF-A5-01")
)

var (
	_ LectorNivelKYC         = KYCNoDisponible{}
	_ Riel                   = RielNoDisponible{}
	_ LectorExtractoCustodio = CustodioNoDisponible{}
)
