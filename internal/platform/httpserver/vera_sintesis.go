package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// maxTextoSintesis es el tope de caracteres de /v1/vera/sintetizar. Debe coincidir con el
// maxLength de SintetizarVeraRequest en api/openapi.yaml. Cabe con margen en el plazo de 25 s
// del backend: la cuenta está en docs/stt-tts-operacion.md.
const maxTextoSintesis = 400

// SintetizarVera entrega audio aparte del turno: un fallo nunca borra el mensaje.
func (s servidorV1) SintetizarVera(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.exigirConsentimientoVera(w, r, nil) {
		return
	}
	var req api.SintetizarVeraRequest
	if !api.DecodeAndValidateClosed(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Texto) == "" || utf8.RuneCountInString(req.Texto) > maxTextoSintesis {
		api.WriteError(w, 422, "ValidationError", fmt.Sprintf("El texto debe contener entre 1 y %d caracteres.", maxTextoSintesis), nil)
		return
	}
	if s.Vera.Sintetizador == nil {
		// Sin sintetizador no hay audio que entregar: no se gasta un intento de la cuota de voz.
		degradadoDeSintesis(w)
		return
	}
	if !s.exigirUsoVera(w, r, copilot.CanalVoz) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	audio, err := s.Vera.Sintetizador.Sintetizar(ctx, req.Texto, "ef_dora")
	if err == nil && len(audio) > 0 {
		w.Header().Set("Content-Type", "audio/wav")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(audio)
		return
	}
	degradadoDeSintesis(w)
}

// degradadoDeSintesis avisa a la app de que use su voz local: el mensaje nunca se pierde.
func degradadoDeSintesis(w http.ResponseWriter) {
	w.Header().Set("X-Vera-Audio", "no-disponible")
	api.WriteData(w, http.StatusOK, api.SintesisDegradada{AudioDisponible: false, VozLocal: true}, nil)
}
