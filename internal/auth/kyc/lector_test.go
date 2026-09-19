package kyc

import (
	"errors"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
)

// HU-AUT-09 / A11: una persona siempre resuelve al único nivel que el núcleo siembra.
func TestNivelKYCPersonaEsBajoMonto(t *testing.T) {
	nivel, err := nivelDeTipo(TipoPersona)
	if err != nil {
		t.Fatalf("nivelDeTipo(persona): %v", err)
	}
	if nivel != nucleo.NivelKYCDepositoBajoMonto {
		t.Fatalf("nivel = %q, quiero la constante del núcleo %q", nivel, nucleo.NivelKYCDepositoBajoMonto)
	}
}

// H5: una empresa no opera hasta KYC pleno y el error entra al envelope como 403 KycRequired.
func TestNivelKYCEmpresaPidePleno(t *testing.T) {
	nivel, err := nivelDeTipo(TipoEmpresa)
	if nivel != "" {
		t.Fatalf("nivel = %q, quiero vacío cuando falta el KYC pleno", nivel)
	}
	if !errors.Is(err, auth.ErrKYCPlenoRequerido) {
		t.Fatalf("err = %v, quiero auth.ErrKYCPlenoRequerido", err)
	}
	var apiErr *platformerrors.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("quiero un *platformerrors.APIError, obtuve %T", err)
	}
	if apiErr.Status != 403 || apiErr.Code != "KycRequired" {
		t.Fatalf("status/code = %d/%q, quiero 403/KycRequired", apiErr.Status, apiErr.Code)
	}
}

// Fail closed: un tipo fuera del modelo no cae en un nivel por defecto.
func TestNivelKYCTipoDesconocidoFallaCerrado(t *testing.T) {
	_, err := nivelDeTipo("fantasma")
	if !errors.Is(err, ErrTipoDeParteDesconocido) {
		t.Fatalf("err = %v, quiero ErrTipoDeParteDesconocido", err)
	}
}

// Criterio 1: sin fila, la derivación por tipo separa persona y empresa.
func TestEstadoPorDefectoDerivaPorTipo(t *testing.T) {
	persona, err := estadoPorDefecto(TipoPersona)
	if err != nil {
		t.Fatalf("estadoPorDefecto(persona): %v", err)
	}
	if persona.Producto != ProductoBajoMonto || persona.EstadoKYC != EstadoBasico {
		t.Fatalf("persona = %+v, quiero bajo_monto/basico", persona)
	}
	empresa, err := estadoPorDefecto(TipoEmpresa)
	if err != nil {
		t.Fatalf("estadoPorDefecto(empresa): %v", err)
	}
	if empresa.Producto != ProductoDepositoOrdinario || empresa.EstadoKYC != EstadoPendientePleno {
		t.Fatalf("empresa = %+v, quiero deposito_ordinario/pendiente_pleno", empresa)
	}
}
