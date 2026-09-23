// Package kyc es el módulo de KYC de auth: el nivel de producto que el núcleo consulta y la verificación del cliente.
package kyc

import (
	"context"
	"errors"
)

// NivelBajoMonto y NivelPleno son los dos niveles de verificación que el cliente puede pedir.
const (
	NivelBajoMonto = "bajo_monto"
	NivelPleno     = "pleno"
)

// Los tres productos que admite la tabla de estados.
const (
	ProductoBajoMonto         = "bajo_monto"
	ProductoPleno             = "pleno"
	ProductoDepositoOrdinario = "deposito_ordinario"
)

// El vocabulario de estado_kyc que esta unidad fija, libre en el contrato.
const (
	EstadoBasico         = "basico"
	EstadoPendientePleno = "pendiente_pleno"
	EstadoPleno          = "pleno"
	EstadoRechazado      = "rechazado"
)

// Los dos tipos de parte que deciden la derivación perezosa.
const (
	TipoPersona = "persona"
	TipoEmpresa = "empresa"
)

// MedioRegistro es el medio de la evidencia de bajo monto: los datos mínimos ya los capturó el registro.
const MedioRegistro = "registro"

// Los dos resultados que puede devolver el proveedor de KYC.
const (
	ResultadoVerificado = "verificado"
	ResultadoRechazado  = "rechazado"
	ResultadoPendiente  = "pendiente"
)

// ErrTipoDeParteDesconocido reporta un tipo de parte fuera del modelo.
var ErrTipoDeParteDesconocido = errors.New("kyc: el tipo de la parte no existe en el modelo")

// ErrNivelNoAplicaEmpresa reporta bajo monto para una empresa: es solo de personas naturales (H5).
var ErrNivelNoAplicaEmpresa = errors.New("kyc: el bajo monto es solo para personas naturales")

// ErrNivelNoSoportado reporta un nivel que no es de verificación.
var ErrNivelNoSoportado = errors.New("kyc: el nivel no es verificable")

// Solicitud es lo que el módulo le pide al proveedor de KYC.
type Solicitud struct {
	ParteID   string
	Nivel     string
	Proveedor string
}

// Resultado es lo que el proveedor de KYC devuelve.
type Resultado struct {
	Estado     string
	Referencia string
	Medio      string
}

// ProveedorKYC es el puerto del proveedor externo; el adaptador real queda fuera del harness.
type ProveedorKYC interface {
	Verificar(ctx context.Context, solicitud Solicitud) (Resultado, error)
}

// AbridorDeSubcuentas es el método público del núcleo que la materialización de empresa dispara.
type AbridorDeSubcuentas interface {
	AbrirSubcuentasDeUsuario(ctx context.Context, usuarioID string) (int64, error)
}

// Estado es el producto y el estado de KYC que se responden.
type Estado struct {
	Producto  string
	EstadoKYC string
}
