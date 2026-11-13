//go:build integration

package inteligencia

import (
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ahoraLasSeries = time.Date(2026, time.September, 1, 15, 0, 0, 0, time.UTC)

// sembrarSerieDePago deja tres meses completos con el mismo pago y la misma llave.
func sembrarSerieDePago(t *testing.T, pool *pgxpool.Pool, usuario string, monto int64, llave string) {
	t.Helper()
	for _, mes := range []time.Month{time.June, time.July, time.August} {
		sembrarCopia(t, pool, filaDeCopia{
			usuario: usuario, ocurrido: time.Date(2026, mes, 1, 15, 0, 0, 0, time.UTC),
			monto: monto, direccion: "salida", categoria: "alimentos_mercado",
			contraparte: "ARRENDADOR", llave: llave, nombre: "ARRENDADOR",
			transaccion: uuid.New(),
		})
	}
}

// TestSeriesDelUsuarioSeparaDosUsuariosConLaMismaLlave cubre que una llave compartida no mezcla
// historia: cada usuario ve su propia serie con su propio monto esperado, y una serie de otro
// usuario no aparece.
func TestSeriesDelUsuarioSeparaDosUsuariosConLaMismaLlave(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuarioA := usuarioDeAvisos(t, pool)
	usuarioB := usuarioDeAvisos(t, pool)
	const llaveCompartida = "L-SERIE-COMPARTIDA"

	sembrarSerieDePago(t, pool, usuarioA, 5_000_000, llaveCompartida)
	sembrarSerieDePago(t, pool, usuarioB, 9_000_000, llaveCompartida)
	sembrarSerieDePago(t, pool, usuarioB, 7_000_000, "L-SOLO-DE-B")

	seriesA, err := servicio.SeriesDelUsuario(t.Context(), usuarioA, ahoraLasSeries)
	if err != nil {
		t.Fatalf("series de A: %v", err)
	}
	seriesB, err := servicio.SeriesDelUsuario(t.Context(), usuarioB, ahoraLasSeries)
	if err != nil {
		t.Fatalf("series de B: %v", err)
	}

	if len(seriesA) != 1 {
		t.Fatalf("series de A = %d, quiero 1: la serie de B no puede aparecer", len(seriesA))
	}
	if len(seriesB) != 2 {
		t.Fatalf("series de B = %d, quiero 2", len(seriesB))
	}

	casos := []struct {
		usuario string
		series  []SerieConsultable
		monto   int64
	}{
		{usuarioA, seriesA, 5_000_000},
		{usuarioB, seriesB, 9_000_000},
	}
	for _, caso := range casos {
		serie := caso.series[0]
		if serie.IdentidadValor != llaveCompartida {
			t.Errorf("%s: identidad = %q, quiero %q", caso.usuario, serie.IdentidadValor, llaveCompartida)
		}
		if serie.Estado != string(avisos.Madura) || serie.Cadencia != "mensual" {
			t.Errorf("%s: estado/cadencia = %q/%q", caso.usuario, serie.Estado, serie.Cadencia)
		}
		if serie.Categoria != "alimentos_mercado" {
			t.Errorf("%s: categoría = %q", caso.usuario, serie.Categoria)
		}
		if serie.ProximoMonto == nil || *serie.ProximoMonto != caso.monto {
			t.Errorf("%s: próximo monto = %v, quiero %d", caso.usuario, serie.ProximoMonto, caso.monto)
		}
		if serie.ProximoFecha == nil || !serie.ProximoFecha.Equal(time.Date(2026, time.September, 1, 0, 0, 0, 0, avisos.Zona)) {
			t.Errorf("%s: próximo fecha = %v", caso.usuario, serie.ProximoFecha)
		}
	}

	// La segunda serie de B existe y la de A no ve: ya lo cubre len(seriesA)==1, pero se nombra.
	if seriesB[1].IdentidadValor != "L-SOLO-DE-B" {
		t.Errorf("segunda serie de B = %q", seriesB[1].IdentidadValor)
	}
}
