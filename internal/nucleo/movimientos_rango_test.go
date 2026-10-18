package nucleo

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestRangoMovimientosColombiaY366Dias(t *testing.T) {
	for _, c := range []struct {
		desde, hasta string
		valido       bool
	}{{"2024-01-01", "2024-12-31", true}, {"2024-01-01", "2025-01-01", false}, {"2026-02-29", "2026-03-01", false}, {"2026-10-19", "2026-10-18", false}, {"2026-10-18", "2026-10-18", true}} {
		desde, hasta, err := RangoMovimientosColombia(c.desde, c.hasta)
		if (err == nil) != c.valido {
			t.Fatalf("%+v: %v", c, err)
		}
		if c.valido {
			_, offset := desde.Zone()
			if offset != -5*3600 || desde.Hour() != 0 || hasta.Hour() != 0 || hasta.Sub(desde) > 366*24*time.Hour {
				t.Fatal("corte incorrecto")
			}
		}
	}
}

func TestResumenPorMotivoNoDuplicaSubcuentas(t *testing.T) {
	a := nuevoResumenPeriodo("motivo")
	for _, m := range []MovimientoPeriodo{
		{Lineas: []LineaMovimiento{{Monto: 1000, Motivo: MotivoDeposito}}},
		{ContraparteRef: "a", Lineas: []LineaMovimiento{{Monto: -500, Motivo: MotivoEnvio}, {Monto: -2, Motivo: MotivoGravamen}}},
		{ContraparteRef: "a", Lineas: []LineaMovimiento{{Monto: 100, Motivo: MotivoDevolucion}}},
	} {
		if err := a.anexar(m); err != nil {
			t.Fatal(err)
		}
	}
	r, err := a.finalizar()
	if err != nil {
		t.Fatal(err)
	}
	if r.Cantidad != 3 || r.Entradas != 1100 || r.Salidas != 502 || r.Neto != 598 || len(r.Grupos) != 4 || r.Grupos[0].Clave != "deposito" || r.Grupos[0].Total != 1000 {
		t.Fatalf("resumen: %+v", r)
	}
}

func TestResumenContrapartesDiezGruposYOverflow(t *testing.T) {
	a := nuevoResumenPeriodo("contraparte")
	for i := 0; i < 12; i++ {
		if err := a.anexar(MovimientoPeriodo{ContraparteRef: string(rune('a' + i)), Lineas: []LineaMovimiento{{Monto: Centavos(i + 1), Motivo: MotivoTransferenciaRecibida}}}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := a.finalizar()
	if err != nil || len(r.Grupos) != 10 || !r.HayMas || r.Cantidad != 12 || r.Grupos[0].Total != 12 || r.Entradas != 78 {
		t.Fatalf("top grupos: %+v %v", r, err)
	}
	a = nuevoResumenPeriodo("motivo")
	if err := a.anexar(MovimientoPeriodo{Lineas: []LineaMovimiento{{Monto: math.MinInt64, Motivo: MotivoEnvio}}}); !errors.Is(err, ErrDesbordamiento) {
		t.Fatal(err)
	}
	a = nuevoResumenPeriodo("motivo")
	if err := a.anexar(MovimientoPeriodo{Lineas: []LineaMovimiento{{Monto: math.MaxInt64, Motivo: MotivoDeposito}, {Monto: 1, Motivo: MotivoDeposito}}}); !errors.Is(err, ErrDesbordamiento) {
		t.Fatal(err)
	}
}
