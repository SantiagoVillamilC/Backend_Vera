package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// RegistradorDePersonas es el servicio de alta de persona que el handler consume.
type RegistradorDePersonas interface {
	RegistrarPersona(ctx context.Context, solicitud auth.SolicitudDeRegistro) (auth.Registro, error)
}

// HandlerDeRegistro atiende el alta pública de persona y el 501 de la rama empresa.
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

// Register decodifica por tipo_cuenta: la rama persona sigue y la empresa responde 501.
func (h *HandlerDeRegistro) Register(w http.ResponseWriter, r *http.Request) {
	decodificado, ok := api.DecodeDiscriminated(w, r, "tipo_cuenta", map[string]any{
		"persona": &api.RegisterPersonaRequest{},
		"empresa": &api.RegisterEmpresaRequest{},
	})
	if !ok {
		return
	}
	persona, ok := decodificado.(*api.RegisterPersonaRequest)
	if !ok {
		sinImplementar(w, r)
		return
	}
	h.registrarPersona(w, r, persona)
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
