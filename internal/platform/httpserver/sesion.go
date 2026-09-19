package httpserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// ServicioDeSesion verifica el segundo factor y rota el refresh.
type ServicioDeSesion interface {
	VerificarOtp(ctx context.Context, id uuid.UUID, codigo, dispositivo string) (auth.SesionEmitida, error)
	RotarRefresh(ctx context.Context, refreshEnClaro string) (auth.TokensRotados, error)
}

// HandlerDeSesion atiende la verificación del OTP y la rotación del refresh.
type HandlerDeSesion struct{ servicio ServicioDeSesion }

// NuevoSesionHandler construye el handler sobre el servicio de sesión.
func NuevoSesionHandler(servicio ServicioDeSesion) *HandlerDeSesion {
	return &HandlerDeSesion{servicio: servicio}
}

// verificacionDeOtp elige el handler real o el stub 501 cuando no está inyectado.
func verificacionDeOtp(handler VerificarOtpHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.VerifyOtp
}

// rotacionDeRefresh elige el handler real o el stub 501 cuando no está inyectado.
func rotacionDeRefresh(handler RefreshHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.Refresh
}

// VerifyOtp valida el cuerpo, verifica el desafío de login y responde el par con el usuario.
func (h *HandlerDeSesion) VerifyOtp(w http.ResponseWriter, r *http.Request) {
	var entrada api.VerifyOtpRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	resultado, err := h.servicio.VerificarOtp(r.Context(), entrada.ChallengeId, entrada.Codigo, dispositivoDeLaPeticion(r))
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, respuestaDeSesion(resultado), nil)
}

// Refresh valida el cuerpo, rota el refresh y responde el par nuevo.
func (h *HandlerDeSesion) Refresh(w http.ResponseWriter, r *http.Request) {
	var entrada api.RefreshRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	tokens, err := h.servicio.RotarRefresh(r.Context(), entrada.RefreshToken)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, api.Tokens{AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken}, nil)
}

// dispositivoDeLaPeticion recorta el User-Agent a 120 runas para el listado de dispositivos.
func dispositivoDeLaPeticion(r *http.Request) string {
	runas := []rune(strings.TrimSpace(r.UserAgent()))
	if len(runas) > 120 {
		runas = runas[:120]
	}
	return string(runas)
}

// respuestaDeSesion traduce la sesión de dominio al contrato con el rol nulo en contexto usuario.
func respuestaDeSesion(sesion auth.SesionEmitida) api.Sesion {
	usuario := api.Usuario{
		Id:             openapi_types.UUID(sesion.Usuario.ID),
		TipoCuenta:     api.UsuarioTipoCuenta(sesion.Usuario.TipoCuenta),
		Correo:         sesion.Usuario.Correo,
		ContextoActivo: sesion.Usuario.ContextoActivo,
	}
	if sesion.Usuario.NombreCompleto != "" {
		usuario.NombreCompleto = &sesion.Usuario.NombreCompleto
	}
	if sesion.Usuario.RazonSocial != "" {
		usuario.RazonSocial = &sesion.Usuario.RazonSocial
	}
	if sesion.Usuario.RolActivo != "" {
		usuario.RolActivo = &sesion.Usuario.RolActivo
	}
	return api.Sesion{AccessToken: sesion.AccessToken, RefreshToken: sesion.RefreshToken, Usuario: usuario}
}
