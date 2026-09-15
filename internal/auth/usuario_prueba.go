package auth

import (
	"errors"
	"net/http"
	"strings"
)

// CabeceraUsuarioDePrueba es la señal que identifica al usuario de prueba en una petición. La
// fase 0 no tiene JWT real: este adaptador reemplaza la validación del token por la sola
// presencia de la cabecera y siempre responde el mismo usuario. Cuando exista el JWT real se
// cambia el adaptador que main inyecta en el router, sin tocar el grupo autenticado ni los
// handlers.
const CabeceraUsuarioDePrueba = "X-Test-User"

// UsuarioDePruebaID es el usuario_id fijo que responde el adaptador. Es un UUID válido porque
// users.id es uuid, y su fila se siembra aparte de las migraciones y solo en el entorno de
// desarrollo: una migración la replicaría en todos los entornos, incluidos staging y main, y
// esta identidad no debe ser un dato de producción.
const UsuarioDePruebaID = "00000000-0000-4000-8000-000000000001"

// ErrSinIdentidad lo devuelve UsuarioDePrueba cuando la petición no trae la cabecera de
// prueba. RequireAuth lo traduce a un 401 dentro del envelope.
var ErrSinIdentidad = errors.New("auth: la petición no trae la identidad de prueba")

// UsuarioDePrueba es el adaptador de identidad de la fase 0: no valida ningún token y responde
// UsuarioDePruebaID ante la presencia de la cabecera X-Test-User. Es un relleno deliberado, no
// autenticación: no debe desplegarse a producción.
type UsuarioDePrueba struct{}

// UsuarioID responde el usuario de prueba cuando la petición trae la cabecera X-Test-User con
// un valor no vacío, y ErrSinIdentidad en cualquier otro caso, para fallar cerrado.
func (UsuarioDePrueba) UsuarioID(r *http.Request) (string, error) {
	if strings.TrimSpace(r.Header.Get(CabeceraUsuarioDePrueba)) == "" {
		return "", ErrSinIdentidad
	}
	return UsuarioDePruebaID, nil
}
