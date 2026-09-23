package kycsim

import (
	"context"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/kyc"
)

// El simulador es determinista y acepta el alias aprobado que trae el contrato del sandbox.
func TestSimuladorDeterminista(t *testing.T) {
	casos := map[string]string{
		"verificado": "verificado",
		"aprobado":   "verificado",
		"rechazado":  "rechazado",
		"pendiente":  "pendiente",
	}
	for entrada, esperado := range casos {
		t.Run(entrada, func(t *testing.T) {
			simulador, err := Nuevo(entrada)
			if err != nil {
				t.Fatalf("Nuevo(%q): %v", entrada, err)
			}
			resultado, err := simulador.Verificar(context.Background(), kyc.Solicitud{
				ParteID:   "00000000-0000-4000-8000-000000000001",
				Nivel:     kyc.NivelPleno,
				Proveedor: "simulador",
			})
			if err != nil {
				t.Fatalf("Verificar: %v", err)
			}
			if resultado.Estado != esperado {
				t.Fatalf("estado = %q, quiero %q", resultado.Estado, esperado)
			}
			if resultado.Referencia == "" || resultado.Medio == "" {
				t.Fatalf("resultado = %+v, quiero referencia y medio no vacíos", resultado)
			}
		})
	}
}

// Un resultado fuera del vocabulario detiene el arranque en vez de inventar un desenlace.
func TestSimuladorRechazaResultadoDesconocido(t *testing.T) {
	if _, err := Nuevo("quizas"); err == nil {
		t.Fatal("Nuevo(quizas) no falló")
	}
}
