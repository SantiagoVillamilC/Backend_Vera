package httpserver

import (
	"context"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

// VerificadorDeRecuperacion completa el segundo metodo de la recuperacion y entrega el reset_token.
type VerificadorDeRecuperacion interface {
	VerificarSegundoFactor(ctx context.Context, challengeID uuid.UUID, codigo, codigoRespaldo, origen string) (string, error)
}

// RestablecedorDeContrasena fija la contrasena nueva con el reset_token.
type RestablecedorDeContrasena interface {
	RestablecerContrasena(ctx context.Context, resetToken, nuevaContrasena string) error
}

// RecuperacionVerifyHandler atiende la verificacion publica del segundo metodo de recuperacion.
type RecuperacionVerifyHandler interface {
	VerificarSegundoFactor(http.ResponseWriter, *http.Request)
}

// RecuperacionResetHandler atiende el restablecimiento publico de la contrasena.
type RecuperacionResetHandler interface {
	RestablecerContrasena(http.ResponseWriter, *http.Request)
}

// HandlerDeRestablecimiento cierra la recuperacion: segundo metodo y contrasena nueva.
type HandlerDeRestablecimiento struct {
	verificador   VerificadorDeRecuperacion
	restablecedor RestablecedorDeContrasena
}

// NuevoRestablecimientoHandler construye el handler sobre los dos servicios del cierre.
func NuevoRestablecimientoHandler(verificador VerificadorDeRecuperacion, restablecedor RestablecedorDeContrasena) *HandlerDeRestablecimiento {
	return &HandlerDeRestablecimiento{verificador: verificador, restablecedor: restablecedor}
}

// verificacionDeRecuperacion elige el handler real o el stub 501 cuando no esta inyectado.
func verificacionDeRecuperacion(handler RecuperacionVerifyHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.VerificarSegundoFactor
}

// restablecimientoDeContrasena elige el handler real o el stub 501 cuando no esta inyectado.
func restablecimientoDeContrasena(handler RecuperacionResetHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.RestablecerContrasena
}

// VerificarSegundoFactor valida el cuerpo, consume el codigo de respaldo y responde el reset_token.
func (h *HandlerDeRestablecimiento) VerificarSegundoFactor(w http.ResponseWriter, r *http.Request) {
	var entrada api.RecoveryVerifyRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	origen, _ := clientIPKey(r)
	token, err := h.verificador.VerificarSegundoFactor(r.Context(), entrada.ChallengeId, entrada.Codigo, entrada.SegundoMetodo.Codigo, origen)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, api.ResetToken{ResetToken: token}, nil)
}

// RestablecerContrasena valida el cuerpo, fija la contrasena y responde sin tokens.
func (h *HandlerDeRestablecimiento) RestablecerContrasena(w http.ResponseWriter, r *http.Request) {
	var entrada api.RecoveryResetRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	if err := h.restablecedor.RestablecerContrasena(r.Context(), entrada.ResetToken, entrada.NuevaContrasena); err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	mensaje := "Contrasena restablecida."
	api.WriteData(w, http.StatusOK, api.OkData{Mensaje: &mensaje}, nil)
}
