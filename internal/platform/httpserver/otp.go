package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/google/uuid"
)

// ReenviadorDesafios reenvía el código de un desafío vigente.
type ReenviadorDesafios interface {
	ReenviarDesafio(ctx context.Context, id uuid.UUID, origen string) (auth.Desafio, error)
}

// OTPHandler atiende el reenvío público del código de un desafío.
type OTPHandler struct{ reenviador ReenviadorDesafios }

// NuevoOTPHandler construye el handler público de reenvío.
func NuevoOTPHandler(reenviador ReenviadorDesafios) *OTPHandler {
	return &OTPHandler{reenviador: reenviador}
}

// reenvioDeOtp elige el handler real de reenvío o el stub 501 cuando no está inyectado.
func reenvioDeOtp(handler DesafiosHandler) http.HandlerFunc {
	if handler == nil {
		return sinImplementar
	}
	return handler.ResendOtp
}

// ResendOtp valida el cuerpo, reenvía el desafío y responde el Desafio con la vigencia actualizada.
func (h *OTPHandler) ResendOtp(w http.ResponseWriter, r *http.Request) {
	var entrada api.ResendOtpRequest
	if !api.DecodeAndValidate(w, r, &entrada) {
		return
	}
	origen, _ := clientIPKey(r)
	desafio, err := h.reenviador.ReenviarDesafio(r.Context(), entrada.ChallengeId, origen)
	if err != nil {
		escribirErrorDeAuth(w, r, err)
		return
	}
	api.WriteData(w, http.StatusOK, api.Desafio{
		ChallengeId:      entrada.ChallengeId,
		ExpiraEnSegundos: desafio.ExpiraEnSegundos,
		Proposito:        api.DesafioProposito(desafio.Proposito),
	}, nil)
}

// escribirErrorDeAuth honra el APIError de auth (incluido el 503) y delega el resto en WriteAPIError.
func escribirErrorDeAuth(w http.ResponseWriter, r *http.Request, err error) {
	mapeado := auth.MapearError(err)
	var apiErr *platformerrors.APIError
	if errors.As(mapeado, &apiErr) {
		api.WriteError(w, apiErr.Status, apiErr.Code, apiErr.Message, nil)
		return
	}
	api.WriteAPIError(w, r, mapeado)
}
