package copilot

import (
	"context"
	"encoding/json"
)

// ProveedorIA genera un único turno. Nunca ejecuta las funciones que propone.
// Sus implementaciones deben respetar la cancelación y no registrar contenido.
type ProveedorIA interface {
	Generar(context.Context, SolicitudIA) (RespuestaIA, error)
}

// SolicitudIA contiene únicamente los datos que el orquestador autoriza a enviar.
type SolicitudIA struct {
	// SoloTranscripcion exige salida JSON sin herramientas; sólo lo fija el servidor.
	SoloTranscripcion bool
	Instrucciones     string
	Historial         []TurnoIA
	Funciones         []FuncionIA
}

// FuncionIA declara una herramienta; Parametros es un JSON Schema de tipo object.
type FuncionIA struct {
	Nombre      string
	Descripcion string
	Parametros  json.RawMessage
}

// TurnoIA conserva el orden de las partes, incluidas llamadas paralelas.
// Rol admite usuario, asistente y herramienta (resultados de funciones).
type TurnoIA struct {
	Rol    string
	Partes []ParteIA
}

// ParteIA representa texto, audio, una llamada o su resultado (uno por parte).
// Firma es metadata opaca: se devuelve intacta al proveedor, nunca al usuario.
type ParteIA struct {
	Texto     string
	Audio     *AudioIA
	Llamada   *LlamadaIA
	Resultado *ResultadoIA
	Firma     []byte
}

// AudioIA transporta bytes en memoria, sin URLs ni descargas implícitas.
type AudioIA struct {
	MIME  string
	Datos []byte
}

// LlamadaIA conserva el identificador original cuando el proveedor lo emite.
// Los importes deben ser strings de centavos, nunca números JSON flotantes.
type LlamadaIA struct {
	ID         string
	Nombre     string
	Argumentos json.RawMessage
}

// ResultadoIA correlaciona un resultado con la llamada que lo originó.
type ResultadoIA struct {
	ID     string
	Nombre string
	Datos  json.RawMessage
}

// RespuestaIA contiene un turno reutilizable y consumo reportado por el proveedor.
type RespuestaIA struct {
	Turno         TurnoIA
	TokensEntrada int32
	TokensSalida  int32
}

// ErrorProveedor es una categoría estable sin mensajes ni cuerpos del SDK.
// Permite errors.Is incluso a través de wrappers del orquestador.
type ErrorProveedor string

func (e ErrorProveedor) Error() string { return string(e) }

// Categorías públicas de errores del proveedor, sin detalles sensibles del SDK.
const (
	ErrCredencialesIA ErrorProveedor = "credenciales del proveedor IA inválidas" //nolint:gosec // categoría pública de error, no una credencial
	ErrSolicitudIA    ErrorProveedor = "solicitud al proveedor IA inválida"
	ErrBloqueoIA      ErrorProveedor = "respuesta bloqueada por seguridad"
	ErrLimiteIA       ErrorProveedor = "límite del proveedor IA alcanzado"
	ErrTimeoutIA      ErrorProveedor = "tiempo del proveedor IA agotado"
	ErrNoDisponibleIA ErrorProveedor = "proveedor IA no disponible"
	ErrRespuestaIA    ErrorProveedor = "respuesta del proveedor IA inválida o incompleta"
	ErrGuionAgotadoIA ErrorProveedor = "guion del proveedor simulado agotado"
)
