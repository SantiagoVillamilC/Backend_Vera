//go:build integration

package inteligencia

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// leerPromocion devuelve la categoría promovida de un destino y si existe.
func leerPromocion(t *testing.T, pool *pgxpool.Pool, tipo, nivel, valor string) (Categoria, bool) {
	t.Helper()
	var categoria string
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `
SELECT categoria FROM inteligencia.promocion_destino
 WHERE tipo_destino = $1 AND nivel = $2 AND valor = $3`, tipo, nivel, valor).Scan(&categoria)
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("leer la promoción %s/%s/%s: %v", tipo, nivel, valor, err)
	}
	return Categoria(categoria), true
}

// TestDosUsuariosPromuevenYElTerceroHereda cubre el criterio de R-08: dos usuarios que corrigen el
// mismo destino a la misma categoría cambian lo que recibe un tercero en un movimiento nuevo, y uno
// solo no cambia nada.

// limpiarVotosYPromociones deja las dos tablas de R-08 vacías: la suite comparte la base y un voto
// de una corrida anterior no puede promover un destino en esta.
func limpiarVotosYPromociones(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			if _, err := tx.Exec(ctx, "DELETE FROM inteligencia.promocion_destino"); err != nil {
				return struct{}{}, err
			}
			_, err := tx.Exec(ctx, "DELETE FROM inteligencia.voto_destino")
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("limpiar votos y promociones: %v", err)
	}
}

func TestDosUsuariosPromuevenYElTerceroHereda(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	limpiarVotosYPromociones(t, pool)
	ctx := t.Context()
	lexico, err := CargarLexicoV2()
	if err != nil {
		t.Fatalf("cargar el léxico: %v", err)
	}
	servicio := NuevoServicio(pool, ConLexico(lexico))

	usuarios := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for _, usuario := range usuarios {
		sembrarCuentaDeUsuario(t, pool, usuario)
	}
	base := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	comercio := "Droguería Promoción K"
	pagos := map[string]int64{}
	for _, usuario := range usuarios[:3] {
		pagos[usuario] = sembrarPagoExterno(t, pool, usuario, comercio, 5000, base)
	}
	if _, err := servicio.Enriquecer(ctx, 1000); err != nil {
		t.Fatalf("enriquecer las filas iniciales: %v", err)
	}

	compras := CategoriaCompras
	for _, usuario := range usuarios[:2] {
		if _, err := servicio.CorregirMovimiento(ctx, usuario, transaccionDelPago(pagos[usuario]),
			SolicitudDeCorreccion{Categoria: &compras}); err != nil {
			t.Fatalf("corregir %s: %v", usuario, err)
		}
	}

	clave := NormalizarNombre(comercio)
	categoria, existe := leerPromocion(t, pool, "comercio", "nombre", clave)
	if !existe || categoria != CategoriaCompras {
		t.Fatalf("promoción = %q/%v, quiero comercio/nombre/%q=compras", categoria, existe, clave)
	}

	tercero := usuarios[2]
	nuevo := sembrarPagoExterno(t, pool, tercero, comercio, 7000, base.AddDate(0, 0, 1))
	if _, err := servicio.Enriquecer(ctx, 1000); err != nil {
		t.Fatalf("enriquecer el movimiento nuevo del tercero: %v", err)
	}
	fila := leerFilaDeLaCopia(t, pool, tercero, transaccionDelPago(nuevo))
	if fila.Categoria != string(compras) || fila.Origen != OrigenMemoria {
		t.Fatalf("tercero = %+v, quiero compras por memoria promovida", fila)
	}

	// Un usuario solo corrige otro destino: no hay promoción.
	otro := usuarios[3]
	solo := sembrarPagoExterno(t, pool, otro, "Tienda Promoción Sola", 8000, base)
	if _, err := servicio.Enriquecer(ctx, 1000); err != nil {
		t.Fatalf("enriquecer la tienda sola: %v", err)
	}
	if _, err := servicio.CorregirMovimiento(ctx, otro, transaccionDelPago(solo),
		SolicitudDeCorreccion{Categoria: &compras}); err != nil {
		t.Fatalf("corregir sola: %v", err)
	}
	if _, existe := leerPromocion(t, pool, "comercio", "nombre", NormalizarNombre("Tienda Promoción Sola")); existe {
		t.Fatalf("una sola corrección promovió el destino")
	}
}
