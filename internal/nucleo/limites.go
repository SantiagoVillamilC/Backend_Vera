package nucleo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// NivelKYCDepositoBajoMonto es el único nivel de KYC del alcance de la tesis y el que elige las reglas vigentes (HU-NF-06, Contexto).
const NivelKYCDepositoBajoMonto = "deposito_bajo_monto"

// Las dos ventanas de límite que evalúa este motor: el tope por operación y el cupo mensual (HU-NF-06, criterios 10 y 11).
const (
	VentanaLimiteOperacion = "operacion"
	VentanaLimiteMensual   = "mensual"
)

// Los dos tipos de regla: la que rechaza por cupo y la que grava con GMF (HU-NF-06, criterio 13).
const (
	TipoReglaRechaza = "rechaza"
	TipoReglaGrava   = "grava"
)

// ppmEscala es el denominador de tarifa_ppm: 4000 ppm son el 4x1000 (HU-NF-06, criterio 13).
const ppmEscala = 1_000_000

// zonaHorariaCorte es el huso del corte del mes calendario: medianoche de Colombia, UTC-5 (HU-NF-06, Riesgos).
var zonaHorariaCorte = time.FixedZone("COT", -5*60*60)

// estadosDebitoDelMes son los estados en que el dinero salió del disponible y no ha vuelto (HU-NF-06, Riesgos).
var estadosDebitoDelMes = []string{
	EstadoTransferenciaReservada,
	EstadoTransferenciaDespachada,
	EstadoTransferenciaConfirmada,
	EstadoTransferenciaFallida,
	EstadoTransferenciaIndeterminada,
}

// ErrTopeOperacionExcedido rechaza un envío que por sí solo supera el tope del depósito de bajo monto (HU-NF-06, criterio 10).
var ErrTopeOperacionExcedido = errors.New("nucleo: el monto supera el tope del depósito de bajo monto")

// ErrCupoMensualExcedido rechaza un envío que agota el cupo mensual del depósito de bajo monto (HU-NF-06, criterio 11).
var ErrCupoMensualExcedido = errors.New("nucleo: el cupo mensual del depósito de bajo monto se agotó")

// ErrReglaLimiteNoVigente rechaza el envío cuando falta la regla del nivel para la fecha; se elige rechazar en vez de dejar pasar sin tope (HU-NF-06, Preguntas abiertas).
var ErrReglaLimiteNoVigente = errors.New("nucleo: no hay regla de límite vigente para el nivel de KYC en la fecha del envío")

// ErrValorUVTNoVigente rechaza el envío cuando no hay valor de UVT para el año de la fecha del envío (HU-NF-06, Consideraciones).
var ErrValorUVTNoVigente = errors.New("nucleo: no hay valor de UVT vigente para el año de la fecha del envío")

// ErrReglaGMFNoVigente rechaza el envío cuando falta la regla que grava con GMF; se rechaza en vez de omitir el gravamen (HU-NF-06, criterio 13).
var ErrReglaGMFNoVigente = errors.New("nucleo: no hay regla vigente de GMF para el nivel de KYC en la fecha del envío")

// ReglaLimite es una regla vigente con su tope ya leído, su tipo y su tarifa en ppm.
type ReglaLimite struct {
	Tipo              string
	Ventana           string
	TopeUVTCentesimas int64
	TarifaPPM         int64
}

// verificarLimite evalúa tope, cupo mensual y GMF dentro de la transacción del envío y devuelve el gravamen (HU-NF-06, criterios 10 a 13).
func verificarLimite(ctx context.Context, q *repositorio.Queries, usuarioID, nivelKYC string, monto Centavos, momento time.Time) (Centavos, error) {
	anio, desde, hasta := ventanaMensualColombia(momento)

	valorUVT, err := q.ValorUVTDelAnio(ctx, int32(anio))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrValorUVTNoVigente
		}
		return 0, fmt.Errorf("nucleo: leer la UVT de %d: %w", anio, err)
	}

	filas, err := q.ReglasLimiteVigentes(ctx, repositorio.ReglasLimiteVigentesParams{
		NivelKyc: nivelKYC,
		Fecha:    pgtype.Date{Time: momento.In(zonaHorariaCorte), Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("nucleo: leer las reglas de límite de %q: %w", nivelKYC, err)
	}
	reglas := make([]ReglaLimite, 0, len(filas))
	for _, fila := range filas {
		reglas = append(reglas, ReglaLimite{
			Tipo:              fila.Tipo,
			Ventana:           fila.Ventana,
			TopeUVTCentesimas: fila.TopeUvtCentesimas,
			TarifaPPM:         fila.TarifaPpm,
		})
	}

	suma, err := q.SumaDebitoDelMes(ctx, repositorio.SumaDebitoDelMesParams{
		UsuarioID: usuarioID,
		Estados:   estadosDebitoDelMes,
		Desde:     pgtype.Timestamptz{Time: desde, Valid: true},
		Hasta:     pgtype.Timestamptz{Time: hasta, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("nucleo: sumar los débitos del mes de %q: %w", usuarioID, err)
	}

	if err := evaluarLimite(reglas, valorUVT, Centavos(suma), monto); err != nil {
		return 0, err
	}
	return evaluarGMF(reglas, valorUVT, Centavos(suma), monto)
}

// evaluarLimite decide sobre reglas ya resueltas: rechaza por operación antes que por mes y solo mira las reglas que rechazan (HU-NF-06, criterios 10 a 12).
func evaluarLimite(reglas []ReglaLimite, valorUVTCentavos int64, sumaMensual, monto Centavos) error {
	topes := map[string]Centavos{}
	for _, regla := range reglas {
		if regla.Tipo != TipoReglaRechaza {
			continue
		}
		tope, err := centavosDesdeTopeUVT(regla.TopeUVTCentesimas, valorUVTCentavos)
		if err != nil {
			return err
		}
		if _, visto := topes[regla.Ventana]; !visto {
			topes[regla.Ventana] = tope
		}
	}

	topeOperacion, hayOperacion := topes[VentanaLimiteOperacion]
	topeMensual, hayMensual := topes[VentanaLimiteMensual]
	if !hayOperacion || !hayMensual {
		return ErrReglaLimiteNoVigente
	}

	if monto > topeOperacion {
		return ErrTopeOperacionExcedido
	}
	total, err := sumaMensual.Sumar(monto)
	if err != nil {
		return err
	}
	if total > topeMensual {
		return ErrCupoMensualExcedido
	}
	return nil
}

// CuentaExentaGMF es la cuenta marcada como exenta: la subcuenta disponible del emisor (HU-NF-06, criterio 13).
func CuentaExentaGMF(usuarioID string) string {
	return NombreCuentaDisponible(usuarioID)
}

// evaluarGMF devuelve el 4x1000 solo sobre lo que el envío excede del tope exento mensual (HU-NF-06, criterio 13).
func evaluarGMF(reglas []ReglaLimite, valorUVTCentavos int64, acumulado, monto Centavos) (Centavos, error) {
	for _, regla := range reglas {
		if regla.Tipo != TipoReglaGrava || regla.Ventana != VentanaLimiteMensual {
			continue
		}
		topeExento, err := centavosDesdeTopeUVT(regla.TopeUVTCentesimas, valorUVTCentavos)
		if err != nil {
			return 0, err
		}
		exceso, err := excesoDelEnvio(monto, acumulado, topeExento)
		if err != nil {
			return 0, err
		}
		return gravamenSobreExceso(exceso, regla.TarifaPPM)
	}
	return 0, ErrReglaGMFNoVigente
}

// excesoDelEnvio es la parte del envío que cae por encima del tope exento del mes (HU-NF-06, criterio 13).
func excesoDelEnvio(monto, acumulado, topeExento Centavos) (Centavos, error) {
	if monto < 0 || acumulado < 0 {
		return 0, ErrMontoInvalido
	}
	total, err := acumulado.Sumar(monto)
	if err != nil {
		return 0, err
	}
	if total <= topeExento {
		return 0, nil
	}
	exceso, err := total.Restar(topeExento)
	if err != nil {
		return 0, err
	}
	if exceso > monto {
		exceso = monto
	}
	return exceso, nil
}

// gravamenSobreExceso aplica la tarifa en ppm con aritmética entera y redondeo a la mitad hacia arriba (HU-NF-06, criterio 13).
func gravamenSobreExceso(exceso Centavos, tarifaPPM int64) (Centavos, error) {
	if exceso <= 0 {
		return 0, nil
	}
	if tarifaPPM <= 0 || exceso.Int64() > math.MaxInt64/tarifaPPM {
		return 0, ErrDesbordamiento
	}
	return Centavos(divisionRedondeada(exceso.Int64()*tarifaPPM, ppmEscala)), nil
}

// divisionRedondeada divide dos enteros no negativos y redondea al entero más cercano con la mitad hacia arriba.
func divisionRedondeada(numerador, denominador int64) int64 {
	cociente := numerador / denominador
	if (numerador%denominador)*2 >= denominador {
		cociente++
	}
	return cociente
}

// SaldoExigido es lo que el disponible del emisor debe cubrir: el monto más el gravamen (HU-NF-06, criterio 13.4).
func SaldoExigido(monto, gravamen Centavos) (Centavos, error) {
	return monto.Sumar(gravamen)
}

// centavosDesdeTopeUVT convierte centésimas de UVT a centavos con aritmética entera, redondeando al centavo con la mitad hacia arriba.
func centavosDesdeTopeUVT(topeUVTCentesimas, valorUVTCentavos int64) (Centavos, error) {
	if topeUVTCentesimas <= 0 || valorUVTCentavos <= 0 {
		return 0, ErrMontoInvalido
	}
	if topeUVTCentesimas > math.MaxInt64/valorUVTCentavos {
		return 0, ErrDesbordamiento
	}
	producto := topeUVTCentesimas * valorUVTCentavos
	centavos := producto / 100
	if producto%100 >= 50 {
		centavos++
	}
	return Centavos(centavos), nil
}

// ventanaMensualColombia devuelve el año de la UVT y el rango [inicio, fin) del mes calendario colombiano (HU-NF-06, Riesgos).
func ventanaMensualColombia(momento time.Time) (int, time.Time, time.Time) {
	local := momento.In(zonaHorariaCorte)
	inicio := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, zonaHorariaCorte)
	return local.Year(), inicio, inicio.AddDate(0, 1, 0)
}
