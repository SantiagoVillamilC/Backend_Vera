package copilot

// MotivoBloqueo identifica reglas conocidas sin incluir contenido del proveedor.
type MotivoBloqueo string

// Motivos de diagnóstico seguros. Los valores del SDK se traducen a esta lista cerrada.
const (
	BloqueoEntrada       MotivoBloqueo = "ProviderInput"
	BloqueoSeguridad     MotivoBloqueo = "ProviderSafety"
	BloqueoRecitacion    MotivoBloqueo = "ProviderRecitation"
	BloqueoDatos         MotivoBloqueo = "ProviderSensitiveData"
	BloqueoOtro          MotivoBloqueo = "ProviderOther"
	BloqueoInstrucciones MotivoBloqueo = "InstructionDisclosure"
)

// ErrorBloqueo conserva la categoría pública y añade únicamente un motivo conocido.
// Error no serializa el motivo ni mensajes del SDK.
type ErrorBloqueo struct{ Motivo MotivoBloqueo }

func (*ErrorBloqueo) Error() string { return string(ErrBloqueoIA) }

// Unwrap mantiene compatibles las comprobaciones errors.Is de los servicios.
func (*ErrorBloqueo) Unwrap() error { return ErrBloqueoIA }
