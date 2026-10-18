package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

func (s servidorV1) exigirUsoVera(w http.ResponseWriter, r *http.Request, canal copilot.CanalAsistente) bool {
	if s.Vera.Uso == nil {
		api.WriteError(w, http.StatusServiceUnavailable, "AssistantUnavailable", "No se pudo verificar el límite del asistente.", nil)
		return false
	}
	err := s.Vera.Uso.Admitir(r.Context(), canal)
	if err == nil {
		return true
	}
	var limite *copilot.ErrorLimiteUso
	if errors.As(err, &limite) {
		segundos := int64(limite.ReintentarEn / time.Second)
		if limite.ReintentarEn%time.Second != 0 {
			segundos++
		}
		if segundos < 1 {
			segundos = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(segundos, 10))
		api.WriteError(w, http.StatusTooManyRequests, "TooManyRequests", "Límite de uso del asistente alcanzado. Intenta de nuevo después del intervalo indicado.", nil)
	} else {
		api.WriteError(w, http.StatusServiceUnavailable, "AssistantUnavailable", "No se pudo verificar el límite del asistente.", nil)
	}
	return false
}
