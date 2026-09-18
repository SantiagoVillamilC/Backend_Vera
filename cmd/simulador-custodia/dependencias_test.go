package main

import (
	"os/exec"
	"strings"
	"testing"
)

// HU-NF-09 Criterio 1: el binario no arrastra el repositorio del núcleo ni el driver de Postgres.
func TestHU_NF_09_Criterio1_ElBinarioNoDependeDelNucleo(t *testing.T) {
	salida, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Skipf("sin toolchain de Go para inspeccionar dependencias: %v", err)
	}

	prohibidas := []string{"internal/nucleo", "internal/repositorio", "jackc/pgx", "jackc/pgerrcode", "riverqueue"}
	for _, prohibida := range prohibidas {
		if strings.Contains(string(salida), prohibida) {
			t.Errorf("el binario del simulador depende de %q, y el criterio 1 lo prohíbe", prohibida)
		}
	}
}
