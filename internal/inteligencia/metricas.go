package inteligencia

import "time"

// DesenlaceDeLote clasifica el final de una corrida del lote de enriquecimiento.
type DesenlaceDeLote string

// Desenlaces del lote. La omisión por candado no es un error: es un lote que no corrió.
const (
	DesenlaceLoteOK      DesenlaceDeLote = "ok"
	DesenlaceLoteError   DesenlaceDeLote = "error"
	DesenlaceLoteEnCurso DesenlaceDeLote = "lote_en_curso"
)

// DesenlaceDeCorreccion clasifica lo que respondió el endpoint de corrección.
type DesenlaceDeCorreccion string

// Desenlaces de la corrección. Se derivan del mismo punto que elige el código HTTP.
const (
	DesenlaceCorreccionAplicada     DesenlaceDeCorreccion = "aplicada"
	DesenlaceCorreccionNoEncontrada DesenlaceDeCorreccion = "no_encontrada"
	DesenlaceCorreccionInvalida     DesenlaceDeCorreccion = "invalida"
	DesenlaceCorreccionConflicto    DesenlaceDeCorreccion = "conflicto"
	DesenlaceCorreccionError        DesenlaceDeCorreccion = "error"
)

// MotivoDeConflicto separa los dos 409 de la corrección, que significan cosas distintas.
type MotivoDeConflicto string

// Motivos de conflicto de la corrección.
const (
	ConflictoCorreccionEnCurso   MotivoDeConflicto = "correccion_en_curso"
	ConflictoCopiaSinReconstruir MotivoDeConflicto = "copia_sin_reconstruir"
)

// ResumenDeAvisos es lo que una corrida de avisos produjo, ya con todas las transacciones confirmadas.
// Las duraciones por usuario se miden alrededor de cada transacción y son tiempo, no estado.
type ResumenDeAvisos struct {
	Usuarios             int
	Emitidos             int
	YaEmitidos           int
	SuprimidosPorCupo    int
	DuracionesPorUsuario []time.Duration
	Fallo                bool
}

// Metricas es el puerto de observabilidad del contexto de inteligencia. La implementación concreta
// vive fuera del paquete: acá solo se declara qué se mide y cuándo, para que el dominio no dependa
// de una biblioteca de métricas. Todo incremento ocurre después de que la transacción confirmó.
type Metricas interface {
	ClasificadorActivo(activo bool)
	LoteTerminado(resultado Resultado, duracion time.Duration)
	LoteNoEjecutado(desenlace DesenlaceDeLote, duracion time.Duration)
	AvisosTerminados(resumen ResumenDeAvisos, duracion time.Duration)
	CorreccionTerminada(desenlace DesenlaceDeCorreccion, filasCambiadas int)
	ConflictoDeCorreccion(motivo MotivoDeConflicto)
}

// sinMetricas es el relleno del puerto: no mide nada y nunca falla.
type sinMetricas struct{}

// SinMetricas devuelve el relleno del puerto para cuando las métricas están apagadas.
func SinMetricas() Metricas { return sinMetricas{} }

// ClasificadorActivo no hace nada.
func (sinMetricas) ClasificadorActivo(bool) {}

// LoteTerminado no hace nada.
func (sinMetricas) LoteTerminado(Resultado, time.Duration) {}

// LoteNoEjecutado no hace nada.
func (sinMetricas) LoteNoEjecutado(DesenlaceDeLote, time.Duration) {}

// AvisosTerminados no hace nada.
func (sinMetricas) AvisosTerminados(ResumenDeAvisos, time.Duration) {}

// CorreccionTerminada no hace nada.
func (sinMetricas) CorreccionTerminada(DesenlaceDeCorreccion, int) {}

// ConflictoDeCorreccion no hace nada.
func (sinMetricas) ConflictoDeCorreccion(MotivoDeConflicto) {}

var _ Metricas = sinMetricas{}
