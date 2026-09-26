package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestDemoraRespetaLaBanda comprueba que la variación nunca sale del rango declarado.
func TestDemoraRespetaLaBanda(t *testing.T) {
	t.Parallel()

	perillas := Perillas{Latencia: 100 * time.Millisecond, Variacion: 25 * time.Millisecond}
	limiteBajo, limiteAlto := 75*time.Millisecond, 125*time.Millisecond
	for range 1000 {
		if d := perillas.Demora(); d < limiteBajo || d > limiteAlto {
			t.Fatalf("Demora() = %v, quiero estar en [%v, %v]", d, limiteBajo, limiteAlto)
		}
	}
}

// TestDemoraSinVariacionEsLaLatencia fija el comportamiento determinista con variación cero.
func TestDemoraSinVariacionEsLaLatencia(t *testing.T) {
	t.Parallel()

	perillas := Perillas{Latencia: 100 * time.Millisecond}
	for range 100 {
		if d := perillas.Demora(); d != perillas.Latencia {
			t.Fatalf("Demora() = %v, quiero %v", d, perillas.Latencia)
		}
	}
}

// TestOcurreExtremosDeterministas cubre los dos extremos de la probabilidad de rechazo.
func TestOcurreExtremosDeterministas(t *testing.T) {
	t.Parallel()

	for range 1000 {
		if ocurre(0) {
			t.Fatal("ocurre(0) devolvió verdadero, quiero falso siempre")
		}
		if !ocurre(1) {
			t.Fatal("ocurre(1) devolvió falso, quiero verdadero siempre")
		}
	}
}

// TestValidaRechazaFueraDeRango cubre las perillas inválidas.
func TestValidaRechazaFueraDeRango(t *testing.T) {
	t.Parallel()

	base := Perillas{ModoCaida: ModoCaidaNinguna}
	tests := map[string]Perillas{
		"p_rechazo negativo":      {ModoCaida: ModoCaidaNinguna, PRechazo: -0.1},
		"p_rechazo mayor que uno": {ModoCaida: ModoCaidaNinguna, PRechazo: 1.1},
		"p_perdida mayor que uno": {ModoCaida: ModoCaidaNinguna, PRespuestaPerdida: 1.1},
		"latencia negativa":       {ModoCaida: ModoCaidaNinguna, Latencia: -time.Second},
		"variacion negativa":      {ModoCaida: ModoCaidaNinguna, Variacion: -time.Second},
		"modo desconocido":        {ModoCaida: "apagado"},
	}
	for name, perillas := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if err := perillas.Valida(); !errors.Is(err, ErrPerillasInvalidas) {
				t.Errorf("Valida() error = %v, quiero ErrPerillasInvalidas", err)
			}
		})
	}
	if err := base.Valida(); err != nil {
		t.Errorf("Valida() de las perillas base = %v, quiero nil", err)
	}
}

// TestControlCambiaBajoCandado cambia y lee las perillas desde varias goroutines.
func TestControlCambiaBajoCandado(t *testing.T) {
	t.Parallel()

	control := NuevoControl(Perillas{ModoCaida: ModoCaidaNinguna})
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 100 {
				if i%2 == 0 {
					control.Fijar(Perillas{ModoCaida: ModoCaidaSilencio, PRechazo: 0.5})
					continue
				}
				_ = control.Actuales()
			}
		})
	}
	wg.Wait()
}
