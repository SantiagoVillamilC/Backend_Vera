//go:build integration

package inteligencia

import (
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// La emisión de un solo usuario es la que dispara la confirmación de una liquidación. Estas pruebas
// usan septiembre de 2026 con un usuario nuevo, igual que las del presupuesto, para no medir nada
// de otras corridas de la suite.

var ahoraDelAvisoEnVivo = time.Date(2026, time.September, 1, 15, 0, 0, 0, time.UTC)

const pagoRecurrenteEnVivo = int64(5_000_000)

// sembrarSerieMensualDePago deja tres meses completos con el mismo pago y deja el próximo vencimiento
// en el mismo día del reloj inyectado, dentro de la ventana de siete días.
func sembrarSerieMensualDePago(t *testing.T, pool *pgxpool.Pool, usuario string) {
	t.Helper()
	for _, mes := range []time.Month{time.June, time.July, time.August} {
		sembrarCopia(t, pool, filaDeCopia{
			usuario: usuario, ocurrido: time.Date(2026, mes, 1, 15, 0, 0, 0, time.UTC),
			monto: pagoRecurrenteEnVivo, direccion: "salida", categoria: "alimentos_mercado",
			contraparte: "ARRENDADOR", llave: "L-ARRENDADOR", nombre: "ARRENDADOR",
			transaccion: uuid.New(),
		})
	}
}

// sembrarSaldoBajo deja en el ledger el disponible del usuario con el saldo pedido. En un pasivo el
// disponible acumula negativo, así que la línea lleva el signo cambiado.
func sembrarSaldoBajo(t *testing.T, pool *pgxpool.Pool, usuario string, saldoCentavos int64) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción del saldo: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}
	var entryID int64
	if err := tx.QueryRow(ctx,
		"INSERT INTO nucleo.journal_entry (tipo, ocurrido_en) VALUES ('deposito', $1) RETURNING id",
		ahoraDelAvisoEnVivo.AddDate(0, -6, 0)).Scan(&entryID); err != nil {
		t.Fatalf("insertar el asiento del saldo: %v", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO nucleo.posting (entry_id, cuenta_id, monto, moneda) VALUES ($1, $2, $3, 'COP'), ($1, $4, $5, 'COP')",
		entryID, "pasivo:usuario:"+usuario+":disponible", -saldoCentavos,
		"activo:custodia:banco", saldoCentavos); err != nil {
		t.Fatalf("insertar las líneas del saldo: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("confirmar el saldo: %v", err)
	}
}

func TestElAvisoDePagoLlegaSinEsperarElLote(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NuevoServicio(pool)
	usuario := usuarioDeAvisos(t, pool)
	sembrarSerieMensualDePago(t, pool, usuario)
	sembrarSaldoBajo(t, pool, usuario, 1_000_000)

	insertados, err := servicio.EmitirAvisosDelUsuario(t.Context(), usuario, ahoraDelAvisoEnVivo)
	if err != nil {
		t.Fatalf("emitir los avisos de %q: %v", usuario, err)
	}
	if insertados == 0 {
		t.Fatalf("no se guardó ningún aviso para %q", usuario)
	}

	tarjetas, err := servicio.AvisosDelUsuario(t.Context(), usuario, "2026-09", ahoraDelAvisoEnVivo)
	if err != nil {
		t.Fatalf("listar los avisos de %q: %v", usuario, err)
	}
	pago := AvisoEmitido{}
	pagos := 0
	for _, tarjeta := range tarjetas {
		t.Logf("aviso %s monto=%s texto=%q", tarjeta.Tipo, avisos.Pesos(tarjeta.MontoCentavos), tarjeta.Texto)
		if tarjeta.Tipo == avisos.PagoNoAlcanza {
			pagos++
			pago = tarjeta
		}
	}
	if pagos != 1 {
		t.Fatalf("avisos de pago listados = %d, quiero 1", pagos)
	}
	if pago.MontoCentavos != 1_000_000 {
		t.Errorf("monto del aviso = %d centavos, quiero 1000000", pago.MontoCentavos)
	}
	if pago.ReferenciaCentavos == nil || *pago.ReferenciaCentavos != pagoRecurrenteEnVivo {
		t.Errorf("referencia del aviso = %v, quiero %d centavos", pago.ReferenciaCentavos, pagoRecurrenteEnVivo)
	}
}
