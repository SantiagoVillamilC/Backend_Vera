package nucleo

import "context"

// Campo es un par clave/valor del contexto de un error capturado.
type Campo struct {
	Clave string
	Valor string
}

// ContextoError identifica una falla para el rastreador: la operación que falló y los campos del
// dominio que la ubican (descuadre_id, intencion_id, tipo, montos, ventana...). El request_id no
// va acá: el logger lo estampa desde el contexto de la petición.
type ContextoError struct {
	Operacion string
	Campos    []Campo
}

// RastreadorErrores es el puerto al rastreador de errores del proyecto. Lo declara el núcleo,
// main le inyecta la implementación. Capturar no devuelve error, así que no interrumpe el flujo
// que escala; el error reportado debe ser un centinela estable y los datos variables van en Campos.
type RastreadorErrores interface {
	Capturar(ctx context.Context, err error, contexto ContextoError)
}
