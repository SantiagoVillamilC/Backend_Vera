// Package kycsim es el proveedor de KYC simulado, determinista y sin red, para pruebas y demo.
package kycsim

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/kyc"
)

// Simulador devuelve un resultado fijo y determinista para toda verificación.
type Simulador struct{ resultado string }

// Nuevo arma el simulador con el resultado fijado por la configuración.
func Nuevo(resultado string) (*Simulador, error) {
	canonico, err := normalizar(resultado)
	if err != nil {
		return nil, err
	}
	return &Simulador{resultado: canonico}, nil
}

// Verificar devuelve el resultado fijado, sin red ni aleatoriedad.
func (s *Simulador) Verificar(ctx context.Context, solicitud kyc.Solicitud) (kyc.Resultado, error) {
	slog.InfoContext(ctx, "kyc simulado", "nivel", solicitud.Nivel, "resultado", s.resultado)
	return kyc.Resultado{
		Estado:     s.resultado,
		Referencia: "sim-" + solicitud.ParteID,
		Medio:      "simulador",
	}, nil
}

// normalizar acepta el vocabulario canónico y el alias aprobado que trae el contrato del sandbox.
func normalizar(resultado string) (string, error) {
	switch resultado {
	case kyc.ResultadoVerificado, "aprobado":
		return kyc.ResultadoVerificado, nil
	case kyc.ResultadoRechazado:
		return kyc.ResultadoRechazado, nil
	case kyc.ResultadoPendiente:
		return kyc.ResultadoPendiente, nil
	default:
		return "", fmt.Errorf("kycsim: KYC_SIMULADOR_RESULTADO debe ser verificado, rechazado o pendiente, se recibió %q", resultado)
	}
}

var _ kyc.ProveedorKYC = (*Simulador)(nil)
