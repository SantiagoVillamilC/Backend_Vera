package nucleo

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// HU-NF-06, contexto: la UVT de 2026 es 52.374, así que 210,50 UVT son 11.024.727 exactos, sin residuo.
const topeCanonico2026 = Centavos(1102472700)

const uvts2026 = int64(5237400)

// TestCentavosDesdeTopeUVTConAritmeticaEntera fija la conversión de UVT a centavos sin flotante (HU-NF-06, criterios 10 y 11).
func TestCentavosDesdeTopeUVTConAritmeticaEntera(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		tope        int64
		valor       int64
		querer      Centavos
		quererError error
	}{
		"210,50 UVT de 2026 exactas":        {tope: 21050, valor: uvts2026, querer: topeCanonico2026},
		"210,50 UVT con otra UVT de prueba": {tope: 21050, valor: 100, querer: 21050},
		"un centavo sin residuo":            {tope: 1, valor: 100, querer: 1},
		"un centavo con residuo 49":         {tope: 1, valor: 149, querer: 1},
		"un centavo con residuo 50 sube":    {tope: 1, valor: 150, querer: 2},
		"un centavo con residuo 51 sube":    {tope: 1, valor: 151, querer: 2},
		"tope cero":                         {tope: 0, valor: uvts2026, quererError: ErrMontoInvalido},
		"UVT cero":                          {tope: 21050, valor: 0, quererError: ErrMontoInvalido},
		"UVT negativa":                      {tope: 21050, valor: -1, quererError: ErrMontoInvalido},
		"producto que no cabe en int64":     {tope: math.MaxInt64, valor: 2, quererError: ErrDesbordamiento},
		"producto en el borde de int64":     {tope: math.MaxInt64 / 2, valor: 2, querer: Centavos((math.MaxInt64 / 2 * 2) / 100)},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			got, err := centavosDesdeTopeUVT(caso.tope, caso.valor)
			if !errors.Is(err, caso.quererError) {
				t.Fatalf("error = %v, quiero %v", err, caso.quererError)
			}
			if err == nil && got != caso.querer {
				t.Errorf("centavosDesdeTopeUVT(%d, %d) = %d, quiero %d", caso.tope, caso.valor, got, caso.querer)
			}
		})
	}
}

// TestEvaluarLimiteTopeOperacionYCupoMensual cubre el borde exacto del tope y la suma del mes (HU-NF-06, criterios 10 a 12).
func TestEvaluarLimiteTopeOperacionYCupoMensual(t *testing.T) {
	t.Parallel()

	reglasCompletas := []ReglaLimite{
		{Ventana: VentanaLimiteOperacion, TopeUVTCentesimas: 21050},
		{Ventana: VentanaLimiteMensual, TopeUVTCentesimas: 21050},
	}

	casos := map[string]struct {
		reglas      []ReglaLimite
		valor       int64
		sumaMensual Centavos
		monto       Centavos
		quererError error
	}{
		"exactamente el tope por operación pasa":        {reglas: reglasCompletas, valor: uvts2026, sumaMensual: 0, monto: topeCanonico2026},
		"un centavo más que el tope se rechaza":         {reglas: reglasCompletas, valor: uvts2026, sumaMensual: 0, monto: topeCanonico2026 + 1, quererError: ErrTopeOperacionExcedido},
		"exactamente el cupo mensual pasa":              {reglas: reglasCompletas, valor: uvts2026, sumaMensual: topeCanonico2026 - 100, monto: 100},
		"un centavo más que el cupo mensual se rechaza": {reglas: reglasCompletas, valor: uvts2026, sumaMensual: topeCanonico2026 - 100, monto: 101, quererError: ErrCupoMensualExcedido},
		"sin regla de operación vigente se rechaza":     {reglas: reglasCompletas[1:], valor: uvts2026, sumaMensual: 0, monto: 1, quererError: ErrReglaLimiteNoVigente},
		"sin regla mensual vigente se rechaza":          {reglas: reglasCompletas[:1], valor: uvts2026, sumaMensual: 0, monto: 1, quererError: ErrReglaLimiteNoVigente},
		"sin ninguna regla vigente se rechaza":          {reglas: nil, valor: uvts2026, sumaMensual: 0, monto: 1, quererError: ErrReglaLimiteNoVigente},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			err := evaluarLimite(caso.reglas, caso.valor, caso.sumaMensual, caso.monto)
			if !errors.Is(err, caso.quererError) {
				t.Fatalf("error = %v, quiero %v", err, caso.quererError)
			}
		})
	}
}

// dbSinUVT es un repositorio sin base que siempre responde ErrNoRows y sirve para el camino de configuración ausente.
type dbSinUVT struct{}

func (dbSinUVT) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (dbSinUVT) Query(context.Context, string, ...interface{}) (pgx.Rows, error) { return nil, nil }

func (dbSinUVT) QueryRow(context.Context, string, ...interface{}) pgx.Row { return filaAusente{} }

type filaAusente struct{}

// Scan reporta la ausencia de fila como pgx.ErrNoRows.
func (filaAusente) Scan(...interface{}) error { return pgx.ErrNoRows }

// TestVerificarLimiteSinValorDeUVTRechazaSinTocarLaBase cubre la recomendación de rechazar cuando falta la UVT (HU-NF-06, Preguntas abiertas).
func TestVerificarLimiteSinValorDeUVTRechazaSinTocarLaBase(t *testing.T) {
	t.Parallel()

	err := verificarLimite(t.Context(), repositorio.New(dbSinUVT{}), uuid.NewString(), NivelKYCDepositoBajoMonto, 1000, time.Now())
	if !errors.Is(err, ErrValorUVTNoVigente) {
		t.Fatalf("error = %v, quiero ErrValorUVTNoVigente", err)
	}
}

// TestEstadosDebitoDelMesFijanQueCuentaEnLaVentana cubre qué operaciones suman en el mes (HU-NF-06, Riesgos / Supuestos).
func TestEstadosDebitoDelMesFijanQueCuentaEnLaVentana(t *testing.T) {
	t.Parallel()

	querer := []string{
		EstadoTransferenciaReservada,
		EstadoTransferenciaDespachada,
		EstadoTransferenciaConfirmada,
		EstadoTransferenciaFallida,
		EstadoTransferenciaIndeterminada,
	}
	if !slices.Equal(estadosDebitoDelMes, querer) {
		t.Errorf("estados que suman = %v, quiero %v", estadosDebitoDelMes, querer)
	}
	if slices.Contains(estadosDebitoDelMes, EstadoTransferenciaCreada) {
		t.Error("creada no debe sumar: el dinero todavía no salió del disponible")
	}
	if slices.Contains(estadosDebitoDelMes, EstadoTransferenciaCompensada) {
		t.Error("compensada no debe sumar: el dinero volvió al disponible")
	}
}

// TestVentanaMensualColombiaCortaAMedianocheDeColombia fija el corte del mes en UTC-5 (HU-NF-06, Riesgos / Supuestos).
func TestVentanaMensualColombiaCortaAMedianocheDeColombia(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		momento     time.Time
		quererAnio  int
		quererDesde time.Time
	}{
		"cuatro de la mañana UTC del día 1 sigue en el mes anterior": {
			momento:     time.Date(2026, 6, 1, 4, 0, 0, 0, time.UTC),
			quererAnio:  2026,
			quererDesde: time.Date(2026, 5, 1, 5, 0, 0, 0, time.UTC),
		},
		"las cinco UTC del día 1 ya es el mes nuevo": {
			momento:     time.Date(2026, 6, 1, 5, 0, 0, 0, time.UTC),
			quererAnio:  2026,
			quererDesde: time.Date(2026, 6, 1, 5, 0, 0, 0, time.UTC),
		},
		"un envío de fin de año sigue en el año que corre en Colombia": {
			momento:     time.Date(2027, 1, 1, 4, 0, 0, 0, time.UTC),
			quererAnio:  2026,
			quererDesde: time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC),
		},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			anio, desde, hasta := ventanaMensualColombia(caso.momento)
			if anio != caso.quererAnio {
				t.Errorf("año = %d, quiero %d", anio, caso.quererAnio)
			}
			if !desde.Equal(caso.quererDesde) {
				t.Errorf("inicio = %s, quiero %s", desde, caso.quererDesde)
			}
			quererHasta := caso.quererDesde.AddDate(0, 1, 0)
			if !hasta.Equal(quererHasta) {
				t.Errorf("fin = %s, quiero %s", hasta, quererHasta)
			}
		})
	}
}
