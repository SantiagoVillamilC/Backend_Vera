package httpserver

import (
	"context"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// RecuperadorDeCuenta pide el desafío de recuperación sin revelar si la cuenta existe.
type RecuperadorDeCuenta interface {
	SolicitarRecuperacion(ctx context.Context, correo, origen string) (auth.Desafio, error)
}

// RecuperacionHandler atiende la solicitud pública del desafío de recuperación.
type RecuperacionHandler interface {
	SolicitarRecuperacion(http.ResponseWriter, *http.Request)
}

// HandlerDeRecuperacion atiende la entrada pública de la recuperación.
type HandlerDeRecuperacion struct{ recuperador RecuperadorDeCuenta }

// NuevoRecuperacionHandler construye el handler sobre el servicio de recuperación.
func NuevoRecuperacionHandler(recuperador RecuperadorDeCuenta) *HandlerDeRecuperacion {
	return &HandlerDeRecuperacion{recuperador: recuperador}
}

// recuperacionDeSesion elige el handler real o el stub 501 cuando no está inyectado.
func recuperacionDeSesion(handler RecuperacionHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.SolicitarRecuperacion
}

// SolicitarRecuperacion valida el correo y responde 202 con el desafío, siempre genérico.
func (h *HandlerDeRecuperacion) SolicitarRecuperacion(w http.ResponseWriter, r *http.Request) {
	var entrada api.RecoveryRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	origen, _ := clientIPKey(r)
	desafio, err := h.recuperador.SolicitarRecuperacion(r.Context(), entrada.Correo, origen)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusAccepted, api.Desafio{
		ChallengeId:      desafio.ID,
		ExpiraEnSegundos: desafio.ExpiraEnSegundos,
		Proposito:        api.DesafioProposito(desafio.Proposito),
	}, nil)
}
