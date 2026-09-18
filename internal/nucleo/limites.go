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

// contextoDeLimite es lo que el motor lee de la base: las reglas vigentes, la UVT del año y el acumulado del mes.
type contextoDeLimite struct {
	reglas    []ReglaLimite
	valorUVT  int64
	acumulado Centavos
}

// leerContextoDeLimite reúne la UVT del año, las reglas vigentes y los débitos del mes del emisor (HU-NF-06, criterios 10 a 13).
func leerContextoDeLimite(ctx context.Context, q *repositorio.Queries, usuarioID, nivelKYC string, momento time.Time) (contextoDeLimite, error) {
	anio, desde, hasta := ventanaMensualColombia(momento)

	valorUVT, err := q.ValorUVTDelAnio(ctx, int32(anio))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return contextoDeLimite{}, ErrValorUVTNoVigente
		}
		return contextoDeLimite{}, fmt.Errorf("nucleo: leer la UVT de %d: %w", anio, err)
	}

	filas, err := q.ReglasLimiteVigentes(ctx, repositorio.ReglasLimiteVigentesParams{
		NivelKyc: nivelKYC,
		Fecha:    pgtype.Date{Time: momento.In(zonaHorariaCorte), Valid: true},
	})
	if err != nil {
		return contextoDeLimite{}, fmt.Errorf("nucleo: leer las reglas de límite de %q: %w", nivelKYC, err)
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
		return contextoDeLimite{}, fmt.Errorf("nucleo: sumar los débitos del mes de %q: %w", usuarioID, err)
	}

	return contextoDeLimite{reglas: reglas, valorUVT: valorUVT, acumulado: Centavos(suma)}, nil
}

// verificarLimite evalúa tope, cupo mensual y GMF dentro de la transacción del envío y devuelve el gravamen (HU-NF-06, criterios 10 a 13).
func verificarLimite(ctx context.Context, q *repositorio.Queries, usuarioID, nivelKYC string, monto Centavos, momento time.Time) (Centavos, error) {
	contexto, err := leerContextoDeLimite(ctx, q, usuarioID, nivelKYC, momento)
	if err != nil {
		return 0, err
	}
	if err := evaluarLimite(contexto.reglas, contexto.valorUVT, contexto.acumulado, monto); err != nil {
		return 0, err
	}
	return evaluarGMF(contexto.reglas, contexto.valorUVT, contexto.acumulado, monto)
}

// MontoMaximoEnviable devuelve lo máximo que el usuario puede enviar sin que el saldo insuficiente por GMF lo sorprenda (HU-NF-06, criterio 14).
func (s *Servicio) MontoMaximoEnviable(ctx context.Context, usuarioID string, momento time.Time) (Centavos, error) {
	if s.kyc == nil {
		return 0, ErrKYCNoDisponible
	}
	nivel, err := s.kyc.NivelKYC(ctx, usuarioID)
	if err != nil {
		return 0, fmt.Errorf("nucleo: leer el nivel de KYC de %q: %w", usuarioID, err)
	}
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (Centavos, error) {
			return montoMaximoEnviable(ctx, q, usuarioID, nivel, momento)
		})
}

// montoMaximoEnviable lee el contexto del límite y el saldo del emisor, y despeja el mayor monto enviable (HU-NF-06, criterio 14).
func montoMaximoEnviable(ctx context.Context, q *repositorio.Queries, usuarioID, nivelKYC string, momento time.Time) (Centavos, error) {
	contexto, err := leerContextoDeLimite(ctx, q, usuarioID, nivelKYC, momento)
	if err != nil {
		return 0, err
	}
	saldo, err := q.SaldoDeCuenta(ctx, NombreCuentaDisponible(usuarioID))
	if err != nil {
		return 0, fmt.Errorf("nucleo: leer el saldo de %q: %w", usuarioID, err)
	}
	disponible, err := Centavos(0).Restar(Centavos(saldo))
	if err != nil {
		return 0, err
	}
	return despejeDelTope(contexto, disponible)
}

// despejeDelTope resuelve los tres casos del criterio y recorta por el tope por operación y el cupo mensual restante (HU-NF-06, criterio 14).
func despejeDelTope(contexto contextoDeLimite, saldo Centavos) (Centavos, error) {
	topeMensual, err := topeDeRegla(contexto.reglas, TipoReglaRechaza, VentanaLimiteMensual, contexto.valorUVT)
	if err != nil {
		return 0, err
	}
	reglaGMF, ok := reglaGMFVigente(contexto.reglas)
	if !ok {
		return 0, ErrReglaGMFNoVigente
	}
	topeExento, err := centavosDesdeTopeUVT(reglaGMF.TopeUVTCentesimas, contexto.valorUVT)
	if err != nil {
		return 0, err
	}
	monto, err := montoSinCupo(saldo, contexto.acumulado, topeExento, reglaGMF.TarifaPPM)
	if err != nil {
		return 0, err
	}
	restante, err := cupoRestante(topeMensual, contexto.acumulado)
	if err != nil {
		return 0, err
	}
	topeOperacion, err := topeDeRegla(contexto.reglas, TipoReglaRechaza, VentanaLimiteOperacion, contexto.valorUVT)
	if err != nil {
		return 0, err
	}
	if monto > restante {
		monto = restante
	}
	if monto > topeOperacion {
		monto = topeOperacion
	}
	return monto, nil
}

// topeDeRegla convierte el tope de la regla vigente del tipo y la ventana pedidos; sin ella falla cerrado (HU-NF-06, criterios 10 y 13).
func topeDeRegla(reglas []ReglaLimite, tipo, ventana string, valorUVTCentavos int64) (Centavos, error) {
	for _, regla := range reglas {
		if regla.Tipo == tipo && regla.Ventana == ventana {
			return centavosDesdeTopeUVT(regla.TopeUVTCentesimas, valorUVTCentavos)
		}
	}
	if tipo == TipoReglaGrava {
		return 0, ErrReglaGMFNoVigente
	}
	return 0, ErrReglaLimiteNoVigente
}

// montoSinCupo despeja el mayor monto enviable por saldo y GMF, sin mirar todavía el cupo mensual (HU-NF-06, criterio 14).
func montoSinCupo(saldo, acumulado, topeExento Centavos, tarifaPPM int64) (Centavos, error) {
	if saldo <= 0 {
		return 0, nil
	}
	if acumulado < 0 {
		return 0, ErrMontoInvalido
	}
	alcance, err := saldo.Sumar(acumulado)
	if err != nil {
		return 0, err
	}
	if alcance <= topeExento {
		return saldo, nil
	}
	if acumulado >= topeExento {
		return despejeConGravamen(saldo, tarifaPPM)
	}
	exentoRestante, err := topeExento.Restar(acumulado)
	if err != nil {
		return 0, err
	}
	resto, err := saldo.Restar(exentoRestante)
	if err != nil {
		return 0, err
	}
	parteGravada, err := despejeConGravamen(resto, tarifaPPM)
	if err != nil {
		return 0, err
	}
	return exentoRestante.Sumar(parteGravada)
}

// cupoRestante es lo que falta del cupo mensual para el acumulado ya aplicado (HU-NF-06, criterio 11).
func cupoRestante(topeMensual, acumulado Centavos) (Centavos, error) {
	if acumulado >= topeMensual {
		return 0, nil
	}
	return topeMensual.Restar(acumulado)
}

// despejeConGravamen es el mayor monto cuyo exigido, el monto más su GMF, no supera el saldo dado (HU-NF-06, criterio 14).
func despejeConGravamen(saldo Centavos, tarifaPPM int64) (Centavos, error) {
	if saldo <= 0 {
		return 0, nil
	}
	if saldo.Int64() > math.MaxInt64/ppmEscala {
		return 0, ErrDesbordamiento
	}
	monto := Centavos(saldo.Int64() * ppmEscala / (ppmEscala + tarifaPPM))
	for {
		cabe, err := saldoCubre(monto+1, saldo, tarifaPPM)
		if err != nil {
			return 0, err
		}
		if !cabe {
			return monto, nil
		}
		monto++
	}
}

// saldoCubre reporta si el disponible alcanza para el monto más su GMF (HU-NF-06, criterio 13.4).
func saldoCubre(monto, saldo Centavos, tarifaPPM int64) (bool, error) {
	if monto <= 0 {
		return true, nil
	}
	gravamen, err := gravamenSobreExceso(monto, tarifaPPM)
	if err != nil {
		return false, err
	}
	exigido, err := SaldoExigido(monto, gravamen)
	if err != nil {
		return false, err
	}
	return exigido <= saldo, nil
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

// reglaGMFVigente devuelve la regla que grava el mes, la única que aplica el 4x1000 (HU-NF-06, criterio 13).
func reglaGMFVigente(reglas []ReglaLimite) (ReglaLimite, bool) {
	for _, regla := range reglas {
		if regla.Tipo == TipoReglaGrava && regla.Ventana == VentanaLimiteMensual {
			return regla, true
		}
	}
	return ReglaLimite{}, false
}

// evaluarGMF devuelve el 4x1000 solo sobre lo que el envío excede del tope exento mensual (HU-NF-06, criterio 13).
func evaluarGMF(reglas []ReglaLimite, valorUVTCentavos int64, acumulado, monto Centavos) (Centavos, error) {
	regla, ok := reglaGMFVigente(reglas)
	if !ok {
		return 0, ErrReglaGMFNoVigente
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
