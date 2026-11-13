//go:build integration

package inteligencia

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// filaCuradaDelArchivo lee el primer nombre del catálogo v3 y su categoría del mismo archivo que
// carga cmd/cargar-catalogo, para que la prueba use una fila curada de verdad.
func filaCuradaDelArchivo(t *testing.T) (string, Categoria) {
	t.Helper()
	crudo, err := os.ReadFile(filepath.Join("..", "..", "bd", "datos", "catalogo-v3", "catalogo_v3.csv"))
	if err != nil {
		t.Fatalf("leer el catálogo del repositorio: %v", err)
	}
	lector := csv.NewReader(bytes.NewReader(crudo))
	lector.FieldsPerRecord = -1
	cabecera, err := lector.Read()
	if err != nil {
		t.Fatalf("leer el encabezado del catálogo: %v", err)
	}
	indice := make(map[string]int, len(cabecera))
	for posicion, columna := range cabecera {
		indice[strings.TrimSpace(strings.TrimPrefix(columna, "\ufeff"))] = posicion
	}
	posicionNombre, hayNombre := indice["nombre_normalizado"]
	posicionCategoria, hayCategoria := indice["categoria"]
	if !hayNombre || !hayCategoria {
		t.Fatalf("el encabezado del catálogo = %v, want nombre_normalizado y categoria", cabecera)
	}
	registro, err := lector.Read()
	if err != nil {
		t.Fatalf("leer la primera fila del catálogo: %v", err)
	}
	if posicionNombre >= len(registro) || posicionCategoria >= len(registro) {
		t.Fatalf("la primera fila del catálogo = %v, want las dos columnas", registro)
	}
	return strings.TrimSpace(registro[posicionNombre]),
		Categoria(strings.TrimSpace(registro[posicionCategoria]))
}

// asegurarNombreCurado deja el nombre curado en la tabla sin pisar la fila que ya esté: si está con
// su categoría la usa tal cual, y si falta la escribe con un origen propio que la limpieza retira al
// terminar. Así la prueba no depende de que la base venga sembrada desde fuera del repositorio y
// tampoco borra ninguna fila curada.
func asegurarNombreCurado(t *testing.T, pool *pgxpool.Pool, nombre string, categoria Categoria) {
	t.Helper()
	ctx := t.Context()
	origen := "prueba-e2e-" + sufijoDePrueba()

	fila, err := enTransaccion(ctx, poolDeInteligencia{pool: pool},
		func(ctx context.Context, _ transaccionDeInteligencia, q *repositorio.Queries) (repositorio.CatalogoMarca, error) {
			return q.BuscarCatalogoPorNombre(ctx, nombre)
		})
	switch {
	case err == nil:
		if Categoria(fila.Categoria) != categoria {
			t.Fatalf("la fila %q tiene categoría %q en la base, want %q", nombre, fila.Categoria,
				categoria)
		}
		return
	case !errors.Is(err, pgx.ErrNoRows):
		t.Fatalf("buscar %q en el catálogo: %v", nombre, err)
	}

	if _, err := GuardarCatalogoMarca(ctx, pool,
		[]FilaCatalogoMarca{{Nombre: nombre, Categoria: categoria}}, origen, "prueba"); err != nil {
		t.Fatalf("sembrar %q en el catálogo: %v", nombre, err)
	}
	t.Cleanup(func() { retirarCatalogoDePrueba(t, pool, origen) })
}

// TestUnNombreDelCatalogoCuradoSaleConOrigenCatalogo corre un lote real de Enriquecer con un nombre
// que está en el catálogo curado y comprueba el origen en inteligencia.movimiento_enriquecido, no
// solo en la llamada a Decidir. Los asientos, la memoria y la copia son de un usuario propio y se
// borran al terminar.
func TestUnNombreDelCatalogoCuradoSaleConOrigenCatalogo(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	nombre, categoria := filaCuradaDelArchivo(t)
	asegurarNombreCurado(t, pool, nombre, categoria)

	servicio := NuevoServicio(pool, ConCatalogo(make(map[string]Categoria), make(map[string]Categoria)))
	if err := servicio.CargarCatalogo(ctx); err != nil {
		t.Fatalf("CargarCatalogo: %v", err)
	}
	if cargada := servicio.catalogo().Nombres[nombre]; cargada != categoria {
		t.Fatalf("catálogo = %q para %q, want %q: CargarCatalogo no trajo la fila", cargada, nombre,
			categoria)
	}
	escalones := escalonesDelLote(servicio.catalogo(), nil, nil)
	if decision := Decidir(Entrada{
		Canal:       CanalQR,
		Nombre:      nombre,
		TipoDestino: DestinoComercio,
	}, Recursos{CatalogoNombres: servicio.catalogo().Nombres}, escalones); decision.Escalon != EscalonCatalogo {
		t.Fatalf("decisión = %+v, want escalón %d antes de correr el lote", decision, EscalonCatalogo)
	}

	usuario := uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, usuario)
	asiento := sembrarAsiento(t, pool, "despacho_riel", nil, nombre, "",
		time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC), []lineaDePrueba{
			{cuenta: "pasivo:usuario:" + usuario + ":disponible", monto: 5000},
			{cuenta: "pasivo:liquidacion_pendiente:riel", monto: -5000},
		})
	cursorAnterior := leerCursorDeEnriquecimiento(t, pool)
	t.Cleanup(func() {
		borrarPorUsuarios(t, pool,
			"DELETE FROM inteligencia.movimiento_enriquecido WHERE usuario_id = ANY($1)", []string{usuario})
		escribirCursorDeEnriquecimiento(t, pool, mayorDe([]int64{cursorAnterior, asiento}))
	})
	escribirCursorDeEnriquecimiento(t, pool, asiento-1)

	resultado, err := servicio.Enriquecer(ctx, 1)
	if err != nil {
		t.Fatalf("Enriquecer: %v", err)
	}
	if resultado.PorOrigen[OrigenCatalogo] < 1 {
		t.Fatalf("por origen = %v, want al menos un movimiento de %q", resultado.PorOrigen, OrigenCatalogo)
	}
	transaccion := transaccionDeGrupo(grupo{clave: fmt.Sprintf("entry:%d", asiento)})
	leido := leerMovimiento(t, pool, usuario, transaccion)
	if leido.Origen != OrigenCatalogo || leido.Categoria != string(categoria) {
		t.Fatalf("movimiento en la copia = origen %q, categoría %q, want origen %q, categoría %q",
			leido.Origen, leido.Categoria, OrigenCatalogo, categoria)
	}
}
