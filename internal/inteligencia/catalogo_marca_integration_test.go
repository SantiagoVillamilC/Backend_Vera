//go:build integration

package inteligencia

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sufijoDePrueba devuelve ocho caracteres únicos que la normalización deja igual.
func sufijoDePrueba() string {
	return strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:8]
}

// nombreDePrueba arma un nombre propio de la prueba: corto para no pasar el tope de la clave del
// evaluador y con el sufijo único, para no pisar las filas del catálogo curado.
func nombreDePrueba(sufijo, etiqueta string) string {
	return "PRUEBA-" + sufijo + "-" + etiqueta
}

// sembrarCatalogoCurado inserta filas con el origen de la prueba y las retira al terminar. El origen
// y los nombres son propios, así que la limpieza no toca las filas curadas de A1-02 ni las de otra
// prueba.
func sembrarCatalogoCurado(t *testing.T, origen string, filas []repositorio.UpsertCatalogoMarcaParams) {
	t.Helper()
	ctx := t.Context()
	pool := abrirPoolDePrueba(t)
	_, err := enTransaccion(ctx, poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			for _, fila := range filas {
				if _, err := q.UpsertCatalogoMarca(ctx, fila); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatalf("sembrar el catálogo: %v", err)
	}
	t.Cleanup(func() { retirarCatalogoDePrueba(t, pool, origen) })
}

// retirarCatalogoDePrueba borra las filas del origen de la prueba, con el rol que puede escribir.
func retirarCatalogoDePrueba(t *testing.T, pool *pgxpool.Pool, origen string) {
	t.Helper()
	_, err := enTransaccion(context.Background(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "DELETE FROM inteligencia.catalogo_marca WHERE origen = $1", origen)
			return struct{}{}, err
		})
	if err != nil {
		t.Errorf("limpiar el catálogo de prueba: %v", err)
	}
}

// contarCatalogoDelOrigen cuenta las filas de un origen con el rol que puede leer el catálogo.
func contarCatalogoDelOrigen(t *testing.T, pool *pgxpool.Pool, origen string) int {
	t.Helper()
	total, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (int, error) {
			var cuenta int
			err := tx.QueryRow(ctx,
				"SELECT count(*) FROM inteligencia.catalogo_marca WHERE origen = $1", origen).Scan(&cuenta)
			return cuenta, err
		})
	if err != nil {
		t.Fatalf("contar el origen %q: %v", origen, err)
	}
	return total
}

func TestBuscarCatalogoPorNombre(t *testing.T) {
	sufijo := sufijoDePrueba()
	origen := "prueba-nombre-" + sufijo
	nombre := nombreDePrueba(sufijo, "FARMACIA")
	sembrarCatalogoCurado(t, origen, []repositorio.UpsertCatalogoMarcaParams{{
		NombreNormalizado: nombre,
		Categoria:         string(CategoriaSalud),
		Origen:            origen,
		Version:           "prueba",
	}})
	pool := abrirPoolDePrueba(t)

	fila, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (repositorio.CatalogoMarca, error) {
			return q.BuscarCatalogoPorNombre(ctx, nombre)
		})
	if err != nil {
		t.Fatalf("BuscarCatalogoPorNombre: %v", err)
	}
	if fila.Categoria != string(CategoriaSalud) || fila.Origen != origen || fila.Version != "prueba" {
		t.Fatalf("fila = %+v, want salud de %q versión prueba", fila, origen)
	}
	if fila.Llave.Valid {
		t.Errorf("llave = %q, want NULL", fila.Llave.String)
	}
}

func TestBuscarCatalogoPorLlave(t *testing.T) {
	sufijo := sufijoDePrueba()
	origen := "prueba-llave-" + sufijo
	nombre := nombreDePrueba(sufijo, "ALMACEN")
	llave := origen + "@bre-b"
	sembrarCatalogoCurado(t, origen, []repositorio.UpsertCatalogoMarcaParams{{
		NombreNormalizado: nombre,
		Categoria:         string(CategoriaCompras),
		Llave:             pgtype.Text{String: llave, Valid: true},
		Origen:            origen,
		Version:           "prueba",
	}})
	pool := abrirPoolDePrueba(t)

	fila, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (repositorio.CatalogoMarca, error) {
			return q.BuscarCatalogoPorLlave(ctx, llave)
		})
	if err != nil {
		t.Fatalf("BuscarCatalogoPorLlave: %v", err)
	}
	if fila.NombreNormalizado != nombre || fila.Categoria != string(CategoriaCompras) {
		t.Fatalf("fila = %+v, want %q en compras", fila, nombre)
	}
}

// TestElServicioCargaElCatalogoYDecideConEl cubre una marca con varias sedes: cada fila entra al mapa
// y un nombre en minúsculas decide por el escalón 4.
func TestElServicioCargaElCatalogoYDecideConEl(t *testing.T) {
	sufijo := sufijoDePrueba()
	origen := "prueba-servicio-" + sufijo
	sedes := []string{
		nombreDePrueba(sufijo, "SEDE-1"),
		nombreDePrueba(sufijo, "SEDE-2"),
		nombreDePrueba(sufijo, "SEDE-3"),
	}
	sembradas := make([]repositorio.UpsertCatalogoMarcaParams, 0, len(sedes))
	for _, sede := range sedes {
		sembradas = append(sembradas, repositorio.UpsertCatalogoMarcaParams{
			NombreNormalizado: sede,
			Categoria:         string(CategoriaAlimentosMercado),
			Origen:            origen,
			Version:           "prueba",
		})
	}
	sembrarCatalogoCurado(t, origen, sembradas)
	nombres := make(map[string]Categoria)
	llaves := make(map[string]Categoria)
	servicio := NuevoServicio(abrirPoolDePrueba(t), ConCatalogo(nombres, llaves))
	if err := servicio.CargarCatalogo(t.Context()); err != nil {
		t.Fatalf("CargarCatalogo: %v", err)
	}
	for _, sede := range sedes {
		if nombres[sede] != CategoriaAlimentosMercado {
			t.Fatalf("catálogo de nombres = %v, want %q en %q", nombres[sede], sede,
				CategoriaAlimentosMercado)
		}
	}

	escalones := escalonesDelLote(servicio.catalogo(), nil, nil)
	decision := Decidir(Entrada{
		Canal:       CanalQR,
		Nombre:      strings.ToLower(sedes[0]),
		TipoDestino: DestinoComercio,
	}, Recursos{CatalogoNombres: nombres}, escalones)
	if decision.Origen != OrigenCatalogo || decision.Categoria != CategoriaAlimentosMercado {
		t.Fatalf("decisión = %+v, want origen %q y categoría %q", decision, OrigenCatalogo,
			CategoriaAlimentosMercado)
	}
}

// TestUnNombreQueNoEstaEnLaTablaCaeAlEscalonSiguiente comprueba el caso que la tarjeta exige: con el
// catálogo cargado, un nombre que no está no sale con origen catalogo.
func TestUnNombreQueNoEstaEnLaTablaCaeAlEscalonSiguiente(t *testing.T) {
	sufijo := sufijoDePrueba()
	origen := "prueba-ausente-" + sufijo
	nombre := nombreDePrueba(sufijo, "SEDE")
	sembrarCatalogoCurado(t, origen, []repositorio.UpsertCatalogoMarcaParams{{
		NombreNormalizado: nombre,
		Categoria:         string(CategoriaAlimentosMercado),
		Origen:            origen,
		Version:           "prueba",
	}})
	nombres := make(map[string]Categoria)
	llaves := make(map[string]Categoria)
	servicio := NuevoServicio(abrirPoolDePrueba(t), ConCatalogo(nombres, llaves))
	if err := servicio.CargarCatalogo(t.Context()); err != nil {
		t.Fatalf("CargarCatalogo: %v", err)
	}
	if nombres[nombre] != CategoriaAlimentosMercado {
		t.Fatalf("catálogo de nombres = %v, want la sede %q cargada", nombres, nombre)
	}

	escalones := escalonesDelLote(servicio.catalogo(), nil, nil)
	decision := Decidir(Entrada{
		Canal:       CanalQR,
		Nombre:      "Ferretería El Tornillo",
		TipoDestino: DestinoComercio,
	}, Recursos{CatalogoNombres: nombres}, escalones)
	if decision.Origen == OrigenCatalogo {
		t.Fatalf("decisión = %+v, want que un nombre ausente no salga con origen %q", decision,
			OrigenCatalogo)
	}
	if decision.Origen != OrigenSinCategoria {
		t.Fatalf("decisión = %+v, want origen %q", decision, OrigenSinCategoria)
	}
}

// TestCargarCatalogoNoConservaClavesDeLaCargaAnterior comprueba la garantía del comentario: la carga
// vacía los mapas antes de llenarlos. La prueba usa filas, un origen y un mapa propios, porque la
// tabla tiene el catálogo curado completo y los mapas no quedan vacíos en esta base.
func TestCargarCatalogoNoConservaClavesDeLaCargaAnterior(t *testing.T) {
	sufijo := sufijoDePrueba()
	origen := "prueba-vaciado-" + sufijo
	nombre := nombreDePrueba(sufijo, "SEDE")
	llave := origen + "@bre-b"
	sembrarCatalogoCurado(t, origen, []repositorio.UpsertCatalogoMarcaParams{{
		NombreNormalizado: nombre,
		Categoria:         string(CategoriaTransporte),
		Llave:             pgtype.Text{String: llave, Valid: true},
		Origen:            origen,
		Version:           "prueba",
	}})
	pool := abrirPoolDePrueba(t)
	nombres := map[string]Categoria{"CLAVE-FANTASMA": CategoriaCompras}
	llaves := map[string]Categoria{"@fantasma": CategoriaCompras}
	servicio := NuevoServicio(pool, ConCatalogo(nombres, llaves))
	if err := servicio.CargarCatalogo(t.Context()); err != nil {
		t.Fatalf("CargarCatalogo: %v", err)
	}
	if nombres[nombre] != CategoriaTransporte || llaves[llave] != CategoriaTransporte {
		t.Fatalf("catálogo = %+v, want la fila de la prueba cargada", servicio.catalogo())
	}
	if _, quedo := nombres["CLAVE-FANTASMA"]; quedo {
		t.Fatalf("catálogo de nombres = %v, want que la clave vieja se haya borrado", nombres)
	}
	if _, quedo := llaves["@fantasma"]; quedo {
		t.Fatalf("catálogo de llaves = %v, want que la llave vieja se haya borrado", llaves)
	}

	retirarCatalogoDePrueba(t, pool, origen)
	if err := servicio.CargarCatalogo(t.Context()); err != nil {
		t.Fatalf("CargarCatalogo tras retirar la fila: %v", err)
	}
	if _, quedo := nombres[nombre]; quedo {
		t.Fatalf("catálogo de nombres = %v, want que %q ya no esté", nombres, nombre)
	}
	if _, quedo := llaves[llave]; quedo {
		t.Fatalf("catálogo de llaves = %v, want que %q ya no esté", llaves, llave)
	}
}

// TestLaBaseRechazaSinCategoriaEnElCatalogo cubre el disparador de la migración 0031: sin_categoria
// existe en inteligencia.categoria, así que la clave foránea la aceptaría, y la que la rechaza es la
// regla de visibilidad.
func TestLaBaseRechazaSinCategoriaEnElCatalogo(t *testing.T) {
	sufijo := sufijoDePrueba()
	origen := "prueba-trigger-" + sufijo
	pool := abrirPoolDePrueba(t)
	t.Cleanup(func() { retirarCatalogoDePrueba(t, pool, origen) })

	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (struct{}, error) {
			_, err := q.UpsertCatalogoMarca(ctx, repositorio.UpsertCatalogoMarcaParams{
				NombreNormalizado: nombreDePrueba(sufijo, "SIN-CATEGORIA"),
				Categoria:         string(SinCategoria),
				Origen:            origen,
				Version:           "prueba",
			})
			return struct{}{}, err
		})
	if err == nil {
		t.Fatalf("la base aceptó %q en el catálogo curado", SinCategoria)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.ForeignKeyViolation {
		t.Fatalf("error = %v, want la violación que levanta el disparador", err)
	}
	if total := contarCatalogoDelOrigen(t, pool, origen); total != 0 {
		t.Fatalf("filas del origen tras el rechazo = %d, want 0", total)
	}
}
