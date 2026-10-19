package auth

import (
	"context"
	"errors"
	"time"
)

// Errores del presupuesto compartido por HTTP y el asistente.
var (
	ErrConsultaDestinatariosLimitada     = errors.New("auth: demasiadas consultas de destinatarios")
	ErrConsultaDestinatariosNoDisponible = errors.New("auth: contador de destinatarios no disponible")
)

// LimiteDestinatarios cuenta intentos por usuario, independientemente del canal.
type LimiteDestinatarios struct {
	contadores Contadores
	claves     *Claves
	limite     int64
	ventana    time.Duration
}

// NuevoLimiteDestinatarios exige un contador compartido que falle cerrado.
func NuevoLimiteDestinatarios(c Contadores, claves *Claves, limite int, ventana time.Duration) *LimiteDestinatarios {
	if c == nil || claves == nil || limite <= 0 || ventana <= 0 {
		panic("destinatarios: configuracion incompleta")
	}
	return &LimiteDestinatarios{c, claves, int64(limite), ventana}
}

// Exigir consume también los intentos que terminan en destino propio o inexistente.
func (l *LimiteDestinatarios) Exigir(ctx context.Context, usuario string) error {
	if usuario == "" {
		return ErrConsultaDestinatariosNoDisponible
	}
	n, err := l.contadores.Incrementar(ctx, "destinatarios:usuario:"+l.claves.ClaveDeParte(usuario), l.ventana)
	if err != nil {
		return ErrConsultaDestinatariosNoDisponible
	}
	if n > l.limite {
		return ErrConsultaDestinatariosLimitada
	}
	return nil
}
