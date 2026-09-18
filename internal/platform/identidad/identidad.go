// Package identidad lleva el usuario autenticado de una petición en el contexto, en un paquete hoja para evitar el ciclo httpserver, handler, httpserver.
package identidad

import "context"

// clave es el tipo propio no exportado de la clave del contexto, nunca un string, para evitar colisiones y lecturas ajenas (staticcheck SA1029).
type clave struct{}

// Identidad es quién es la petición autenticada y con qué contexto y rol opera.
type Identidad struct {
	UsuarioID string
	SesionID  string
	Contexto  string
	Rol       string
}

// ConIdentidad devuelve un contexto con la identidad completa, lo llama RequireAuth al resolverla.
func ConIdentidad(ctx context.Context, id Identidad) context.Context {
	return context.WithValue(ctx, clave{}, id)
}

// IdentidadDeContexto devuelve la identidad del contexto, con un segundo valor false si no hay ninguna.
func IdentidadDeContexto(ctx context.Context) (Identidad, bool) {
	id, ok := ctx.Value(clave{}).(Identidad)
	return id, ok
}

// ConUsuarioID conserva el seam viejo como wrapper sobre ConIdentidad.
func ConUsuarioID(ctx context.Context, usuarioID string) context.Context {
	return ConIdentidad(ctx, Identidad{UsuarioID: usuarioID})
}

// UsuarioIDDeContexto es un wrapper del accesor nuevo para no tocar a los handlers del núcleo.
func UsuarioIDDeContexto(ctx context.Context) (string, bool) {
	id, ok := IdentidadDeContexto(ctx)
	return id.UsuarioID, ok
}
