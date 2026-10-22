//go:build paridad

package inteligencia

import (
	"encoding/csv"
	"io"
	"os"
	"testing"
)

// TestParidadDesarrolloContraPython compara, fila a fila, el artefacto embebido de Go con la
// predicción que dejó la referencia Python en el CSV de desarrollo. El CSV lo produce
// `dataset-inteligencia/medir_desarrollo_tfidf.py`, que aborta si la partición de prueba aparece en
// la salida. Aquí se vuelve a exigir lo mismo antes de mirar ninguna cifra: la paridad se comprueba
// solo en desarrollo.
//
// Se corre con `go test -tags paridad ./internal/inteligencia/`. La ruta del CSV se puede cambiar con
// la variable PARIDAD_CSV.
func TestParidadDesarrolloContraPython(t *testing.T) {
	ruta := os.Getenv("PARIDAD_CSV")
	if ruta == "" {
		ruta = "/tmp/paridad-tfidf-desarrollo.csv"
	}
	archivo, err := os.Open(ruta)
	if err != nil {
		t.Fatalf("abrir el CSV de paridad: %v", err)
	}
	defer archivo.Close()

	modelo, err := ModeloTFIDFEmbebido()
	if err != nil {
		t.Fatalf("cargar el artefacto embebido: %v", err)
	}

	lector := csv.NewReader(archivo)
	encabezado, err := lector.Read()
	if err != nil {
		t.Fatalf("leer el encabezado: %v", err)
	}
	columna := map[string]int{}
	for i, nombre := range encabezado {
		columna[nombre] = i
	}
	for _, exigida := range []string{"nombre", "mcc_categoria", "categoria_esperada", "prediccion_python", "parte"} {
		if _, existe := columna[exigida]; !existe {
			t.Fatalf("el CSV no trae la columna %q", exigida)
		}
	}

	decididas, aciertos, filas := 0, 0, 0
	for {
		fila, err := lector.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("leer una fila: %v", err)
		}
		filas++
		if parte := fila[columna["parte"]]; parte != "desarrollo" {
			t.Fatalf("el CSV trae una fila de la partición %q: la paridad se mide solo en desarrollo", parte)
		}
		nombre := fila[columna["nombre"]]
		mcc := fila[columna["mcc_categoria"]]
		esperada := fila[columna["categoria_esperada"]]
		dePython := fila[columna["prediccion_python"]]

		categoria, decidio := Predecir(modelo, nombre, mcc)
		obtenida := ""
		if decidio {
			obtenida = string(categoria)
			decididas++
		}
		if obtenida != dePython {
			t.Fatalf("%q: Go dice %q y Python dijo %q", nombre, obtenida, dePython)
		}
		if obtenida != "" && obtenida == esperada {
			aciertos++
		}
	}

	if filas == 0 {
		t.Fatalf("el CSV no trae filas")
	}
	if decididas != 39 || aciertos != 38 {
		t.Fatalf("la paridad da %d decididas y %d correctas, want 39 y 38", decididas, aciertos)
	}
	t.Logf("paridad en desarrollo: %d filas, %d decididas, %d correctas", filas, decididas, aciertos)
}
