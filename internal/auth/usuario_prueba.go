package auth

import (
	"errors"
	"net/http"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

// CabeceraUsuarioDePrueba es la señal que identifica al usuario de prueba en la fase 0, que main reemplaza por el JWT real.
const CabeceraUsuarioDePrueba = "X-Test-User"

// UsuarioDePruebaID es el usuario fijo del adaptador, un UUID válido que se siembra solo en desarrollo.
const UsuarioDePruebaID = "00000000-0000-4000-8000-000000000001"

// UsuarioDePruebaSesionID es el sid fijo de la identidad de prueba, distinto del usuario.
const UsuarioDePruebaSesionID = "00000000-0000-4000-8000-000000000002"

// ErrSinIdentidad lo devuelve UsuarioDePrueba cuando falta la cabecera de prueba y RequireAuth lo traduce a 401.
var ErrSinIdentidad = errors.New("auth: la petición no trae la identidad de prueba")

// UsuarioDePrueba es el adaptador de la fase 0, sin validar token y sin desplegarse a producción.
type UsuarioDePrueba struct{}

// Identidad responde la identidad de prueba si la cabecera no está vacía y ErrSinIdentidad en cualquier otro caso.
func (UsuarioDePrueba) Identidad(r *http.Request) (identidad.Identidad, error) {
	if strings.TrimSpace(r.Header.Get(CabeceraUsuarioDePrueba)) == "" {
		return identidad.Identidad{}, ErrSinIdentidad
	}
	return identidad.Identidad{
		UsuarioID: UsuarioDePruebaID,
		SesionID:  UsuarioDePruebaSesionID,
		ParteID:   UsuarioDePruebaID,
		Contexto:  "usuario",
	}, nil
}
