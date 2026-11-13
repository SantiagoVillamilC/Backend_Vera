package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
)

const cabeceraDePrueba = "nombre_normalizado,categoria\n"

// manifiestoDe arma un manifiesto con la huella del CSV dado.
func manifiestoDe(crudo []byte, version string) Manifiesto {
	suma := sha256.Sum256(crudo)
	return Manifiesto{
		Version:        version,
		ArchivosSha256: map[string]string{ArchivoDelManifiesto: hex.EncodeToString(suma[:])},
	}
}

func TestVerificarHuellaAceptaElArchivoDelManifiesto(t *testing.T) {
	crudo := []byte(cabeceraDePrueba + "ALMACEN-EXITO,alimentos_mercado\n")
	manifiesto := manifiestoDe(crudo, "v3")
	if err := verificarHuella(crudo, manifiesto.ArchivosSha256[ArchivoDelManifiesto]); err != nil {
		t.Fatalf("verificarHuella: %v", err)
	}
}

func TestUnCSVConHuellaDistintaSeRechaza(t *testing.T) {
	esperada := manifiestoDe([]byte(cabeceraDePrueba+"ALMACEN-EXITO,alimentos_mercado\n"), "v3")
	alterado := []byte(cabeceraDePrueba + "ALMACEN-EXITO,compras\n")
	err := verificarHuella(alterado, esperada.ArchivosSha256[ArchivoDelManifiesto])
	if !errors.Is(err, ErrHuellaDistinta) {
		t.Fatalf("verificarHuella = %v, want %v", err, ErrHuellaDistinta)
	}
}

func TestUnManifiestoSinElArchivoSeRechaza(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "manifiesto.json")
	crudo, err := json.Marshal(Manifiesto{Version: "v3", ArchivosSha256: map[string]string{"otro.csv": "00"}})
	if err != nil {
		t.Fatalf("serializar el manifiesto: %v", err)
	}
	if err := os.WriteFile(ruta, crudo, 0o600); err != nil {
		t.Fatalf("escribir el manifiesto: %v", err)
	}
	if _, err := leerManifiesto(ruta); !errors.Is(err, ErrHuellaDistinta) {
		t.Fatalf("leerManifiesto = %v, want %v", err, ErrHuellaDistinta)
	}
}

func TestElEncabezadoSeParseaConBOMYEspacios(t *testing.T) {
	crudo := []byte("\ufeffnombre_normalizado, categoria \nALMACEN-EXITO,alimentos_mercado\n")
	filas, err := leerCatalogo(crudo)
	if err != nil {
		t.Fatalf("leerCatalogo: %v", err)
	}
	if len(filas) != 1 || filas[0].Nombre != "ALMACEN-EXITO" ||
		filas[0].Categoria != inteligencia.CategoriaAlimentosMercado {
		t.Fatalf("filas = %+v, want una fila de ALMACEN-EXITO en alimentos_mercado", filas)
	}
}

func TestElEncabezadoIncompletoSeRechaza(t *testing.T) {
	casos := map[string]string{
		"sin categoria":  "nombre_normalizado\nALMACEN-EXITO\n",
		"sin nombre":     "categoria\nalimentos_mercado\n",
		"columna extra":  "nombre_normalizado,categoria,mcc\nALMACEN-EXITO,alimentos_mercado,5411\n",
		"archivo vacío":  "",
		"solo cabecera":  cabeceraDePrueba,
		"sin encabezado": "ALMACEN-EXITO,alimentos_mercado\n",
	}
	for nombre, crudo := range casos {
		t.Run(nombre, func(t *testing.T) {
			if _, err := leerCatalogo([]byte(crudo)); !errors.Is(err, ErrCatalogoInvalido) {
				t.Fatalf("leerCatalogo = %v, want %v", err, ErrCatalogoInvalido)
			}
		})
	}
}

func TestUnNombreSinNormalizarSeRechaza(t *testing.T) {
	crudo := []byte(cabeceraDePrueba + "Almacén Éxito S.A.S.,alimentos_mercado\n")
	_, err := leerCatalogo(crudo)
	if !errors.Is(err, ErrCatalogoInvalido) {
		t.Fatalf("leerCatalogo = %v, want %v", err, ErrCatalogoInvalido)
	}
	if !strings.Contains(err.Error(), inteligencia.ClaveDeCatalogo("Almacén Éxito S.A.S.")) {
		t.Fatalf("el error no menciona la forma que busca la cascada: %v", err)
	}
}

func TestLaNormalizacionDelCargadorEsLaDeLaCascada(t *testing.T) {
	nombre := inteligencia.ClaveDeCatalogo("Almacén Éxito S.A.S.")
	if nombre != "ALMACEN-EXITO-SAS" {
		t.Fatalf("ClaveDeCatalogo = %q, want ALMACEN-EXITO-SAS", nombre)
	}
	filas, err := leerCatalogo([]byte(cabeceraDePrueba + nombre + ",alimentos_mercado\n"))
	if err != nil {
		t.Fatalf("leerCatalogo: %v", err)
	}
	if filas[0].Nombre != nombre {
		t.Fatalf("nombre cargado = %q, want %q", filas[0].Nombre, nombre)
	}
}

func TestUnaCategoriaDesconocidaSeRechaza(t *testing.T) {
	crudo := []byte(cabeceraDePrueba + "ALMACEN-EXITO,alimentos_mercado\nTIENDA-NUEVA,inventada\n")
	filas, err := leerCatalogo(crudo)
	if err != nil {
		t.Fatalf("leerCatalogo: %v", err)
	}
	validas := map[string]struct{}{string(inteligencia.CategoriaAlimentosMercado): {},
		string(inteligencia.CategoriaCompras): {}}
	err = validarCategorias(filas, validas)
	if !errors.Is(err, ErrCatalogoInvalido) {
		t.Fatalf("validarCategorias = %v, want %v", err, ErrCatalogoInvalido)
	}
	if !strings.Contains(err.Error(), `"inventada"`) {
		t.Fatalf("el error no nombra la categoría: %v", err)
	}
}

func TestLasFilasRepetidasSeRechazan(t *testing.T) {
	crudo := []byte(cabeceraDePrueba +
		"ALMACEN-EXITO,alimentos_mercado\nALMACEN-EXITO,alimentos_mercado\n")
	if _, err := leerCatalogo(crudo); !errors.Is(err, ErrCatalogoInvalido) {
		t.Fatalf("leerCatalogo = %v, want %v", err, ErrCatalogoInvalido)
	}
}

func TestLaLlaveOpcionalSeLee(t *testing.T) {
	crudo := []byte("nombre_normalizado,categoria,llave\nDROGUERIA-NORTE,salud,@droguerianorte\n")
	filas, err := leerCatalogo(crudo)
	if err != nil {
		t.Fatalf("leerCatalogo: %v", err)
	}
	if filas[0].Llave != "@droguerianorte" {
		t.Fatalf("llave = %q, want @droguerianorte", filas[0].Llave)
	}
}

func TestElArchivoDelRepositorioCoincideConSuManifiesto(t *testing.T) {
	manifiesto, err := leerManifiesto(filepath.Join("..", "..", "bd", "datos", "catalogo-v3",
		"manifiesto.json"))
	if err != nil {
		t.Fatalf("leerManifiesto: %v", err)
	}
	crudo, err := os.ReadFile(filepath.Join("..", "..", "bd", "datos", "catalogo-v3",
		"catalogo_v3.csv"))
	if err != nil {
		t.Fatalf("leer el catálogo: %v", err)
	}
	if err := verificarHuella(crudo, manifiesto.ArchivosSha256[ArchivoDelManifiesto]); err != nil {
		t.Fatalf("verificarHuella: %v", err)
	}
	filas, err := leerCatalogo(crudo)
	if err != nil {
		t.Fatalf("leerCatalogo: %v", err)
	}
	if len(filas) != 5728 {
		t.Fatalf("filas = %d, want 5728", len(filas))
	}
	if manifiesto.Version != "v3" {
		t.Fatalf("versión del manifiesto = %q, want v3", manifiesto.Version)
	}
	for _, fila := range filas {
		if clave := inteligencia.ClaveDeCatalogo(fila.Nombre); clave != fila.Nombre {
			t.Fatalf("%q no es la forma que busca la cascada, quiero %q", fila.Nombre, clave)
		}
	}
}

func TestUnNombreSinCortarAlLargoDelEvaluadorSeRechaza(t *testing.T) {
	largo := inteligencia.NormalizarNombre("Almacén Facol 102 Alquería")
	if largo != "ALMACEN-FACOL-102-ALQUERIA" {
		t.Fatalf("NormalizarNombre = %q, want ALMACEN-FACOL-102-ALQUERIA", largo)
	}
	clave := inteligencia.ClaveDeCatalogo("Almacén Facol 102 Alquería")
	if clave != "ALMACEN-FACOL-102-ALQUERI" {
		t.Fatalf("ClaveDeCatalogo = %q, want ALMACEN-FACOL-102-ALQUERI", clave)
	}
	_, err := leerCatalogo([]byte(cabeceraDePrueba + largo + ",compras\n"))
	if !errors.Is(err, ErrCatalogoInvalido) {
		t.Fatalf("leerCatalogo = %v, want %v", err, ErrCatalogoInvalido)
	}
	if !strings.Contains(err.Error(), clave) {
		t.Fatalf("el error no menciona la clave del evaluador: %v", err)
	}
}

func TestUnaHuellaDistintaAbortaAntesDeAbrirLaBase(t *testing.T) {
	dir := t.TempDir()
	rutaCSV := filepath.Join(dir, "catalogo_v3.csv")
	if err := os.WriteFile(rutaCSV, []byte(cabeceraDePrueba+"ALMACEN-EXITO,compras\n"), 0o600); err != nil {
		t.Fatalf("escribir el CSV: %v", err)
	}
	manifiesto := manifiestoDe([]byte(cabeceraDePrueba+"ALMACEN-EXITO,alimentos_mercado\n"), "v3")
	crudoManifiesto, err := json.Marshal(manifiesto)
	if err != nil {
		t.Fatalf("serializar el manifiesto: %v", err)
	}
	rutaManifiesto := filepath.Join(dir, "manifiesto.json")
	if err := os.WriteFile(rutaManifiesto, crudoManifiesto, 0o600); err != nil {
		t.Fatalf("escribir el manifiesto: %v", err)
	}

	t.Setenv("APP_DATABASE_URL", "")
	banderas := flag.CommandLine
	argumentos := os.Args
	t.Cleanup(func() {
		flag.CommandLine = banderas
		os.Args = argumentos
	})
	flag.CommandLine = flag.NewFlagSet("cargar-catalogo", flag.ContinueOnError)
	os.Args = []string{"cargar-catalogo", "-csv", rutaCSV, "-manifiesto", rutaManifiesto, "-aplicar"}

	err = run()
	if !errors.Is(err, ErrHuellaDistinta) {
		t.Fatalf("run = %v, want %v: la huella se comprueba antes de tocar la base", err, ErrHuellaDistinta)
	}
}
