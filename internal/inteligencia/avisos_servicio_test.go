//go:build integration

package inteligencia

import (
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/google/uuid"
)

// Los montos del aviso se leen de la fila que guardó el emisor, no de una fila armada a mano: lo que
// se mide es que guardarAviso persiste la proyección y que la bandeja la devuelve igual.

func TestGuardarAvisoPersisteLosMontosDeLaProyeccion(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)

	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("corrida de calentamiento: %v", err)
	}

	usuario := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, usuario, 5, 90_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	sembrarCopia(t, pool, filaDeCopia{usuario: usuario,
		ocurrido: time.Date(2026, 7, 3, 15, 0, 0, 0, time.UTC), monto: 150_000_000,
		direccion: "salida", categoria: "alimentos_mercado", contraparte: "MERCADO",
		llave: "L-MERCADO", nombre: "MERCADO", transaccion: uuid.New()})

	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("EmitirAvisos: %v", err)
	}

	presupuesto, existe := tiposDeAvisosDeMes(t, servicio, usuario, "2026-07")[avisos.PresupuestoAnticipado]
	if !existe {
		t.Fatal("falta el aviso de presupuesto anticipado")
	}
	if presupuesto.MontoCentavos != 150_000_000 {
		t.Fatalf("monto = %d, want 150000000", presupuesto.MontoCentavos)
	}
	if presupuesto.ReferenciaCentavos == nil || *presupuesto.ReferenciaCentavos != 90_000_000 {
		t.Fatalf("referencia = %v, want 90000000", presupuesto.ReferenciaCentavos)
	}
	if presupuesto.SeveridadCentavos != 60_000_000 {
		t.Fatalf("severidad = %d, want 60000000", presupuesto.SeveridadCentavos)
	}
	if presupuesto.Texto !=
		"En julio de 2026 llevas $1.500.000 en gastos. Lo habitual en tus meses completos es $900.000." {
		t.Fatalf("el texto no coincide con las cifras guardadas: %q", presupuesto.Texto)
	}

	categoria, existe := tiposDeAvisosDeMes(t, servicio, usuario, "2026-07")[avisos.CategoriaSobreMedia]
	if !existe {
		t.Fatal("falta el aviso de categoría sobre la media")
	}
	if categoria.MontoCentavos != 150_000_000 ||
		categoria.ReferenciaCentavos == nil || *categoria.ReferenciaCentavos != 90_000_000 {
		t.Fatalf("montos de la categoría = %+v", categoria)
	}

	paginados, err := servicio.AvisosDelUsuarioPaginados(t.Context(), usuario,
		time.Date(2026, time.July, 1, 0, 0, 0, 0, avisos.Zona), EstadoVigentes, nil, 50)
	if err != nil {
		t.Fatalf("listar paginados: %v", err)
	}
	vistoEnLaPagina := false
	for _, aviso := range paginados.Avisos {
		if aviso.ID == presupuesto.ID {
			vistoEnLaPagina = true
			if aviso.MontoCentavos != presupuesto.MontoCentavos ||
				aviso.ReferenciaCentavos == nil ||
				*aviso.ReferenciaCentavos != *presupuesto.ReferenciaCentavos {
				t.Fatalf("la página no coincide con la bandeja: %+v contra %+v", aviso, presupuesto)
			}
		}
	}
	if !vistoEnLaPagina {
		t.Fatalf("el aviso %s no aparece en la página de %s", presupuesto.ID, presupuesto.Periodo)
	}

	detalle, err := servicio.Aviso(t.Context(), usuario, presupuesto.ID)
	if err != nil {
		t.Fatalf("leer el aviso: %v", err)
	}
	if detalle.MontoCentavos != presupuesto.MontoCentavos ||
		detalle.ReferenciaCentavos == nil ||
		*detalle.ReferenciaCentavos != *presupuesto.ReferenciaCentavos {
		t.Fatalf("el detalle no coincide: %+v contra %+v", detalle, presupuesto)
	}
}

func TestAvisoSinLineaBaseSeListaSinReferencia(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)

	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("corrida de calentamiento: %v", err)
	}

	usuario := usuarioDeAvisos(t, pool)
	gastoMensual(t, pool, usuario, 6, 80_000_000, "alimentos_mercado", "MERCADO", "L-MERCADO")
	for _, momento := range []time.Time{
		time.Date(2026, 6, 20, 15, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 10, 15, 0, 0, 0, time.UTC),
	} {
		monto := int64(15_000_000)
		if momento.Month() == time.July {
			monto = 15_200_000
		}
		sembrarCopia(t, pool, filaDeCopia{usuario: usuario, ocurrido: momento, monto: monto,
			direccion: "salida", categoria: "entretenimiento", contraparte: "NUEVA",
			llave: "L-NUEVA", nombre: "NUEVA", transaccion: uuid.New()})
	}

	if _, err := servicio.EmitirAvisos(t.Context(), ahoraDeAvisos); err != nil {
		t.Fatalf("EmitirAvisos: %v", err)
	}

	cobro, existe := tiposDeAvisosDeMes(t, servicio, usuario, "2026-06")[avisos.CobroNuevo]
	if !existe {
		t.Fatal("falta el aviso de cobro nuevo")
	}
	if cobro.MontoCentavos != 15_100_000 || cobro.ReferenciaCentavos != nil {
		t.Fatalf("montos del cobro nuevo = %+v", cobro)
	}

	detalle, err := servicio.Aviso(t.Context(), usuario, cobro.ID)
	if err != nil {
		t.Fatalf("leer el aviso: %v", err)
	}
	if detalle.MontoCentavos != cobro.MontoCentavos || detalle.ReferenciaCentavos != nil {
		t.Fatalf("el detalle no coincide: %+v contra %+v", detalle, cobro)
	}
}
