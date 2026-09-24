package httpserver

import (
	"context"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// IniciadorDeSesion es el servicio que autentica por celular y emite el par de tokens.
type IniciadorDeSesion interface {
	IniciarSesion(ctx context.Context, celular, contrasena, contexto, origen, dispositivo string) (auth.SesionEmitida, error)
}

// HandlerDeLogin atiende el inicio de sesión público.
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

// Login valida el cuerpo, autentica por celular y responde el par de tokens con el usuario.
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
	resultado, err := h.iniciador.IniciarSesion(r.Context(), entrada.Celular, entrada.Contrasena, contexto, origen, dispositivoDeLaPeticion(r))
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, respuestaDeSesion(resultado), nil)
}
