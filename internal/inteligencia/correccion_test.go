package inteligencia

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCorregirMovimientoValidaAntesDeLaBase(t *testing.T) {
	t.Parallel()

	servicio := NuevoServicio(nil)
	transaccion := uuid.New()
	nombreVacio := "   "
	nombreLargo := strings.Repeat("a", LargoNombreComercio+1)
	sinCategoria := SinCategoria

	casos := map[string]struct {
		solicitud SolicitudDeCorreccion
		esperado  error
	}{
		"sin campos": {solicitud: SolicitudDeCorreccion{}, esperado: ErrCorreccionVacia},
		"nombre vacío": {
			solicitud: SolicitudDeCorreccion{NombreComercio: &nombreVacio},
			esperado:  ErrNombreVacio,
		},
		"nombre largo": {
			solicitud: SolicitudDeCorreccion{NombreComercio: &nombreLargo},
			esperado:  ErrNombreLargo,
		},
		"categoría no visible": {
			solicitud: SolicitudDeCorreccion{Categoria: &sinCategoria},
			esperado:  ErrCategoriaNoVisible,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			_, err := servicio.CorregirMovimiento(t.Context(), "usuario", transaccion, caso.solicitud)
			if !errors.Is(err, caso.esperado) {
				t.Fatalf("error = %v, want %v", err, caso.esperado)
			}
		})
	}
}
