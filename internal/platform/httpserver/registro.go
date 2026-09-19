package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// RegistradorDePersonas es el servicio de alta de persona y empresa que el handler consume.
type RegistradorDePersonas interface {
	RegistrarPersona(ctx context.Context, solicitud auth.SolicitudDeRegistro) (auth.Registro, error)
	RegistrarEmpresa(ctx context.Context, solicitud auth.SolicitudDeRegistroEmpresa) (auth.RegistroEmpresa, error)
}

// HandlerDeRegistro atiende el alta pública de persona y de empresa.
type HandlerDeRegistro struct{ registrador RegistradorDePersonas }

// NuevoRegistroHandler construye el handler sobre el servicio de registro.
func NuevoRegistroHandler(registrador RegistradorDePersonas) *HandlerDeRegistro {
	return &HandlerDeRegistro{registrador: registrador}
}

// registroDePersona elige el handler real o el stub 501 cuando no está inyectado.
func registroDePersona(handler RegistroHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.Register
}

// Register decodifica por tipo_cuenta y despacha el alta de persona o de empresa.
func (h *HandlerDeRegistro) Register(w http.ResponseWriter, r *http.Request) {
	decodificado, ok := api.DecodeDiscriminated(w, r, "tipo_cuenta", map[string]any{
		"persona": &api.RegisterPersonaRequest{},
		"empresa": &api.RegisterEmpresaRequest{},
	})
	if !ok {
		return
	}
	switch entrada := decodificado.(type) {
	case *api.RegisterPersonaRequest:
		h.registrarPersona(w, r, entrada)
	case *api.RegisterEmpresaRequest:
		h.registrarEmpresa(w, r, entrada)
	default:
		sinImplementar(w, r)
	}
}

// registrarPersona llama al servicio y responde 201 con la cuenta pendiente, nunca con tokens.
func (h *HandlerDeRegistro) registrarPersona(w http.ResponseWriter, r *http.Request, entrada *api.RegisterPersonaRequest) {
	origen, _ := clientIPKey(r)
	registro, err := h.registrador.RegistrarPersona(r.Context(), auth.SolicitudDeRegistro{
		Correo:          entrada.Correo,
		Contrasena:      entrada.Contrasena,
		Celular:         entrada.Celular,
		NombreCompleto:  entrada.NombreCompleto,
		TipoDocumento:   string(entrada.TipoDocumento),
		NumeroDocumento: entrada.NumeroDocumento,
		Consentimientos: convertirConsentimientos(entrada.Consentimientos),
		Origen:          origen,
		UserAgent:       r.UserAgent(),
	})
	if err != nil {
		escribirErrorDeRegistro(w, r, err)
		return
	}
	api.WriteData(w, http.StatusCreated, respuestaDeRegistro(registro), nil)
}

// registrarEmpresa llama al servicio y responde 201 con la empresa pendiente, nunca con tokens.
func (h *HandlerDeRegistro) registrarEmpresa(w http.ResponseWriter, r *http.Request, entrada *api.RegisterEmpresaRequest) {
	origen, _ := clientIPKey(r)
	registro, err := h.registrador.RegistrarEmpresa(r.Context(), auth.SolicitudDeRegistroEmpresa{
		RazonSocial:                  entrada.RazonSocial,
		NIT:                          entrada.Nit,
		Correo:                       entrada.Correo,
		Contrasena:                   entrada.Contrasena,
		Celular:                      entrada.Celular,
		RepresentanteNombre:          entrada.RepresentanteLegal.NombreCompleto,
		RepresentanteTipoDocumento:   string(entrada.RepresentanteLegal.TipoDocumento),
		RepresentanteNumeroDocumento: entrada.RepresentanteLegal.NumeroDocumento,
		Consentimientos:              convertirConsentimientos(entrada.Consentimientos),
		Origen:                       origen,
		UserAgent:                    r.UserAgent(),
	})
	if err != nil {
		escribirErrorDeRegistroEmpresa(w, r, err)
		return
	}
	api.WriteData(w, http.StatusCreated, respuestaDeRegistroEmpresa(registro), nil)
}

// convertirConsentimientos traduce el contrato al vocabulario del servicio sin perder el nulo.
func convertirConsentimientos(entrantes []api.Consentimiento) []auth.SolicitudDeConsentimiento {
	resultado := make([]auth.SolicitudDeConsentimiento, 0, len(entrantes))
	for _, consentimiento := range entrantes {
		resultado = append(resultado, auth.SolicitudDeConsentimiento{
			Tipo:         string(consentimiento.Tipo),
			VersionTexto: consentimiento.VersionTexto,
			Aceptado:     consentimiento.Aceptado != nil && *consentimiento.Aceptado,
		})
	}
	return resultado
}

// respuestaDeRegistro arma la respuesta 201 sin contraseña, valor derivado ni tokens.
func respuestaDeRegistro(registro auth.Registro) api.Registro {
	nombre := registro.NombreCompleto
	return api.Registro{
		Id:             openapi_types.UUID(registro.ParteID),
		TipoCuenta:     api.RegistroTipoCuentaPersona,
		Correo:         registro.Correo,
		Celular:        registro.Celular,
		Estado:         api.RegistroEstado(registro.Estado),
		FechaCreacion:  registro.FechaCreacion,
		NombreCompleto: &nombre,
		Desafio: api.Desafio{
			ChallengeId:      openapi_types.UUID(registro.Desafio.ID),
			ExpiraEnSegundos: registro.Desafio.ExpiraEnSegundos,
			Proposito:        api.DesafioProposito(registro.Desafio.Proposito),
		},
	}
}

// respuestaDeRegistroEmpresa arma la respuesta 201 con la razón social, el NIT y el empresa_id, sin secretos.
func respuestaDeRegistroEmpresa(registro auth.RegistroEmpresa) api.Registro {
	razonSocial, nit := registro.RazonSocial, registro.NIT
	empresaID := openapi_types.UUID(registro.EmpresaParteID)
	return api.Registro{
		Id:            openapi_types.UUID(registro.RepresentanteParteID),
		EmpresaId:     &empresaID,
		TipoCuenta:    api.RegistroTipoCuentaEmpresa,
		Correo:        registro.Correo,
		Celular:       registro.Celular,
		Estado:        api.RegistroEstado(registro.Estado),
		FechaCreacion: registro.FechaCreacion,
		RazonSocial:   &razonSocial,
		Nit:           &nit,
		Desafio: api.Desafio{
			ChallengeId:      openapi_types.UUID(registro.Desafio.ID),
			ExpiraEnSegundos: registro.Desafio.ExpiraEnSegundos,
			Proposito:        api.DesafioProposito(registro.Desafio.Proposito),
		},
	}
}

// escribirErrorDeRegistroEmpresa nombra el campo anidado del representante y delega el resto en auth.
func escribirErrorDeRegistroEmpresa(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrNombreInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "representante_legal.nombre_completo", Rule: "nombre"}})
	case errors.Is(err, auth.ErrCelularInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "celular", Rule: "e164"}})
	case errors.Is(err, auth.ErrNitInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nit", Rule: "nit"}})
	case errors.Is(err, auth.ErrRazonSocialInvalida):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "razon_social", Rule: "max"}})
	default:
		escribirErrorDeAuth(w, r, err)
	}
}

// escribirErrorDeRegistro añade los details de campo de la validación y delega el resto en auth.
func escribirErrorDeRegistro(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrNombreInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "nombre_completo", Rule: "nombre"}})
	case errors.Is(err, auth.ErrCelularInvalido):
		api.WriteError(w, http.StatusUnprocessableEntity, "ValidationError", "Request validation failed.",
			[]api.FieldError{{Field: "celular", Rule: "e164"}})
	default:
		escribirErrorDeAuth(w, r, err)
	}
}
