package httpserver

import (
	"context"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// IniciadorDeSesion es el servicio que resuelve el login y crea el desafío del segundo factor.
type IniciadorDeSesion interface {
	IniciarSesion(ctx context.Context, correo, contrasena, contexto, origen string) (auth.LoginData, error)
}

// HandlerDeLogin atiende el desenlace público del inicio de sesión.
type HandlerDeLogin struct{ iniciador IniciadorDeSesion }

// NuevoLoginHandler construye el handler sobre el servicio de login.
func NuevoLoginHandler(iniciador IniciadorDeSesion) *HandlerDeLogin {
	return &HandlerDeLogin{iniciador: iniciador}
}

// loginDeSesion elige el handler real o el stub 501 cuando no está inyectado.
func loginDeSesion(handler LoginHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.Login
}

// Login valida el cuerpo, inicia la sesión y responde el desafío sin tokens.
func (h *HandlerDeLogin) Login(w http.ResponseWriter, r *http.Request) {
	var entrada api.LoginRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	origen, _ := clientIPKey(r)
	contexto := string(api.LoginRequestContextoUsuario)
	if entrada.Contexto != nil {
		contexto = string(*entrada.Contexto)
	}
	resultado, err := h.iniciador.IniciarSesion(r.Context(), entrada.Correo, entrada.Contrasena, contexto, origen)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, respuestaDeLogin(resultado), nil)
}

// respuestaDeLogin traduce el desenlace del servicio al contrato, sin access ni refresh.
func respuestaDeLogin(resultado auth.LoginData) api.LoginData {
	return api.LoginData{
		ChallengeId:           openapi_types.UUID(resultado.ChallengeID),
		ExpiraEnSegundos:      resultado.ExpiraEnSegundos,
		Mensaje:               resultado.Mensaje,
		Proposito:             api.LoginDataProposito(resultado.Proposito),
		RequiereSegundoFactor: api.LoginDataRequiereSegundoFactor(resultado.RequiereSegundoFactor),
	}
}
