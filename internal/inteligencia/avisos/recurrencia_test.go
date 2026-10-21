package avisos

import (
	"testing"
	"time"
)

func fechasCada(dia time.Time, paso time.Duration, cantidad int) []time.Time {
	fechas := make([]time.Time, 0, cantidad)
	for i := range cantidad {
		fechas = append(fechas, dia.Add(time.Duration(i)*paso))
	}
	return fechas
}

func TestIndiceConDosPagosNoAlcanzaElUmbral(t *testing.T) {
	cfg := ConfiguracionPorDefecto().Recurrencia
	fechas := fechasCada(time.Date(2026, 6, 1, 0, 0, 0, 0, Zona), 20*24*time.Hour, 2)
	indice := CalcularIndice(fechas, []int64{15_000_000, 15_200_000}, cfg)
	if indice.EsRecurrente {
		t.Fatalf("dos pagos no deberían ser recurrentes: %+v", indice)
	}
	// N es 2/3, Monto 1 y Fecha 0 porque 20 días está a 6 de la cadencia de 14.
	esperado := Rat("0.4")
	if indice.R.Cmp(esperado) != 0 {
		t.Fatalf("R = %s, want %s", indice.R.RatString(), esperado.RatString())
	}
}

func TestIndiceSeisMensualesEsMadura(t *testing.T) {
	cfg := ConfiguracionPorDefecto().Recurrencia
	var fechas []time.Time
	montos := []int64{}
	for _, mes := range mesesDePrueba {
		fechas = append(fechas, time.Date(mes.Anio, mes.Mes, 17, 0, 0, 0, 0, Zona))
		montos = append(montos, 80_000_000)
	}
	serie := EvaluarSerie(fechas, montos, time.Date(2026, 7, 15, 0, 0, 0, 0, Zona), cfg)
	if serie.Estado != Madura {
		t.Fatalf("estado = %q, want madura", serie.Estado)
	}
	if serie.Indice.R.Cmp(Entero(1)) != 0 {
		t.Fatalf("R = %s, want 1", serie.Indice.R.RatString())
	}
	if serie.Proximo == nil || serie.Proximo.Centro.Format("2006-01-02") != "2026-07-17" {
		t.Fatalf("próximo = %+v", serie.Proximo)
	}
	if serie.Cadencia != "mensual" {
		t.Fatalf("cadencia = %q", serie.Cadencia)
	}
}

func TestUmbralInclusivo(t *testing.T) {
	cfg := ConfiguracionPorDefecto().Recurrencia
	cfg.WMonto = Rat("0.5")
	cfg.WFecha = Rat("0.5")
	cfg.Umbral = Rat("0.5")
	fechas := fechasCada(time.Date(2026, 6, 1, 0, 0, 0, 0, Zona), 20*24*time.Hour, 3)
	indice := CalcularIndice(fechas, []int64{1_000_000, 1_000_000, 1_000_000}, cfg)
	if indice.R.Cmp(Rat("0.5")) != 0 {
		t.Fatalf("R = %s, want 0.5 exacto", indice.R.RatString())
	}
	if !indice.EsRecurrente {
		t.Fatal("un R exactamente igual al umbral debe ser recurrente")
	}
}

func TestEmpateDeCadenciaEligeLaMenor(t *testing.T) {
	cfg := ConfiguracionPorDefecto().Recurrencia
	// Intervalos de 22 días: está a 8 de 14 y a 8 de 30, así que gana 14.
	fechas := fechasCada(time.Date(2026, 6, 1, 0, 0, 0, 0, Zona), 22*24*time.Hour, 3)
	indice := CalcularIndice(fechas, []int64{1_000_000, 1_000_000, 1_000_000}, cfg)
	if indice.CadenciaDias != 14 {
		t.Fatalf("cadencia = %d, want 14", indice.CadenciaDias)
	}
}

func TestUnSoloPagoNoEsRecurrente(t *testing.T) {
	cfg := ConfiguracionPorDefecto().Recurrencia
	indice := CalcularIndice([]time.Time{time.Date(2026, 7, 1, 0, 0, 0, 0, Zona)}, []int64{1}, cfg)
	if indice.EsRecurrente || indice.R.Sign() != 0 {
		t.Fatalf("índice = %+v", indice)
	}
}

func TestEstadosDeLaSerie(t *testing.T) {
	cfg := ConfiguracionPorDefecto().Recurrencia
	mensual := func(cantidad int) []time.Time {
		var fechas []time.Time
		for i := range cantidad {
			fechas = append(fechas, time.Date(2026, time.Month(1+i), 17, 0, 0, 0, 0, Zona))
		}
		return fechas
	}
	montos := func(cantidad int) []int64 {
		salida := make([]int64, cantidad)
		for i := range salida {
			salida[i] = 80_000_000
		}
		return salida
	}
	// Tres pagos y hoy un mes después del último: un ciclo perdido, queda dormida.
	serie := EvaluarSerie(mensual(3), montos(3), time.Date(2026, 5, 20, 0, 0, 0, 0, Zona), cfg)
	if serie.Estado != Dormida || serie.CiclosPerdidos != 1 || serie.Proximo == nil {
		t.Fatalf("un ciclo perdido: %+v", serie)
	}
	// Dos ciclos perdidos: cancelada y sin próximo.
	serie = EvaluarSerie(mensual(3), montos(3), time.Date(2026, 6, 20, 0, 0, 0, 0, Zona), cfg)
	if serie.Estado != Cancelada || serie.Proximo != nil {
		t.Fatalf("dos ciclos perdidos: %+v", serie)
	}
	// Emergente con un ciclo perdido no tiene estado.
	serie = EvaluarSerie(mensual(2), montos(2), time.Date(2026, 4, 20, 0, 0, 0, 0, Zona), cfg)
	if serie.Estado != "" {
		t.Fatalf("emergente con ciclo perdido: %+v", serie)
	}
}

func TestDeteccionAgrupaPorIdentidad(t *testing.T) {
	cfg := ConfiguracionPorDefecto()
	pagos := []Pago{
		pagoDe(mercado, time.Date(2026, 5, 4, 0, 0, 0, 0, Zona), 10, "A", "alimentos_mercado"),
		pagoDe(mercado, time.Date(2026, 6, 4, 0, 0, 0, 0, Zona), 10, "B", "alimentos_mercado"),
		pagoDe(nueva, time.Date(2026, 6, 5, 0, 0, 0, 0, Zona), 20, "C", "entretenimiento"),
	}
	series := DetectarSeries(pagos, time.Date(2026, 7, 15, 0, 0, 0, 0, Zona), cfg.Recurrencia)
	if len(series) != 2 {
		t.Fatalf("series = %d, want 2", len(series))
	}
	if series[0].Identidad != mercado || series[1].Identidad != nueva {
		t.Fatalf("orden de las series: %+v", series)
	}
}
