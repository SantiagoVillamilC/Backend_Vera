package avisos

import "testing"

// hechoPresupuesto arma un hecho de presupuesto anticipado cuyo texto reproduce sus cifras.
func hechoPresupuesto(clave string) Hecho {
	return Hecho{
		Tipo: PresupuestoAnticipado, Causa: CausaR1, Clave: clave,
		MontoCentavos: 150_000_000, ReferenciaCentavos: puntero(90_000_000),
		Mes: mesPuntero(Mes{2026, 7}), SeveridadCentavos: 60_000_000,
	}
}

// TestTarjetasDelDiaConRecuentoCuentaElRecorte mide lo que el cupo deja fuera, que antes no dejaba
// rastro: dos candidatas y cupo de una sola.
func TestTarjetasDelDiaConRecuentoCuentaElRecorte(t *testing.T) {
	t.Parallel()
	hechos := []Hecho{hechoPresupuesto("gasto-total"), hechoPresupuesto("otra-clave")}

	tarjetas, recuento, err := TarjetasDelDiaConRecuento(hechos, nil, 0, 1)
	if err != nil {
		t.Fatalf("tarjetas: %v", err)
	}
	if len(tarjetas) != 1 {
		t.Fatalf("tarjetas = %d, quiero 1", len(tarjetas))
	}
	if recuento != (Recuento{Hechos: 2, Candidatas: 2, Elegidas: 1, Recortadas: 1}) {
		t.Fatalf("recuento = %+v", recuento)
	}
}

// TestTarjetasDelDiaConRecuentoCuentaLasYaEmitidas no confunde una deduplicación con un recorte.
func TestTarjetasDelDiaConRecuentoCuentaLasYaEmitidas(t *testing.T) {
	t.Parallel()
	hechos := []Hecho{hechoPresupuesto("gasto-total"), hechoPresupuesto("otra-clave")}
	yaEmitidas := map[ClaveDeAviso]struct{}{Clave(hechos[0]): {}}

	tarjetas, recuento, err := TarjetasDelDiaConRecuento(hechos, yaEmitidas, 0, 3)
	if err != nil {
		t.Fatalf("tarjetas: %v", err)
	}
	if len(tarjetas) != 1 {
		t.Fatalf("tarjetas = %d, quiero 1", len(tarjetas))
	}
	if recuento.YaEmitidas != 1 {
		t.Fatalf("ya emitidas = %d, quiero 1", recuento.YaEmitidas)
	}
	if recuento.Recortadas != 0 {
		t.Fatalf("recortadas = %d, una deduplicación no es un recorte", recuento.Recortadas)
	}
	if recuento.Candidatas != 1 {
		t.Fatalf("candidatas = %d, quiero 1", recuento.Candidatas)
	}
}

// TestTarjetasDelDiaConRecuentoConCupoAgotado recorta todo lo candidato sin tocar la lista.
func TestTarjetasDelDiaConRecuentoConCupoAgotado(t *testing.T) {
	t.Parallel()
	hechos := []Hecho{hechoPresupuesto("gasto-total")}

	tarjetas, recuento, err := TarjetasDelDiaConRecuento(hechos, nil, 3, 3)
	if err != nil {
		t.Fatalf("tarjetas: %v", err)
	}
	if len(tarjetas) != 0 {
		t.Fatalf("tarjetas = %d, quiero 0", len(tarjetas))
	}
	if recuento.Recortadas != 1 {
		t.Fatalf("recortadas = %d, quiero 1", recuento.Recortadas)
	}
}

// TestTarjetasDelDiaDevuelveLoMismoQueElRecuento blinda el envoltorio que ya usaban las pruebas.
func TestTarjetasDelDiaDevuelveLoMismoQueElRecuento(t *testing.T) {
	t.Parallel()
	hechos := []Hecho{hechoPresupuesto("gasto-total"), hechoPresupuesto("otra-clave")}

	conRecuento, _, err := TarjetasDelDiaConRecuento(hechos, nil, 0, 1)
	if err != nil {
		t.Fatalf("tarjetas: %v", err)
	}
	sinRecuento, err := TarjetasDelDia(hechos, nil, 0, 1)
	if err != nil {
		t.Fatalf("tarjetas: %v", err)
	}
	if len(conRecuento) != len(sinRecuento) {
		t.Fatalf("largos distintos: %d y %d", len(conRecuento), len(sinRecuento))
	}
	for i := range conRecuento {
		if conRecuento[i] != sinRecuento[i] {
			t.Fatalf("tarjeta %d distinta: %+v y %+v", i, conRecuento[i], sinRecuento[i])
		}
	}
}

// TestTarjetasDelDiaConRecuentoFallaCerradaSiElTextoNoReproduce conserva el fallo cerrado.
func TestTarjetasDelDiaConRecuentoFallaCerradaSiElTextoNoReproduce(t *testing.T) {
	t.Parallel()
	hechos := []Hecho{{
		Tipo: PresupuestoAnticipado, Causa: CausaR1, Clave: "gasto-total",
		MontoCentavos: 150_000_000, ReferenciaCentavos: puntero(-1), Mes: mesPuntero(Mes{2026, 7}),
	}}
	if _, _, err := TarjetasDelDiaConRecuento(hechos, nil, 0, 3); err == nil {
		t.Fatal("se esperaba un error de texto")
	}
}
