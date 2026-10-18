package nucleo

import (
	"errors"
	"testing"
)

func TestDesgloseLimitesReutilizaMotor(t *testing.T) {
	for nombre, caso := range map[string]struct{ saldo, acumulado, maximo, porSaldo, gmf Centavos }{
		"sinGMF":      {100000000, 0, 100000000, 100000000, 0},
		"conGMF":      {100000000, topeExentoGMF2026, 99601594, 99601594, 398406},
		"cupoRecorta": {5000000000, topeCanonico2026 - 1000, 1000, 4980079681, 4},
		"cupoAgotado": {100000000, topeCanonico2026, 0, 99601594, 0},
		"sinSaldo":    {0, 0, 0, 0, 0},
	} {
		t.Run(nombre, func(t *testing.T) {
			c := contextoDeLimite{reglas: reglasDeMontoMaximo(), valorUVT: uvts2026, acumulado: caso.acumulado}
			d, err := desgloseDelTope(c, caso.saldo)
			if err != nil {
				t.Fatal(err)
			}
			if d.MaximoEnviable != caso.maximo || d.SaldoDespuesGMF != caso.porSaldo || d.GMF != caso.gmf || d.AplicaGMF != (caso.gmf > 0) || d.TopeOperacion != topeCanonico2026 {
				t.Fatalf("desglose: %+v", d)
			}
			restante, err := cupoRestante(topeCanonico2026, caso.acumulado)
			if err != nil || d.CupoMensualRestante != restante {
				t.Fatalf("cupo: %+v %v", d, err)
			}
			anterior, err := despejeDelTope(c, caso.saldo)
			if err != nil || anterior != d.MaximoEnviable {
				t.Fatalf("divergencia del motor: %d %v", anterior, err)
			}
		})
	}
}

func TestDesgloseLimitesFallaSinReglas(t *testing.T) {
	for _, caso := range []struct {
		tipo, ventana string
		esperado      error
	}{{TipoReglaRechaza, VentanaLimiteOperacion, ErrReglaLimiteNoVigente}, {TipoReglaRechaza, VentanaLimiteMensual, ErrReglaLimiteNoVigente}, {TipoReglaGrava, VentanaLimiteMensual, ErrReglaGMFNoVigente}} {
		reglas := make([]ReglaLimite, 0)
		for _, r := range reglasDeMontoMaximo() {
			if r.Tipo != caso.tipo || r.Ventana != caso.ventana {
				reglas = append(reglas, r)
			}
		}
		if _, err := desgloseDelTope(contextoDeLimite{reglas: reglas, valorUVT: uvts2026}, 100); !errors.Is(err, caso.esperado) {
			t.Fatal(err)
		}
	}
}
