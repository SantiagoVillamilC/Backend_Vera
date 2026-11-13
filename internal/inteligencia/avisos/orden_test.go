package avisos

import (
	"testing"
	"time"
)

// TestAgruparPorPeriodoSeparaPorElMesDelHecho es el núcleo del cupo por período: dos hechos del mismo
// mes van juntos aunque la tarjeta se entregue después, y los grupos salen en orden de mes.
func TestAgruparPorPeriodoSeparaPorElMesDelHecho(t *testing.T) {
	t.Parallel()
	junio := Mes{Anio: 2026, Mes: time.June}
	julio := Mes{Anio: 2026, Mes: time.July}
	hechos := []Hecho{
		{Tipo: PresupuestoAnticipado, Clave: "gasto-total", Mes: &julio},
		{Tipo: CaidaIngreso, Clave: "ingreso-mensual", Mes: &junio},
		{Tipo: CategoriaSobreMedia, Clave: "alimentos_mercado", Mes: &julio},
	}

	grupos := AgruparPorPeriodo(hechos, time.Date(2026, time.July, 15, 12, 0, 0, 0, Zona))
	if len(grupos) != 2 {
		t.Fatalf("grupos = %d, quiero 2: junio y julio", len(grupos))
	}
	if !grupos[0].Inicio.Equal(junio.Fecha()) {
		t.Fatalf("primer grupo = %s, quiero junio", grupos[0].Inicio.Format("2006-01"))
	}
	if len(grupos[0].Hechos) != 1 || grupos[0].Hechos[0].Clave != "ingreso-mensual" {
		t.Fatalf("hechos de junio = %+v", grupos[0].Hechos)
	}
	if !grupos[1].Inicio.Equal(julio.Fecha()) {
		t.Fatalf("segundo grupo = %s, quiero julio", grupos[1].Inicio.Format("2006-01"))
	}
	if len(grupos[1].Hechos) != 2 {
		t.Fatalf("hechos de julio = %d, quiero 2", len(grupos[1].Hechos))
	}
	if grupos[1].Hechos[0].Clave != "gasto-total" || grupos[1].Hechos[1].Clave != "alimentos_mercado" {
		t.Fatalf("el grupo de julio cambió el orden de los hechos: %+v", grupos[1].Hechos)
	}
}

// TestMesDelHechoPrefiereElMesYDespuesLaFecha fija la precedencia y la rama de un hecho sin período,
// que cae en el mes de ahora porque no hay otra fecha de la que sacarlo.
func TestMesDelHechoPrefiereElMesYDespuesLaFecha(t *testing.T) {
	t.Parallel()
	ahora := time.Date(2026, time.August, 15, 12, 0, 0, 0, Zona)
	fecha := time.Date(2026, time.May, 20, 15, 0, 0, 0, time.UTC)
	mesExplicito := Mes{Anio: 2026, Mes: time.March}

	casos := map[string]struct {
		hecho Hecho
		mes   Mes
	}{
		"sin período ni fecha": {hecho: Hecho{Clave: "suelto"}, mes: Mes{Anio: 2026, Mes: time.August}},
		"con fecha":            {hecho: Hecho{Clave: "con-fecha", Fecha: &fecha}, mes: Mes{Anio: 2026, Mes: time.May}},
		"con período y fecha": {
			hecho: Hecho{Clave: "los-dos", Mes: &mesExplicito, Fecha: &fecha},
			mes:   mesExplicito,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if mes := mesDelHecho(caso.hecho, ahora); mes != caso.mes {
				t.Fatalf("mes = %s, quiero %s", mes.Clave(), caso.mes.Clave())
			}
		})
	}
}

// TestAgruparPorPeriodoJuntaElHechoSinPeriodoConElMesDeAhora fija el caso que el emisor usa cuando un
// hecho trae período y otro no: el que trae período conserva el suyo y el que no lo trae cae en el mes
// inyectado, en un grupo propio.
func TestAgruparPorPeriodoJuntaElHechoSinPeriodoConElMesDeAhora(t *testing.T) {
	t.Parallel()
	julio := Mes{Anio: 2026, Mes: time.July}
	ahora := time.Date(2026, time.August, 15, 12, 0, 0, 0, Zona)
	hechos := []Hecho{
		{Tipo: CaidaIngreso, Clave: "ingreso-mensual", Mes: &julio},
		{Tipo: PagoNoAlcanza, Clave: "saldo-corto"},
	}

	grupos := AgruparPorPeriodo(hechos, ahora)
	if len(grupos) != 2 {
		t.Fatalf("grupos = %d, quiero 2: julio y agosto", len(grupos))
	}
	if !grupos[0].Inicio.Equal(julio.Fecha()) || grupos[0].Hechos[0].Clave != "ingreso-mensual" {
		t.Fatalf("primer grupo = %s con %+v, quiero julio con el hecho que trae período",
			grupos[0].Inicio.Format("2006-01"), grupos[0].Hechos)
	}
	agosto := time.Date(2026, time.August, 1, 0, 0, 0, 0, Zona)
	if !grupos[1].Inicio.Equal(agosto) || len(grupos[1].Hechos) != 1 ||
		grupos[1].Hechos[0].Clave != "saldo-corto" {
		t.Fatalf("segundo grupo = %s con %+v, quiero agosto con el hecho sin período",
			grupos[1].Inicio.Format("2006-01"), grupos[1].Hechos)
	}
}
