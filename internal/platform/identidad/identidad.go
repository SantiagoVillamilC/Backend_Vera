// Package identidad lleva el usuario autenticado de una petición en el contexto.
//
// Vive en un paquete hoja, aparte de internal/platform/httpserver, porque httpserver importa
// los handlers (auth, copilot, receipts) y esos handlers leen el usuario_id desde el contexto:
// si el accesor viviera en httpserver, el import handler -> httpserver cerraría el ciclo
// httpserver -> handler -> httpserver. Es la misma razón por la que el envelope vive en
// internal/platform/api.
package identidad

import "context"

// clave es el tipo de la clave del contexto. Es un tipo propio no exportado, nunca un string,
// para que ningún otro paquete pueda colisionar con la clave ni leer el usuario_id por otra
// vía (staticcheck SA1029).
type clave struct{}

// ConUsuarioID devuelve un contexto con el usuario autenticado de la petición. Lo llama
// RequireAuth al resolver la identidad; ningún otro lugar debería escribirlo.
func ConUsuarioID(ctx context.Context, usuarioID string) context.Context {
	return context.WithValue(ctx, clave{}, usuarioID)
}

// UsuarioIDDeContexto devuelve el usuario autenticado que RequireAuth dejó en el contexto. El
// segundo valor es false cuando no hay ninguno, por ejemplo en una ruta que no pasó por el
// grupo autenticado.
func UsuarioIDDeContexto(ctx context.Context) (string, bool) {
	usuarioID, ok := ctx.Value(clave{}).(string)
	return usuarioID, ok
}
