//go:build integration

package inteligencia

import (
	"context"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type movimientoConVersion struct {
	Categoria   string
	Origen      string
	Version     string
	ComercioMid string
	ComercioNom string
	MCC         string
}

func leerMovimientoConVersion(t *testing.T, pool *pgxpool.Pool, usuario string, transaccion uuid.UUID) movimientoConVersion {
	t.Helper()
	var leido movimientoConVersion
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `
SELECT categoria, origen_categoria, version_modelo,
       COALESCE(comercio_mid, ''), COALESCE(comercio_nombre, ''), COALESCE(mcc, '')
  FROM inteligencia.movimiento_enriquecido
 WHERE usuario_id = $1 AND transaccion_id = $2`,
				usuario, pgtype.UUID{Bytes: transaccion, Valid: true}).
				Scan(&leido.Categoria, &leido.Origen, &leido.Version,
					&leido.ComercioMid, &leido.ComercioNom, &leido.MCC)
		})
	if err != nil {
		t.Fatalf("leer movimiento de %q en %s: %v", usuario, transaccion, err)
	}
	return leido
}

func modeloDelComercio(t *testing.T, comercio string) *Modelo {
	t.Helper()
	normalizado := NormalizarNombre(comercio)
	return modeloClasificador(t, idfDeTexto(normalizado),
		map[string]map[string]string{"compras": centroideConCoseno(t, normalizado, 0.9)}, nil)
}

func TestEnriquecerConClasificadorEscribeOrigenTFIDF(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	base := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	comercio := "Tienda Sintética"
	ancla := sembrarAsiento(t, pool, "pago_qr", nil, comercio, base, []lineaDePrueba{
		{"pasivo:usuario:" + usuario + ":disponible", -4500},
		{"pasivo:liquidacion_pendiente:riel", 4500},
	})

	modelo := modeloDelComercio(t, comercio)
	servicio := NuevoServicio(pool, ConClasificador(modelo))
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	transaccion := transaccionDeAnclaDePrueba(t, pool, ancla)
	leido := leerMovimientoConVersion(t, pool, usuario, transaccion)
	if leido.Origen != OrigenTFIDF {
		t.Fatalf("origen = %q, want %q", leido.Origen, OrigenTFIDF)
	}
	if leido.Categoria != string(CategoriaCompras) {
		t.Fatalf("categoría = %q, want %q", leido.Categoria, CategoriaCompras)
	}
	if leido.Version != modelo.VersionModelo() {
		t.Fatalf("version_modelo = %q, want %q", leido.Version, modelo.VersionModelo())
	}
	if leido.MCC != "" {
		t.Fatalf("mcc = %q, want NULL", leido.MCC)
	}
}

func TestEnriquecerSinClasificadorNoEscribeTFIDF(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	base := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	comercio := "Tienda Sintética"
	ancla := sembrarAsiento(t, pool, "pago_qr", nil, comercio, base, []lineaDePrueba{
		{"pasivo:usuario:" + usuario + ":disponible", -3200},
		{"pasivo:liquidacion_pendiente:riel", 3200},
	})

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	transaccion := transaccionDeAnclaDePrueba(t, pool, ancla)
	leido := leerMovimientoConVersion(t, pool, usuario, transaccion)
	if leido.Origen != OrigenSinCategoria || leido.Categoria != string(SinCategoria) {
		t.Fatalf("movimiento = %+v, want sin categoría", leido)
	}
	if leido.Version != VersionModelo {
		t.Fatalf("version_modelo = %q, want %q", leido.Version, VersionModelo)
	}
}

func TestEnriquecerConMemoriaYClasificadorGanaLaMemoria(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	base := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	comercio := "Tienda Sintética"
	ancla := sembrarAsiento(t, pool, "pago_qr", nil, comercio, base, []lineaDePrueba{
		{"pasivo:usuario:" + usuario + ":disponible", -7800},
		{"pasivo:liquidacion_pendiente:riel", 7800},
	})
	insertarMemoria(t, pool, usuario, "comercio", "nombre", NormalizarNombre(comercio), CategoriaSalud)

	modelo := modeloDelComercio(t, comercio)
	servicio := NuevoServicio(pool, ConClasificador(modelo))
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	transaccion := transaccionDeAnclaDePrueba(t, pool, ancla)
	leido := leerMovimientoConVersion(t, pool, usuario, transaccion)
	if leido.Origen != OrigenMemoria || leido.Categoria != string(CategoriaSalud) {
		t.Fatalf("movimiento = %+v, want memoria con salud", leido)
	}
	if leido.Version == modelo.VersionModelo() {
		t.Fatalf("una fila de memoria no puede llevar la versión del clasificador: %q", leido.Version)
	}
}

// transaccionDeAnclaDePrueba resuelve el transaccion_id con el que el lote agrupó un asiento.
func transaccionDeAnclaDePrueba(t *testing.T, pool *pgxpool.Pool, ancla int64) uuid.UUID {
	t.Helper()
	var transaccion uuid.UUID
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `
SELECT transaccion_id FROM inteligencia.movimiento_enriquecido WHERE entry_ancla = $1 LIMIT 1`,
				ancla).Scan(&transaccion)
		})
	if err != nil {
		t.Fatalf("leer la transacción del asiento %d: %v", ancla, err)
	}
	return transaccion
}
