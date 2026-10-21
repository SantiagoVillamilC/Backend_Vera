//go:build integration

package inteligencia

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestFlujoLexicoCorreccionYMemoria recorre la cadena completa de una sola vez: el lote decide con el
// léxico embebido, la corrección del usuario reescribe la fila y deja memoria, y una reconstrucción
// posterior aplica esa memoria a los pagos nuevos del mismo comercio. Es la integración que ninguna
// prueba por pieza cubre.
func TestFlujoLexicoCorreccionYMemoria(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	comercio := "Droguería San Jorge"
	primero := sembrarPagoExterno(t, pool, usuario, comercio, 5000, base)

	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("cargar el léxico embebido: %v", err)
	}
	servicio := NuevoServicio(pool, ConLexico(lexico))
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir con léxico: %v", err)
	}

	fila := leerFilaDeLaCopia(t, pool, usuario, transaccionDelPago(primero))
	if fila.Origen != OrigenLexico || fila.Categoria != string(CategoriaSalud) {
		t.Fatalf("primera fila = %+v, want léxico con salud", fila)
	}

	compras := CategoriaCompras
	correccion, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(primero),
		SolicitudDeCorreccion{Categoria: &compras})
	if err != nil {
		t.Fatalf("CorregirMovimiento: %v", err)
	}
	if correccion.FilasCambiadas != 1 || correccion.Movimiento.Categoria != string(compras) {
		t.Fatalf("corrección = %+v, want una fila en compras", correccion)
	}

	segundo := sembrarPagoExterno(t, pool, usuario, comercio, 7000, base.AddDate(0, 1, 0))
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("segunda reconstrucción: %v", err)
	}
	for _, id := range []int64{primero, segundo} {
		fila := leerFilaDeLaCopia(t, pool, usuario, transaccionDelPago(id))
		if fila.Origen != OrigenMemoria || fila.Categoria != string(compras) {
			t.Errorf("fila %d = %+v, want memoria con compras", id, fila)
		}
	}
}
