// Command evaluar-vera ejecuta el conjunto determinista sin red, claves ni base de datos.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
)

func main() { os.Exit(ejecutar(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func ejecutar(ctx context.Context, args []string, salida, diagnostico io.Writer) int {
	opciones := flag.NewFlagSet("evaluar-vera", flag.ContinueOnError)
	opciones.SetOutput(diagnostico)
	ruta := opciones.String("casos", "", "JSON alternativo; por defecto usa el conjunto versionado incluido en el binario")
	if err := opciones.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if opciones.NArg() != 0 {
		_, _ = fmt.Fprintln(diagnostico, "No se aceptan argumentos posicionales.")
		return 2
	}
	conjunto, err := cargar(*ruta)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostico, "No se pudo cargar el conjunto:", err)
		return 2
	}
	informe, err := evaluacion.EjecutarSimulados(ctx, conjunto)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostico, "No se pudo completar la evaluación:", err)
		return 2
	}
	encoder := json.NewEncoder(salida)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(informe); err != nil {
		_, _ = fmt.Fprintln(diagnostico, "No se pudo escribir el informe.")
		return 2
	}
	if !informe.Aprobado {
		return 1
	}
	return 0
}

func cargar(ruta string) (evaluacion.Conjunto, error) {
	if ruta == "" {
		return evaluacion.Cargar()
	}
	archivo, err := os.Open(ruta) // #nosec G304 -- Archivo elegido explícitamente por quien ejecuta esta CLI local; no recibe rutas HTTP ni ejecuta su contenido.
	if err != nil {
		return evaluacion.Conjunto{}, err
	}
	defer func() { _ = archivo.Close() }()
	return evaluacion.Leer(archivo)
}
