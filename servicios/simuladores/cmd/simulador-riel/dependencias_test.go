package main

import (
	"os/exec"
	"strings"
	"testing"
)

// El binario del simulador no puede arrastrar el repositorio del núcleo ni su base de datos.
func TestElBinarioNoDependeDelNucleo(t *testing.T) {
	salida, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("sin toolchain de Go para inspeccionar dependencias: %v", err)
	}

	prohibidas := []string{"internal/nucleo", "internal/repositorio", "jackc/pgx", "jackc/pgerrcode", "riverqueue"}
	for _, prohibida := range prohibidas {
		if strings.Contains(string(salida), prohibida) {
			t.Errorf("el binario del simulador depende de %q, y la frontera de módulos lo prohíbe", prohibida)
		}
	}
}
