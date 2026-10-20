package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
)

// escribirCatalogo deja el CSV en un directorio temporal y devuelve su ruta.
func escribirCatalogo(t *testing.T, contenido string) string {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "catalogo.csv")
	if err := os.WriteFile(ruta, []byte(contenido), 0o600); err != nil {
		t.Fatalf("escribir el catálogo de prueba: %v", err)
	}
	return ruta
}

// TestLeerCatalogoDevuelveLasLlavesYLaHuella fija lo que el informe necesita para ser reproducible.
func TestLeerCatalogoDevuelveLasLlavesYLaHuella(t *testing.T) {
	t.Parallel()
	contenido := "nombre_normalizado,categoria\nTIENDA-UNO,salud\nTIENDA-DOS,compras\n"
	catalogo, huella, err := leerCatalogo(escribirCatalogo(t, contenido))
	if err != nil {
		t.Fatalf("leerCatalogo: %v", err)
	}
	if len(catalogo) != 2 || catalogo["TIENDA-UNO"] != inteligencia.CategoriaSalud ||
		catalogo["TIENDA-DOS"] != inteligencia.CategoriaCompras {
		t.Fatalf("catálogo = %v", catalogo)
	}
	suma := sha256.Sum256([]byte(contenido))
	if huella != hex.EncodeToString(suma[:]) {
		t.Fatalf("huella = %q, quiero %q", huella, hex.EncodeToString(suma[:]))
	}
}

// TestLeerCatalogoFallaCerrado cierra la puerta a un catálogo que en realidad mide otra cosa.
func TestLeerCatalogoFallaCerrado(t *testing.T) {
	t.Parallel()
	casos := map[string]string{
		"sin la columna de la categoría": "nombre_normalizado\nTIENDA-UNO\n",
		"categoría fuera de gasto":       "nombre_normalizado,categoria\nTIENDA-UNO,salario\n",
		"el mismo nombre con dos clases": "nombre_normalizado,categoria\nTIENDA-UNO,salud\nTIENDA-UNO,compras\n",
		"nombre vacío":                   "nombre_normalizado,categoria\n,salud\n",
		"catálogo sin filas":             "nombre_normalizado,categoria\n",
	}
	for nombre, contenido := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if _, _, err := leerCatalogo(escribirCatalogo(t, contenido)); err == nil {
				t.Fatalf("leerCatalogo aceptó %q", contenido)
			}
		})
	}
}

// TestEscalonesDeCatalogoEnciendeSoloElEscalonCuatro evita que el modo mida la cascada entera.
func TestEscalonesDeCatalogoEnciendeSoloElEscalonCuatro(t *testing.T) {
	t.Parallel()
	escalones, err := escalonesDe(ModoCatalogo)
	if err != nil {
		t.Fatalf("escalonesDe: %v", err)
	}
	if len(escalones) != 1 || !escalones.Contiene(inteligencia.EscalonCatalogo) {
		t.Fatalf("escalones = %v", escalones)
	}
	todos, err := escalonesDe(ModoTodos)
	if err != nil {
		t.Fatalf("escalonesDe todos: %v", err)
	}
	if !todos.Contiene(inteligencia.EscalonCatalogo) {
		t.Fatalf("el modo todos no enciende el catálogo: %v", todos)
	}
}

// TestDecisorDeCatalogoBuscaPorElNombreNormalizado comprueba que la llave del CSV es la que usa la cascada.
func TestDecisorDeCatalogoBuscaPorElNombreNormalizado(t *testing.T) {
	t.Parallel()
	catalogo := map[string]inteligencia.Categoria{"TIENDA-UNO": inteligencia.CategoriaSalud}
	decidir, err := decisorDe(ModoCatalogo, nil, nil, catalogo)
	if err != nil {
		t.Fatalf("decisorDe: %v", err)
	}
	decision := decidir(Fila{ID: "g0001", Nombre: "tienda uno"})
	if decision.Categoria != string(inteligencia.CategoriaSalud) || decision.Origen != inteligencia.OrigenCatalogo {
		t.Fatalf("decisión = %+v", decision)
	}
	if vacia := decidir(Fila{ID: "g0002", Nombre: "otro nombre"}); vacia.Categoria != "" {
		t.Fatalf("decisión para un nombre ausente = %+v", vacia)
	}
}
