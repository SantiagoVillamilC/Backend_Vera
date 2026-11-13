//go:build integration

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func abrirPoolDePrueba(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		t.Skip("APP_DATABASE_URL no definida: la prueba de integración necesita un Postgres real")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("abrir pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// sufijoDePrueba devuelve ocho caracteres únicos que la normalización deja igual.
func sufijoDePrueba() string {
	return strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", ""))[:8]
}

// filasDePrueba toma filas del catálogo del repositorio, así las categorías son las que se cargan en
// producción, y les pone nombres y origen propios de la prueba: la clave primaria es el nombre y el
// catálogo curado ya ocupa los del archivo, así que reusarlos pisaría filas ajenas. La llave Bre-B
// se descarta por la misma razón, porque también es única en la tabla.
func filasDePrueba(t *testing.T, sufijo string, cuantas int) []inteligencia.FilaCatalogoMarca {
	t.Helper()
	crudo, err := os.ReadFile(filepath.Join("..", "..", "bd", "datos", "catalogo-v3", "catalogo_v3.csv"))
	if err != nil {
		t.Fatalf("leer el catálogo del repositorio: %v", err)
	}
	filas, err := leerCatalogo(crudo)
	if err != nil {
		t.Fatalf("leerCatalogo: %v", err)
	}
	if len(filas) < cuantas {
		t.Fatalf("el catálogo trae %d filas, want al menos %d", len(filas), cuantas)
	}
	filas = filas[:cuantas]
	for i := range filas {
		filas[i].Nombre = fmt.Sprintf("PRUEBA-%s-%02d", sufijo, i)
		filas[i].Llave = ""
	}
	return filas
}

// limpiarCatalogo retira las filas del origen de la prueba, con el rol que puede escribir.
func limpiarCatalogo(t *testing.T, pool *pgxpool.Pool, origen string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción de limpieza: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+inteligencia.RolInteligencia); err != nil {
		t.Fatalf("bajar a %s: %v", inteligencia.RolInteligencia, err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path = inteligencia"); err != nil {
		t.Fatalf("fijar search_path: %v", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM inteligencia.catalogo_marca WHERE origen = $1", origen); err != nil {
		t.Fatalf("borrar el catálogo de prueba: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("confirmar la limpieza: %v", err)
	}
}

func contarDelOrigen(t *testing.T, pool *pgxpool.Pool, origen string) int {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción de conteo: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+inteligencia.RolInteligencia); err != nil {
		t.Fatalf("bajar a %s: %v", inteligencia.RolInteligencia, err)
	}
	var total int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM inteligencia.catalogo_marca WHERE origen = $1",
		origen).Scan(&total); err != nil {
		t.Fatalf("contar el catálogo: %v", err)
	}
	return total
}

// versionDeLaFila devuelve la versión con la que quedó la fila, o vacío si ya no está.
func versionDeLaFila(t *testing.T, pool *pgxpool.Pool, nombre string) string {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción de lectura: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+inteligencia.RolInteligencia); err != nil {
		t.Fatalf("bajar a %s: %v", inteligencia.RolInteligencia, err)
	}
	var version string
	err = tx.QueryRow(ctx,
		"SELECT version FROM inteligencia.catalogo_marca WHERE nombre_normalizado = $1",
		nombre).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("leer la versión de %q: %v", nombre, err)
	}
	return version
}

func TestCargarDosVecesNoDuplica(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	sufijo := sufijoDePrueba()
	origen := "prueba-carga-" + sufijo
	version := "v3"
	limpiarCatalogo(t, pool, origen)
	t.Cleanup(func() { limpiarCatalogo(t, pool, origen) })
	filas := filasDePrueba(t, sufijo, 20)

	primera, err := cargar(t.Context(), pool, filas, origen, version)
	if err != nil {
		t.Fatalf("primera carga: %v", err)
	}
	if primera.Leidas != len(filas) || primera.Insertadas != int64(len(filas)) ||
		primera.Actualizadas != 0 || primera.Borradas != 0 {
		t.Fatalf("primera carga = %+v, want %d leídas y %d insertadas", primera, len(filas), len(filas))
	}
	if total := contarDelOrigen(t, pool, origen); total != len(filas) {
		t.Fatalf("filas tras la primera carga = %d, want %d", total, len(filas))
	}

	segunda, err := cargar(t.Context(), pool, filas, origen, version)
	if err != nil {
		t.Fatalf("segunda carga: %v", err)
	}
	if segunda.Leidas != len(filas) || segunda.Insertadas != 0 ||
		segunda.Actualizadas != int64(len(filas)) || segunda.Borradas != 0 {
		t.Fatalf("segunda carga = %+v, want %d actualizadas y ninguna insertada", segunda, len(filas))
	}
	if total := contarDelOrigen(t, pool, origen); total != len(filas) {
		t.Fatalf("filas tras la segunda carga = %d, want %d", total, len(filas))
	}
}

// TestCargarBorraLoQueYaNoEstaEnElArchivo retira una fila del archivo y sube la versión a la vez: el
// borrado va por origen, así que la fila retirada sale aunque conserve la versión con la que entró.
func TestCargarBorraLoQueYaNoEstaEnElArchivo(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	sufijo := sufijoDePrueba()
	origen := "prueba-borrado-" + sufijo
	limpiarCatalogo(t, pool, origen)
	t.Cleanup(func() { limpiarCatalogo(t, pool, origen) })
	filas := filasDePrueba(t, sufijo, 20)

	if _, err := cargar(t.Context(), pool, filas, origen, "v3"); err != nil {
		t.Fatalf("carga inicial: %v", err)
	}
	conteos, err := cargar(t.Context(), pool, filas[:19], origen, "v4")
	if err != nil {
		t.Fatalf("carga reducida: %v", err)
	}
	if conteos.Borradas != 1 {
		t.Fatalf("borradas = %d, want 1", conteos.Borradas)
	}
	if total := contarDelOrigen(t, pool, origen); total != 19 {
		t.Fatalf("filas tras la carga reducida = %d, want 19", total)
	}
	retirada := filas[len(filas)-1].Nombre
	if version := versionDeLaFila(t, pool, retirada); version != "" {
		t.Fatalf("la fila retirada %q sigue con versión %q", retirada, version)
	}
	if version := versionDeLaFila(t, pool, filas[0].Nombre); version != "v4" {
		t.Fatalf("versión de la fila conservada = %q, want v4", version)
	}
}

func TestCargarRechazaUnaCategoriaDesconocidaSinEscribir(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	sufijo := sufijoDePrueba()
	origen := "prueba-categoria-" + sufijo
	limpiarCatalogo(t, pool, origen)
	t.Cleanup(func() { limpiarCatalogo(t, pool, origen) })

	reales := filasDePrueba(t, sufijo, 1)
	filas := []inteligencia.FilaCatalogoMarca{
		reales[0],
		{Nombre: "PRUEBA-" + sufijo + "-MALA", Categoria: inteligencia.Categoria("inexistente")},
	}
	if _, err := cargar(t.Context(), pool, filas, origen, "v3"); err == nil {
		t.Fatal("cargar aceptó una categoría que no está en inteligencia.categoria")
	}
	if total := contarDelOrigen(t, pool, origen); total != 0 {
		t.Fatalf("filas tras el rechazo = %d, want 0", total)
	}
}

// TestCargarRechazaSinCategoriaSinEscribir cubre la otra mitad del criterio: sin_categoria existe en
// inteligencia.categoria, así que la clave foránea la aceptaría, y el cargador la rechaza por la
// misma lista de visibles que usa el disparador de la tabla.
func TestCargarRechazaSinCategoriaSinEscribir(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	sufijo := sufijoDePrueba()
	origen := "prueba-sin-categoria-" + sufijo
	limpiarCatalogo(t, pool, origen)
	t.Cleanup(func() { limpiarCatalogo(t, pool, origen) })

	validas, err := inteligencia.CategoriasSembradas(t.Context(), pool)
	if err != nil {
		t.Fatalf("CategoriasSembradas: %v", err)
	}
	if _, visible := validas[string(inteligencia.SinCategoria)]; visible {
		t.Fatalf("la lista de visibles trae %q", inteligencia.SinCategoria)
	}

	reales := filasDePrueba(t, sufijo, 1)
	filas := []inteligencia.FilaCatalogoMarca{
		reales[0],
		{Nombre: "PRUEBA-" + sufijo + "-SIN", Categoria: inteligencia.SinCategoria},
	}
	_, err = cargar(t.Context(), pool, filas, origen, "v3")
	if !errors.Is(err, ErrCatalogoInvalido) {
		t.Fatalf("cargar = %v, want ErrCatalogoInvalido por la lista de visibles", err)
	}
	if total := contarDelOrigen(t, pool, origen); total != 0 {
		t.Fatalf("filas tras el rechazo = %d, want 0", total)
	}
}
