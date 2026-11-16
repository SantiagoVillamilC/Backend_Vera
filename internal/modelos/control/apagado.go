package control

import (
	"context"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

// solicitarApagado pasa a Apagando y lanza la terminación. Cancela un encendido en curso y espera a
// que su goroutine salga antes de borrar nada. Si ya se está apagando no hace nada.
func (s *Servicio) solicitarApagado(final estado.Fase, mensaje string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.est.Fase == estado.Apagando || s.base.Err() != nil {
		return
	}
	anterior := s.trabajo
	if anterior != nil {
		anterior.cancelar()
	}
	s.est.Fase, s.est.Mensaje = estado.Apagando, mensaje
	s.guardarLocked()
	ctx, cancelar := context.WithCancel(s.base)
	t := &trabajo{cancelar: cancelar, hecho: make(chan struct{})}
	s.trabajo = t
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(t.hecho)
		s.apagar(ctx, anterior, final, mensaje)
	}()
}

func (s *Servicio) apagar(ctx context.Context, anterior *trabajo, final estado.Fase, mensaje string) {
	if anterior != nil {
		<-anterior.hecho
	}
	s.cerrarTunel()
	s.mu.Lock()
	id := s.est.PodID
	s.mu.Unlock()
	s.d.Log.Info("apagando los modelos", "pod", id, "motivo", mensaje)
	var ids []string
	if id != "" {
		ids = []string{id}
	}
	res := s.term.Terminar(ctx, ids...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if res.Terminado {
		s.est = estado.Estado{Fase: final, Mensaje: mensaje}
		s.d.Log.Info("pod terminado y verificado con la lista", "intentos", res.Intentos)
	} else {
		restante := id
		if restante == "" && len(res.Restantes) > 0 {
			restante = res.Restantes[0]
		}
		s.est.Fase, s.est.PodID = estado.Error, restante
		s.est.Mensaje = secreto.Recortar(s.d.Redactor.Limpiar(fmt.Sprintf("NO se pudo terminar el pod %s: %s", restante, res.Motivo)), 400)
		s.d.Log.Error("ALERTA: el pod sigue vivo y puede estar cobrando", "pod", restante, "restantes", res.Restantes)
	}
	s.guardarLocked()
}
