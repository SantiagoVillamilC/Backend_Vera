package httpserver

import (
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// IdentidadDeLaPeticion responde quién es el usuario autenticado, la declara el grupo autenticado y main inyecta el adaptador (hoy auth.UsuarioDePrueba, luego el JWT).
type IdentidadDeLaPeticion interface {
	Identidad(r *http.Request) (identidad.Identidad, error)
}
