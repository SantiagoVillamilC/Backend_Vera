// Package main carga el catálogo curado de marcas del escalón 4 en la tabla de inteligencia.
//
// El archivo y su manifiesto viven en bd/datos/catalogo-v3 y son copia literal de la herramienta de
// evaluación: la huella del manifiesto es lo que garantiza que el servicio busca sobre el mismo
// catálogo que se midió.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ArchivoDelManifiesto es la clave del manifiesto con la huella del CSV que se carga.
const ArchivoDelManifiesto = "catalogo_v3.csv"

// OrigenPorDefecto marca de dónde salieron las filas, para poder borrar solo las de este archivo.
const OrigenPorDefecto = "catalogo_v3"

// ErrHuellaDistinta reporta un CSV que no es el que declara el manifiesto.
var ErrHuellaDistinta = errors.New("la huella del CSV no coincide con el manifiesto")

// ErrCatalogoInvalido reporta un archivo que no se puede cargar: encabezado, nombre o categoría.
var ErrCatalogoInvalido = errors.New("el catálogo no es válido")

// Manifiesto es la parte del manifiesto que el cargador necesita.
type Manifiesto struct {
	Version        string            `json:"version"`
	ArchivosSha256 map[string]string `json:"archivos_sha256"`
}

// Conteos es lo que el cargador informa al terminar.
type Conteos struct {
	Leidas       int
	Insertadas   int64
	Actualizadas int64
	Borradas     int64
}

func main() {
	if err := run(); err != nil {
		slog.Error("cargar-catalogo", "error", err)
		os.Exit(1)
	}
}

func run() error {
	rutaCSV := flag.String("csv", "bd/datos/catalogo-v3/catalogo_v3.csv", "CSV del catálogo curado")
	rutaManifiesto := flag.String("manifiesto", "bd/datos/catalogo-v3/manifiesto.json",
		"manifiesto con la huella del CSV")
	aplicar := flag.Bool("aplicar", false, "escribe en Postgres; sin esta bandera solo verifica y reporta")
	origen := flag.String("origen", OrigenPorDefecto, "valor de la columna origen")
	version := flag.String("version", "", "versión a cargar; vacía toma la del manifiesto")
	flag.Parse()

	manifiesto, err := leerManifiesto(*rutaManifiesto)
	if err != nil {
		return err
	}
	crudo, err := os.ReadFile(*rutaCSV) //nolint:gosec // la ruta la pasa el operador.
	if err != nil {
		return fmt.Errorf("leer el catálogo: %w", err)
	}
	esperada, existe := manifiesto.ArchivosSha256[ArchivoDelManifiesto]
	if !existe {
		return fmt.Errorf("%w: el manifiesto no trae %q", ErrHuellaDistinta, ArchivoDelManifiesto)
	}
	if err := verificarHuella(crudo, esperada); err != nil {
		return err
	}
	filas, err := leerCatalogo(crudo)
	if err != nil {
		return err
	}
	versionACargar := strings.TrimSpace(*version)
	if versionACargar == "" {
		versionACargar = strings.TrimSpace(manifiesto.Version)
	}
	if versionACargar == "" {
		return fmt.Errorf("%w: no hay versión ni en el manifiesto ni en -version", ErrCatalogoInvalido)
	}
	if strings.TrimSpace(*origen) == "" {
		return fmt.Errorf("%w: el origen no puede ir vacío", ErrCatalogoInvalido)
	}
	if !*aplicar {
		fmt.Printf("verificado: %s huella %s, leidas %d, origen %s, version %s (sin -aplicar no se escribió nada)\n",
			*rutaCSV, esperada, len(filas), *origen, versionACargar)
		return nil
	}

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		return errors.New("APP_DATABASE_URL es obligatoria para aplicar el catálogo")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("abrir pool: %w", err)
	}
	defer pool.Close()

	conteos, err := cargar(ctx, pool, filas, *origen, versionACargar)
	if err != nil {
		return err
	}
	fmt.Printf("cargado: %s huella %s, leidas %d, insertadas %d, actualizadas %d, borradas %d, "+
		"origen %s, version %s\n", *rutaCSV, esperada, conteos.Leidas, conteos.Insertadas,
		conteos.Actualizadas, conteos.Borradas, *origen, versionACargar)
	return nil
}

// leerManifiesto lee el manifiesto y exige que declare la huella del archivo del catálogo.
func leerManifiesto(ruta string) (Manifiesto, error) {
	crudo, err := os.ReadFile(ruta) //nolint:gosec // la ruta la pasa el operador.
	if err != nil {
		return Manifiesto{}, fmt.Errorf("leer el manifiesto: %w", err)
	}
	var manifiesto Manifiesto
	if err := json.Unmarshal(crudo, &manifiesto); err != nil {
		return Manifiesto{}, fmt.Errorf("parsear el manifiesto: %w", err)
	}
	if _, existe := manifiesto.ArchivosSha256[ArchivoDelManifiesto]; !existe {
		return Manifiesto{}, fmt.Errorf("%w: el manifiesto no trae %q", ErrHuellaDistinta,
			ArchivoDelManifiesto)
	}
	return manifiesto, nil
}

// verificarHuella compara el sha256 del archivo con el que declara el manifiesto.
func verificarHuella(crudo []byte, esperada string) error {
	suma := sha256.Sum256(crudo)
	calculada := hex.EncodeToString(suma[:])
	if calculada != strings.ToLower(strings.TrimSpace(esperada)) {
		return fmt.Errorf("%w: calculada %s, manifiesto %s", ErrHuellaDistinta, calculada, esperada)
	}
	return nil
}

// leerCatalogo parsea el CSV, exige los nombres en la forma en que la cascada los busca y rechaza
// las filas repetidas.
//
// El nombre se vuelve a pasar por ClaveDeCatalogo y se exige punto fijo: la cascada busca con esa
// función, así que una fila en otra forma nunca se encontraría y es mejor abortar que cargarla.
func leerCatalogo(crudo []byte) ([]inteligencia.FilaCatalogoMarca, error) {
	lector := csv.NewReader(bytes.NewReader(crudo))
	lector.FieldsPerRecord = -1
	cabecera, err := lector.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: leer el encabezado: %w", ErrCatalogoInvalido, err)
	}
	indice, err := indiceDeColumnas(cabecera)
	if err != nil {
		return nil, err
	}
	filas := make([]inteligencia.FilaCatalogoMarca, 0, 5728)
	vistos := make(map[string]struct{}, 5728)
	for numero := 2; ; numero++ {
		registro, err := lector.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: leer la fila %d: %w", ErrCatalogoInvalido, numero, err)
		}
		fila, err := filaDe(registro, indice, numero)
		if err != nil {
			return nil, err
		}
		if _, repetida := vistos[fila.Nombre]; repetida {
			return nil, fmt.Errorf("%w: el nombre %q aparece dos veces", ErrCatalogoInvalido,
				fila.Nombre)
		}
		vistos[fila.Nombre] = struct{}{}
		filas = append(filas, fila)
	}
	if len(filas) == 0 {
		return nil, fmt.Errorf("%w: el archivo no trae filas", ErrCatalogoInvalido)
	}
	return filas, nil
}

// indiceDeColumnas valida el encabezado y devuelve la posición de cada columna conocida.
func indiceDeColumnas(cabecera []string) (map[string]int, error) {
	indice := make(map[string]int, len(cabecera))
	for posicion, nombre := range cabecera {
		limpio := strings.TrimSpace(strings.TrimPrefix(nombre, "\ufeff"))
		switch limpio {
		case "nombre_normalizado", "categoria", "llave":
			indice[limpio] = posicion
		default:
			return nil, fmt.Errorf("%w: columna %q desconocida", ErrCatalogoInvalido, limpio)
		}
	}
	for _, obligatoria := range []string{"nombre_normalizado", "categoria"} {
		if _, existe := indice[obligatoria]; !existe {
			return nil, fmt.Errorf("%w: el encabezado no trae %q", ErrCatalogoInvalido, obligatoria)
		}
	}
	return indice, nil
}

// filaDe convierte un registro en una fila validada.
func filaDe(registro []string, indice map[string]int, numero int) (inteligencia.FilaCatalogoMarca, error) {
	nombre := campo(registro, indice, "nombre_normalizado")
	if nombre == "" {
		return inteligencia.FilaCatalogoMarca{},
			fmt.Errorf("%w: la fila %d no trae nombre", ErrCatalogoInvalido, numero)
	}
	if clave := inteligencia.ClaveDeCatalogo(nombre); clave != nombre {
		return inteligencia.FilaCatalogoMarca{},
			fmt.Errorf("%w: el nombre %q de la fila %d no está en la forma que busca la cascada, quiero %q",
				ErrCatalogoInvalido, nombre, numero, clave)
	}
	categoria := campo(registro, indice, "categoria")
	if categoria == "" {
		return inteligencia.FilaCatalogoMarca{},
			fmt.Errorf("%w: la fila %d no trae categoría", ErrCatalogoInvalido, numero)
	}
	return inteligencia.FilaCatalogoMarca{
		Nombre:    nombre,
		Categoria: inteligencia.Categoria(categoria),
		Llave:     campo(registro, indice, "llave"),
	}, nil
}

// campo devuelve el valor de una columna, o vacío si la columna no existe en el archivo.
func campo(registro []string, indice map[string]int, columna string) string {
	posicion, existe := indice[columna]
	if !existe || posicion >= len(registro) {
		return ""
	}
	return strings.TrimSpace(registro[posicion])
}

// validarCategorias rechaza cualquier fila cuya categoría no sea una de las visibles de
// inteligencia.categoria: un código existente con grupo ninguno tampoco se admite.
func validarCategorias(filas []inteligencia.FilaCatalogoMarca, validas map[string]struct{}) error {
	for _, fila := range filas {
		if _, existe := validas[string(fila.Categoria)]; !existe {
			return fmt.Errorf("%w: la categoría %q de %q no es visible en inteligencia.categoria",
				ErrCatalogoInvalido, fila.Categoria, fila.Nombre)
		}
	}
	return nil
}

// cargar valida las categorías contra la tabla sembrada y escribe el catálogo en una sola
// transacción, con el mismo helper que usa el servicio.
func cargar(ctx context.Context, pool *pgxpool.Pool, filas []inteligencia.FilaCatalogoMarca, origen,
	version string) (Conteos, error) {
	conteos := Conteos{Leidas: len(filas)}
	validas, err := inteligencia.CategoriasSembradas(ctx, pool)
	if err != nil {
		return Conteos{}, err
	}
	if err := validarCategorias(filas, validas); err != nil {
		return Conteos{}, err
	}
	escritos, err := inteligencia.GuardarCatalogoMarca(ctx, pool, filas, origen, version)
	if err != nil {
		return Conteos{}, err
	}
	conteos.Insertadas = escritos.Insertadas
	conteos.Actualizadas = escritos.Actualizadas
	conteos.Borradas = escritos.Borradas
	return conteos, nil
}
