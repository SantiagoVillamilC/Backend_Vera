package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

// creadorDeEmpresasFalso devuelve el uuid o el error configurados sin tocar la base.
type creadorDeEmpresasFalso struct {
	empresaID uuid.UUID
	err       error
	llamadas  int
	parteID   uuid.UUID
	razon     string
	nit       string
}

// CrearEmpresaParaParte registra los argumentos y devuelve el resultado configurado.
func (c *creadorDeEmpresasFalso) CrearEmpresaParaParte(_ context.Context, parteID uuid.UUID, razonSocial, nit string) (uuid.UUID, error) {
	c.llamadas++
	c.parteID, c.razon, c.nit = parteID, razonSocial, nit
	if c.err != nil {
		return uuid.Nil, c.err
	}
	return c.empresaID, nil
}

// identidadDeContextoFijo responde la identidad configurada sin validar token.
type identidadDeContextoFijo struct{ id identidad.Identidad }

// Identidad devuelve la identidad fija con la parte y el contexto pedidos.
func (i identidadDeContextoFijo) Identidad(*http.Request) (identidad.Identidad, error) {
	return i.id, nil
}

// routerConEmpresas monta el router completo con el handler de empresa y la identidad inyectados.
func routerConEmpresas(identidadPeticion IdentidadDeLaPeticion, creador CreadorDeEmpresasParaParte) http.Handler {
	var handler CompaniesHandler
	if creador != nil {
		handler = NuevoEmpresasHandler(creador)
	}
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: identidadPeticion,
		Companies: handler,
	})
}

func TestCreateCompanyDesenlaces(t *testing.T) {
	parte := uuid.New()
	identidadPersona := identidadDeContextoFijo{id: identidad.Identidad{
		UsuarioID: parte.String(), ParteID: parte.String(), Contexto: auth.ContextoUsuario,
	}}
	identidadEmpresa := identidadDeContextoFijo{id: identidad.Identidad{
		UsuarioID: uuid.NewString(), ParteID: parte.String(), Contexto: auth.ContextoEmpresa,
	}}

	casos := map[string]struct {
		identidad    IdentidadDeLaPeticion
		cuerpo       string
		err          error
		estado       int
		codigo       string
		campoDetalle string
		reglaDetalle string
	}{
		"sin credencial": {
			identidad: identidadDeContextoFijo{},
			cuerpo:    `{"razon_social":"Centinela SAS","nit":"9001234568"}`,
			estado:    http.StatusUnauthorized,
			codigo:    "Unauthorized",
		},
		"sin nit": {
			identidad:    identidadPersona,
			cuerpo:       `{"razon_social":"Centinela SAS"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "nit",
			reglaDetalle: "required",
		},
		"nit con digito malo": {
			identidad:    identidadPersona,
			cuerpo:       `{"razon_social":"Centinela SAS","nit":"9001234569"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "nit",
			reglaDetalle: "nit",
		},
		"razon social vacia": {
			identidad:    identidadPersona,
			cuerpo:       `{"razon_social":"","nit":"9001234568"}`,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "razon_social",
			reglaDetalle: "required",
		},
		"servicio rechaza razon social": {
			identidad:    identidadPersona,
			cuerpo:       `{"razon_social":"Centinela SAS","nit":"9001234568"}`,
			err:          auth.ErrRazonSocialInvalida,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "razon_social",
			reglaDetalle: "max",
		},
		"servicio rechaza nit": {
			identidad:    identidadPersona,
			cuerpo:       `{"razon_social":"Centinela SAS","nit":"9001234568"}`,
			err:          auth.ErrNitInvalido,
			estado:       http.StatusUnprocessableEntity,
			codigo:       "ValidationError",
			campoDetalle: "nit",
			reglaDetalle: "nit",
		},
		"contexto empresa": {
			identidad: identidadEmpresa,
			cuerpo:    `{"razon_social":"Centinela SAS","nit":"9001234568"}`,
			estado:    http.StatusBadRequest,
			codigo:    "ContextInvalid",
		},
		"nit repetido": {
			identidad: identidadPersona,
			cuerpo:    `{"razon_social":"Centinela SAS","nit":"9001234568"}`,
			err:       auth.ErrIdentificadorEnUso,
			estado:    http.StatusConflict,
			codigo:    "DuplicateIdentifier",
		},
		"empresa creada": {
			identidad: identidadPersona,
			cuerpo:    `{"razon_social":"Centinela SAS","nit":"9001234568"}`,
			estado:    http.StatusCreated,
		},
	}

	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			empresaID := uuid.New()
			creador := &creadorDeEmpresasFalso{empresaID: empresaID, err: caso.err}
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/companies", strings.NewReader(caso.cuerpo))
			if caso.identidad != nil {
				req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
			}
			rec := httptest.NewRecorder()

			routerConEmpresas(caso.identidad, creador).ServeHTTP(rec, req)

			if rec.Code != caso.estado {
				t.Fatalf("status = %d, quiero %d (cuerpo %q)", rec.Code, caso.estado, rec.Body.String())
			}
			if caso.codigo != "" {
				assertErrorEnvelope(t, rec, caso.estado, caso.codigo)
			}
			if caso.campoDetalle != "" {
				assertDetalleDeValidacion(t, rec, caso.campoDetalle, caso.reglaDetalle)
			}
			if caso.estado == http.StatusCreated {
				datos := datosDeRespuesta(t, rec)
				if datos["empresa_id"] != empresaID.String() || datos["nit"] != "9001234568" || datos["razon_social"] != "Centinela SAS" {
					t.Fatalf("data = %v, quiero empresa_id, nit y razon_social", datos)
				}
				if creador.parteID != parte || creador.nit != "9001234568" || creador.razon != "Centinela SAS" {
					t.Fatalf("servicio recibió (%s, %q, %q), quiero la parte del contexto y el cuerpo", creador.parteID, creador.razon, creador.nit)
				}
			}
		})
	}
}

func TestCreateCompanySinServicioResponde501(t *testing.T) {
	parte := uuid.New()
	identidadPersona := identidadDeContextoFijo{id: identidad.Identidad{
		UsuarioID: parte.String(), ParteID: parte.String(), Contexto: auth.ContextoUsuario,
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/companies", strings.NewReader(`{"razon_social":"Centinela SAS","nit":"9001234568"}`))
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()

	routerConEmpresas(identidadPersona, nil).ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusNotImplemented, "NotImplemented")
}
