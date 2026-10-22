package laboratorio

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

// Límite preventivo local; no representa los créditos restantes de la cuenta.
const maxCaracteresCartesia = 5000

var errPresupuestoCartesia = errors.New("presupuesto local de Cartesia agotado")

func (s *servidor) esperaCartesia() int {
	return max(0, int(math.Ceil(time.Until(s.pausaCartesia).Seconds())))
}

// Se llama bajo el mutex de la sesión. Nueva conversación conserva este consumo.
// Sólo una síntesis por intento; nunca cambia de proveedor o voz silenciosamente.
func (s *servidor) sintetizarCartesia(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.vozCartesia == nil {
		return nil, copilot.ErrNoDisponibleIA
	}
	if s.credencialesCartesiaInvalidas {
		return nil, copilot.ErrCredencialesIA
	}
	if espera := time.Until(s.pausaCartesia); espera > 0 {
		return nil, &copilot.ErrorLimiteIA{Clase: copilot.LimiteDesconocido, ReintentarEn: espera}
	}
	n := utf8.RuneCountInString(s.ultimoTexto)
	if n > maxCaracteresCartesia-s.caracteresCartesia {
		return nil, errPresupuestoCartesia
	}
	// Reserva conservadora incluso ante errores: no asumimos que un intento no cobra.
	s.caracteresCartesia += n
	audio, err := s.vozCartesia.Sintetizar(ctx, s.ultimoTexto, "Catalina")
	var limite *copilot.ErrorLimiteIA
	if errors.As(err, &limite) {
		espera := limite.ReintentarEn
		if espera <= 0 || espera > 24*time.Hour {
			espera = time.Minute
		}
		s.pausaCartesia = time.Now().Add(espera)
	}
	if errors.Is(err, copilot.ErrCredencialesIA) {
		s.credencialesCartesiaInvalidas = true
	}
	return audio, err
}

func (s *servidor) errorVoz(w http.ResponseWriter, err error, voz string) {
	if voz != "Catalina" {
		s.error(w, err)
		return
	}
	var limite *copilot.ErrorLimiteIA
	if errors.As(err, &limite) {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, s.esperaCartesia())))
		api.WriteError(w, 429, "CartesiaLimit", "Cartesia limitó la voz de Catalina. Espera antes de reintentar o elige otra voz; el chat sigue disponible.", nil)
		return
	}
	status, codigo, mensaje := 503, "CartesiaUnavailable", "No se pudo generar la voz de Catalina. El texto sigue disponible; puedes elegir otra voz."
	switch {
	case errors.Is(err, errPresupuestoCartesia):
		status, codigo, mensaje = 429, "CartesiaSessionLimit", "Se alcanzó el límite local de caracteres de Cartesia. El texto sigue disponible; puedes elegir otra voz."
	case errors.Is(err, copilot.ErrCredencialesIA):
		status, codigo, mensaje = 401, "CartesiaCredentials", "Cartesia rechazó la clave de voz. Revisa CARTESIA_API_KEY sin compartirla; el chat sigue disponible."
	case errors.Is(err, copilot.ErrTimeoutIA), errors.Is(err, context.DeadlineExceeded):
		status, codigo, mensaje = 504, "CartesiaTimeout", "Catalina tardó demasiado en generar el audio. El texto sigue disponible; puedes reintentar o elegir otra voz."
	}
	api.WriteError(w, status, codigo, mensaje, nil)
}
