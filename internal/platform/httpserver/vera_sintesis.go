package httpserver

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// SintetizarVera entrega audio aparte del turno: un fallo nunca borra el mensaje.
func (s servidorV1) SintetizarVera(w http.ResponseWriter, r *http.Request, params api.SintetizarVeraParams) {
	w.Header().Set("Cache-Control", "no-store")
	if len(s.Vera.TokenVoz) < 32 || subtle.ConstantTimeCompare([]byte(params.XVeraToken), []byte(s.Vera.TokenVoz)) != 1 {
		api.WriteError(w, 401, "InvalidCredentials", "Token de voz inválido.", nil)
		return
	}
	if !s.exigirConsentimientoVera(w, r, nil) {
		return
	}
	var req api.SintetizarVeraRequest
	if !api.DecodeAndValidateClosed(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Texto) == "" || len(req.Texto) > 8192 {
		api.WriteError(w, 422, "ValidationError", "El texto debe contener entre 1 y 8192 bytes.", nil)
		return
	}
	if !s.exigirUsoVera(w, r, copilot.CanalVoz) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if s.Vera.Sintetizador != nil {
		audio, err := s.Vera.Sintetizador.Sintetizar(ctx, req.Texto, "ef_dora")
		if err == nil && len(audio) > 0 {
			w.Header().Set("Content-Type", "audio/wav")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(audio)
			return
		}
	}
	w.Header().Set("X-Vera-Audio", "no-disponible")
	api.WriteData(w, http.StatusOK, api.SintesisDegradada{AudioDisponible: false, VozLocal: true}, nil)
}
