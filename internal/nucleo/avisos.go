package nucleo

import (
	"context"
	"fmt"
)

// avisarTrasAsiento dispara el aviso inyectado con el usuario titular después del commit. Una falla
// o un pánico del callback se registra y nunca toca el cierre de la liquidación.
func (s *Servicio) avisarTrasAsiento(ctx context.Context, usuarioID string) {
	if s.avisoTrasAsiento == nil || usuarioID == "" {
		return
	}
	defer func() {
		if recuperado := recover(); recuperado != nil {
			s.reportarAvisoFallido(ctx, usuarioID, fmt.Errorf("pánico en el aviso: %v", recuperado))
		}
	}()
	s.avisoTrasAsiento(ctx, usuarioID, s.momentoActual())
}

// reportarAvisoFallido deja rastro del callback que falló, sin tratarlo como falla del núcleo.
func (s *Servicio) reportarAvisoFallido(ctx context.Context, usuarioID string, causa error) {
	if s.rastreador == nil {
		return
	}
	s.rastreador.Capturar(ctx,
		fmt.Errorf("nucleo: avisar tras la liquidación confirmada de %s: %w", usuarioID, causa),
		ContextoError{
			Operacion: "confirmar_liquidacion",
			Canal:     CanalSoloLog,
			Campos:    []Campo{{Clave: "usuario_id", Valor: usuarioID}},
		})
}
