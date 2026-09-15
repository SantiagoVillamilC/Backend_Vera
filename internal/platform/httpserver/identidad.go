package httpserver

import "net/http"

// IdentidadDeLaPeticion responde quién es el usuario autenticado de una petición.
//
// La declara el grupo autenticado, que es quien la consume, y main inyecta la implementación:
// es el patrón de LectorNivelKYC del capítulo 5 de la frontera (la declara el consumidor, la
// implementa el módulo dueño de la identidad). Hoy la implementa auth.UsuarioDePrueba, sin JWT
// real; cuando exista el JWT, main inyecta otro adaptador y ni el grupo autenticado ni los
// handlers cambian.
type IdentidadDeLaPeticion interface {
	UsuarioID(r *http.Request) (string, error)
}
