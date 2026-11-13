//go:build integration

package inteligencia

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// El presupuesto anticipado es el único aviso que compara una proyección contra el histórico. Estas
// pruebas lo siguen de punta a punta con un usuario nuevo y un reloj inyectado en septiembre, un mes
// que ninguna otra corrida de la suite usa, así que lo que se mide es solo de este escenario.

var ahoraDelPresupuesto = time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC)

// Los tres meses completos del escenario. El primer movimiento se siembra el 1 de junio, así que el
// contexto cuenta junio, julio y agosto como completos y septiembre como el mes en curso.
var mesesCompletosDelPresupuesto = []time.Month{time.June, time.July, time.August}

const (
	montoHabitualDelPresupuesto = int64(90_000_000)
	proyeccionDelPresupuesto    = int64(150_000_000)
)

// sembrarHistorialDeTresMeses deja tres meses completos con el mismo gasto y un septiembre por encima
// de esa mediana. Cada mes usa una llave distinta para que ningún comercio se vuelva una serie
// recurrente y el único hecho de septiembre sea el gasto.
func sembrarHistorialDeTresMeses(t *testing.T, pool *pgxpool.Pool, usuario string) {
	t.Helper()
	for _, mes := range mesesCompletosDelPresupuesto {
		sembrarCopia(t, pool, filaDeCopia{
			usuario: usuario, ocurrido: time.Date(2026, mes, 1, 15, 0, 0, 0, time.UTC),
			monto: montoHabitualDelPresupuesto, direccion: "salida", categoria: "alimentos_mercado",
			contraparte: "MERCADO", llave: "L-MERCADO-" + mes.String(),
			nombre: "MERCADO DE " + mes.String(), transaccion: uuid.New(),
		})
	}
	sembrarCopia(t, pool, filaDeCopia{
		usuario: usuario, ocurrido: time.Date(2026, time.September, 4, 15, 0, 0, 0, time.UTC),
		monto: proyeccionDelPresupuesto, direccion: "salida", categoria: "alimentos_mercado",
		contraparte: "MERCADO", llave: "L-MERCADO-SEPTIEMBRE",
		nombre: "MERCADO DE SEPTIEMBRE", transaccion: uuid.New(),
	})
}

// montosDelTextoDevuelve los montos impresos en el texto en el orden en que aparecen. Se contrastan
// contra los campos estructurados del aviso, sin volver a calcular la proyección.
var montoEnElTexto = regexp.MustCompile(`\$[0-9]([0-9.,]*[0-9])?`)

func montosDelTexto(texto string) []string {
	return montoEnElTexto.FindAllString(texto, -1)
}

func referenciaDelAviso(aviso AvisoEmitido) string {
	if aviso.ReferenciaCentavos == nil {
		return "sin referencia"
	}
	return avisos.Pesos(*aviso.ReferenciaCentavos)
}

// cupoOcupadoDelMes lee la ocupación del cupo por los dos caminos que informan la cifra: el resumen
// que ve la app y el conteo que usa el emisor. Los dos tienen que coincidir.
func cupoOcupadoDelMes(t *testing.T, servicio *Servicio, pool *pgxpool.Pool, usuario string,
	ahora time.Time) int {
	t.Helper()
	resumen, err := servicio.Resumen(t.Context(), usuario, "2026-09", ahora)
	if err != nil {
		t.Fatalf("resumen de septiembre de %q: %v", usuario, err)
	}
	emitidos := avisosContadosPorElEmisor(t, pool, usuario, ahora)
	if resumen.AvisosEmitidos != emitidos {
		t.Fatalf("el resumen cuenta %d avisos y el emisor %d",
			resumen.AvisosEmitidos, emitidos)
	}
	return emitidos
}

func TestElPresupuestoAnticipadoDiceSusCifrasYLiberaCupoAlDescartar(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuario := usuarioDeAvisos(t, pool)
	sembrarHistorialDeTresMeses(t, pool, usuario)

	insertados, recuento, err := servicio.emitirAvisosDeUsuario(t.Context(), usuario, ahoraDelPresupuesto)
	if err != nil {
		t.Fatalf("emitir los avisos de %q: %v", usuario, err)
	}
	if insertados != 2 {
		t.Fatalf("insertados = %d, want 2: el presupuesto y la categoría de septiembre", insertados)
	}
	if recuento.Candidatas != 2 || recuento.Elegidas != 2 || recuento.Recortadas != 0 {
		t.Fatalf("recuento = %+v, want 2 candidatas, 2 elegidas y 0 recortadas", recuento)
	}

	listado, err := servicio.AvisosDelUsuario(t.Context(), usuario, "2026-09", ahoraDelPresupuesto)
	if err != nil {
		t.Fatalf("listar los avisos de %q: %v", usuario, err)
	}
	var presupuestos []AvisoEmitido
	for _, aviso := range listado {
		t.Logf("aviso %s monto=%s referencia=%s texto=%q",
			aviso.Tipo, avisos.Pesos(aviso.MontoCentavos), referenciaDelAviso(aviso), aviso.Texto)
		if aviso.Tipo == avisos.PresupuestoAnticipado {
			presupuestos = append(presupuestos, aviso)
		}
	}
	if len(presupuestos) != 1 {
		t.Fatalf("presupuestos listados = %d, want 1: la bandeja deduplica por tipo, clave y periodo",
			len(presupuestos))
	}
	if len(listado) != 2 {
		t.Fatalf("avisos listados = %d, want 2: presupuesto y categoría", len(listado))
	}
	presupuesto := presupuestos[0]

	if presupuesto.Periodo.Format("2006-01-02") != "2026-09-01" {
		t.Fatalf("periodo = %s, want 2026-09-01", presupuesto.Periodo.Format("2006-01-02"))
	}
	if presupuesto.MontoCentavos != proyeccionDelPresupuesto {
		t.Fatalf("monto = %d, want %d: es el gasto acumulado de septiembre",
			presupuesto.MontoCentavos, proyeccionDelPresupuesto)
	}
	if presupuesto.ReferenciaCentavos == nil {
		t.Fatal("el presupuesto anticipado salió sin referencia, así que el texto no podría afirmarla")
	}
	if *presupuesto.ReferenciaCentavos != montoHabitualDelPresupuesto {
		t.Fatalf("referencia = %d, want %d: la mediana de junio, julio y agosto",
			*presupuesto.ReferenciaCentavos, montoHabitualDelPresupuesto)
	}
	if presupuesto.SeveridadCentavos != proyeccionDelPresupuesto-montoHabitualDelPresupuesto {
		t.Fatalf("severidad = %d, want %d", presupuesto.SeveridadCentavos,
			proyeccionDelPresupuesto-montoHabitualDelPresupuesto)
	}

	esperado := fmt.Sprintf(
		"En septiembre de 2026 llevas %s en gastos. Lo habitual en tus meses completos es %s.",
		avisos.Pesos(presupuesto.MontoCentavos), avisos.Pesos(*presupuesto.ReferenciaCentavos))
	if presupuesto.Texto != esperado {
		t.Fatalf("texto = %q, want %q", presupuesto.Texto, esperado)
	}
	montos := montosDelTexto(presupuesto.Texto)
	if len(montos) != 2 {
		t.Fatalf("montos del texto = %v, want 2: la proyección y la referencia", montos)
	}
	if montos[0] != avisos.Pesos(presupuesto.MontoCentavos) ||
		montos[1] != avisos.Pesos(*presupuesto.ReferenciaCentavos) {
		t.Fatalf("el texto dice %v y las cifras estructuradas son %s y %s: no coinciden",
			montos, avisos.Pesos(presupuesto.MontoCentavos),
			avisos.Pesos(*presupuesto.ReferenciaCentavos))
	}
	if !strings.Contains(presupuesto.Texto, "septiembre de 2026") {
		t.Fatalf("el texto no nombra el mes del hecho: %q", presupuesto.Texto)
	}

	cupoAntes := cupoOcupadoDelMes(t, servicio, pool, usuario, ahoraDelPresupuesto)
	if cupoAntes != len(listado) {
		t.Fatalf("cupo ocupado = %d y bandeja = %d, want que coincidan",
			cupoAntes, len(listado))
	}

	if _, err := servicio.DescartarAviso(t.Context(), usuario, presupuesto.ID,
		ahoraDelPresupuesto); err != nil {
		t.Fatalf("descartar: %v", err)
	}

	despues, err := servicio.AvisosDelUsuario(t.Context(), usuario, "2026-09", ahoraDelPresupuesto)
	if err != nil {
		t.Fatalf("listar después de descartar: %v", err)
	}
	if len(despues) != len(listado)-1 {
		t.Fatalf("bandeja después del descarte = %d, want %d", len(despues), len(listado)-1)
	}
	for _, aviso := range despues {
		if aviso.ID == presupuesto.ID || aviso.Tipo == avisos.PresupuestoAnticipado {
			t.Fatalf("el presupuesto descartado sigue en la bandeja: %+v", aviso)
		}
	}

	cupoDespues := cupoOcupadoDelMes(t, servicio, pool, usuario, ahoraDelPresupuesto)
	if cupoDespues != cupoAntes-1 {
		t.Fatalf("cupo ocupado después del descarte = %d, want %d: el descarte no liberó cupo",
			cupoDespues, cupoAntes-1)
	}
	t.Logf("cupo del mes: %d avisos antes del descarte y %d después", cupoAntes, cupoDespues)
}
