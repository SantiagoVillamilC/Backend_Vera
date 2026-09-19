package httpserver

import (
	"context"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// ActivadorDeCuenta es el servicio que activa la cuenta tras verificar el celular.
type ActivadorDeCuenta interface {
	ActivarCuenta(ctx context.Context, celular, codigo string) (auth.Activacion, error)
}

// HandlerDeVerificacion atiende la verificacion publica del celular y la activacion de la cuenta.
type HandlerDeVerificacion struct{ activador ActivadorDeCuenta }

// NuevoVerificacionHandler construye el handler sobre el servicio de activacion.
func NuevoVerificacionHandler(activador ActivadorDeCuenta) *HandlerDeVerificacion {
	return &HandlerDeVerificacion{activador: activador}
}

// verificacionDeCelular elige el handler real o el stub 501 cuando no esta inyectado.
func verificacionDeCelular(handler VerificacionHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.VerificarCelular
}

// VerificarCelular valida el codigo, activa la cuenta y responde 200 con los codigos de respaldo.
func (h *HandlerDeVerificacion) VerificarCelular(w http.ResponseWriter, r *http.Request) {
	var entrada api.VerifyPhoneRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	activacion, err := h.activador.ActivarCuenta(r.Context(), entrada.Celular, entrada.Codigo)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, respuestaDeVerificacion(activacion), nil)
}

// respuestaDeVerificacion arma la respuesta de activacion con los codigos de respaldo.
func respuestaDeVerificacion(activacion auth.Activacion) api.Verificacion {
	codigos := activacion.CodigosRespaldo
	return api.Verificacion{
		Id:              activacion.ParteID,
		Celular:         activacion.Celular,
		Estado:          api.VerificacionEstadoActivo,
		CodigosRespaldo: &codigos,
	}
}
