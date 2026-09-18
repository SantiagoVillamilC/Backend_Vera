package nucleo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
)

// momentoDeMontoMaximo es la fecha de referencia del mes y del año de UVT que usan estas pruebas.
var momentoDeMontoMaximo = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// reglasDeMontoMaximo son las reglas vigentes del único nivel: el cupo mensual de 210,50 UVT y el GMF de 65 UVT (HU-NF-06).
func reglasDeMontoMaximo() []ReglaLimite {
	return []ReglaLimite{
		{Tipo: TipoReglaRechaza, Ventana: VentanaLimiteOperacion, TopeUVTCentesimas: 21050},
		{Tipo: TipoReglaRechaza, Ventana: VentanaLimiteMensual, TopeUVTCentesimas: 21050},
		reglaGMFDePrueba(),
	}
}

// TestHU_NF_06_Criterio14_MontoMaximoEnLosTresCasos cubre los tres casos de la fórmula y el tope por cupo (HU-NF-06, criterio 14).
func TestHU_NF_06_Criterio14_MontoMaximoEnLosTresCasos(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		saldo     Centavos
		acumulado Centavos
		querer    Centavos
	}{
		"sin cruzar el tope exento el máximo es el saldo":    {saldo: 100000000, acumulado: 0, querer: 100000000},
		"con el tope exento agotado el máximo descuenta GMF": {saldo: 100000000, acumulado: topeExentoGMF2026, querer: 99601594},
		"con el envío cruzando el tope grava solo el exceso": {saldo: 200000000, acumulado: topeExentoGMF2026 - 100000000, querer: 199601594},
		"el cupo mensual restante recorta el resultado":      {saldo: 5000000000, acumulado: topeCanonico2026 - 1000, querer: 1000},
		"el cupo mensual agotado deja el máximo en cero":     {saldo: 5000000000, acumulado: topeCanonico2026 + 5, querer: 0},
		"saldo cero deja el máximo en cero":                  {saldo: 0, acumulado: 0, querer: 0},
		"un centavo bajo el tope ya cruza y grava el exceso": {saldo: 100000000, acumulado: topeExentoGMF2026 - 1, querer: 99601594},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			contexto := contextoDeLimite{reglas: reglasDeMontoMaximo(), valorUVT: uvts2026, acumulado: caso.acumulado}
			monto, err := despejeDelTope(contexto, caso.saldo)
			if err != nil {
				t.Fatalf("despejeDelTope: %v, quiero nil", err)
			}
			if monto != caso.querer {
				t.Fatalf("monto máximo = %d, quiero %d", monto, caso.querer)
			}

			topeMensual, err := centavosDesdeTopeUVT(21050, uvts2026)
			if err != nil {
				t.Fatalf("tope mensual: %v", err)
			}
			restante, err := cupoRestante(topeMensual, caso.acumulado)
			if err != nil {
				t.Fatalf("cupoRestante: %v", err)
			}
			if monto > restante {
				t.Errorf("monto = %d supera el cupo restante %d", monto, restante)
			}

			verificarMontoEnviable(t, reglasDeMontoMaximo(), caso.acumulado, monto, caso.saldo, true)
			verificarMontoEnviable(t, reglasDeMontoMaximo(), caso.acumulado, monto+1, caso.saldo, false)
		})
	}
}

// verificarMontoEnviable exige que el saldo cubra el monto más su GMF y el cupo lo admita, o lo contrario cuando cabe es false (HU-NF-06, criterios 11 y 13.4).
func verificarMontoEnviable(t *testing.T, reglas []ReglaLimite, acumulado, monto, saldo Centavos, cabe bool) {
	t.Helper()

	if cabe && monto <= 0 {
		return
	}
	gravamen, err := evaluarGMF(reglas, uvts2026, acumulado, monto)
	if err != nil {
		t.Fatalf("evaluarGMF(%d): %v", monto, err)
	}
	exigido, err := SaldoExigido(monto, gravamen)
	if err != nil {
		t.Fatalf("SaldoExigido(%d): %v", monto, err)
	}
	topeMensual, err := centavosDesdeTopeUVT(21050, uvts2026)
	if err != nil {
		t.Fatalf("tope mensual: %v", err)
	}
	total, err := acumulado.Sumar(monto)
	if err != nil {
		t.Fatalf("acumulado + monto: %v", err)
	}
	cabeSaldo := exigido <= saldo
	cabeCupo := total <= topeMensual
	if cabe && !cabeSaldo {
		t.Errorf("el monto %d no cabe en el saldo %d: exigido %d", monto, saldo, exigido)
	}
	if cabe && !cabeCupo {
		t.Errorf("el monto %d no cabe en el cupo: acumulado + monto = %d", monto, total)
	}
	if !cabe && cabeSaldo && cabeCupo {
		t.Errorf("un centavo más que el máximo (%d) todavía cabría: exigido %d, total %d", monto, exigido, total)
	}
}

// TestHU_NF_06_Criterio14_MontoMaximoRecortaPorTopeDeOperacion cubre un tope por operación menor que el mensual (HU-NF-06, criterio 14).
func TestHU_NF_06_Criterio14_MontoMaximoRecortaPorTopeDeOperacion(t *testing.T) {
	t.Parallel()

	reglas := reglasDeMontoMaximo()
	reglas[0].TopeUVTCentesimas = 1000
	topeOperacion, err := centavosDesdeTopeUVT(1000, uvts2026)
	if err != nil {
		t.Fatalf("tope de operación: %v", err)
	}

	casos := map[string]struct {
		saldo     Centavos
		acumulado Centavos
	}{
		"acumulado en cero":    {saldo: 5000000000, acumulado: 0},
		"acumulado intermedio": {saldo: 5000000000, acumulado: topeOperacion / 2},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			contexto := contextoDeLimite{reglas: reglas, valorUVT: uvts2026, acumulado: caso.acumulado}
			monto, err := despejeDelTope(contexto, caso.saldo)
			if err != nil {
				t.Fatalf("despejeDelTope: %v, quiero nil", err)
			}
			if monto != topeOperacion {
				t.Fatalf("monto máximo = %d, quiero el tope por operación %d", monto, topeOperacion)
			}

			verificarMontoEnviable(t, reglas, caso.acumulado, monto, caso.saldo, true)
			if err := evaluarLimite(reglas, uvts2026, caso.acumulado, monto+1); !errors.Is(err, ErrTopeOperacionExcedido) {
				t.Errorf("un centavo más que %d: err = %v, quiero ErrTopeOperacionExcedido", monto, err)
			}
		})
	}
}

// TestHU_NF_06_Criterio14_MontoMaximoFallaCerradoSinReglas cubre la ausencia de las dos reglas que la fórmula necesita (HU-NF-06, criterio 14).
func TestHU_NF_06_Criterio14_MontoMaximoFallaCerradoSinReglas(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		reglas []ReglaLimite
		querer error
	}{
		"sin regla que grava":    {reglas: []ReglaLimite{{Tipo: TipoReglaRechaza, Ventana: VentanaLimiteMensual, TopeUVTCentesimas: 21050}}, querer: ErrReglaGMFNoVigente},
		"sin regla de cupo":      {reglas: []ReglaLimite{reglaGMFDePrueba()}, querer: ErrReglaLimiteNoVigente},
		"sin regla de operación": {reglas: []ReglaLimite{{Tipo: TipoReglaRechaza, Ventana: VentanaLimiteMensual, TopeUVTCentesimas: 21050}, reglaGMFDePrueba()}, querer: ErrReglaLimiteNoVigente},
		"sin ninguna regla":      {reglas: nil, querer: ErrReglaLimiteNoVigente},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			contexto := contextoDeLimite{reglas: caso.reglas, valorUVT: uvts2026, acumulado: 0}
			if _, err := despejeDelTope(contexto, 1000000); !errors.Is(err, caso.querer) {
				t.Fatalf("error = %v, quiero %v", err, caso.querer)
			}
		})
	}
}

// TestHU_NF_06_Criterio14_MontoMaximoSinUVTRechazaSinTocarLaBase cubre que falte la UVT del año del cálculo (HU-NF-06, criterio 14).
func TestHU_NF_06_Criterio14_MontoMaximoSinUVTRechazaSinTocarLaBase(t *testing.T) {
	t.Parallel()

	_, err := montoMaximoEnviable(t.Context(), repositorio.New(dbSinUVT{}), uuid.NewString(), NivelKYCDepositoBajoMonto, momentoDeMontoMaximo)
	if !errors.Is(err, ErrValorUVTNoVigente) {
		t.Fatalf("error = %v, quiero ErrValorUVTNoVigente", err)
	}
}

// TestHU_NF_06_Criterio14_MontoMaximoSinKYCFallaCerrado cubre que el servicio no consulte sin el lector de nivel de KYC (HU-NF-06, criterio 14).
func TestHU_NF_06_Criterio14_MontoMaximoSinKYCFallaCerrado(t *testing.T) {
	t.Parallel()

	s := NewServicio(nil, nil, nil, nil, nil)
	if _, err := s.MontoMaximoEnviable(context.Background(), uuid.NewString(), momentoDeMontoMaximo); !errors.Is(err, ErrKYCNoDisponible) {
		t.Fatalf("error = %v, quiero ErrKYCNoDisponible", err)
	}
}
