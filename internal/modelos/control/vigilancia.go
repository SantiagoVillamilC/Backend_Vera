package control

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/acceso"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/runpod"
)

// Vigilar llama a Tick cada intervalo hasta que el contexto termine.
func (s *Servicio) Vigilar(ctx context.Context) {
	temporizador := time.NewTicker(s.d.Cfg.Vigilancia)
	defer temporizador.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-temporizador.C:
			s.Tick(ctx)
		}
	}
}

// Tick aplica las reglas de apagado: sesión máxima, inactividad y pod desaparecido. Los chequeos de
// salud no cuentan como actividad (el proxy no los registra) y una petición en vuelo evita el apagado
// por inactividad, no el de la sesión máxima.
func (s *Servicio) Tick(ctx context.Context) {
	s.mu.Lock()
	est := s.est
	s.mu.Unlock()
	ahora := s.d.Reloj()
	if est.Fase != estado.Listo && est.Fase != estado.Encendiendo {
		return
	}
	if !est.SesionInicio.IsZero() && ahora.Sub(est.SesionInicio) >= s.d.Cfg.SesionMaxima {
		s.solicitarApagado(estado.Apagado, fmt.Sprintf("sesion maxima de %s alcanzada", s.d.Cfg.SesionMaxima))
		return
	}
	if est.Fase == estado.Listo {
		s.vigilarListo(ctx, est, ahora)
	}
}

func (s *Servicio) vigilarListo(ctx context.Context, est estado.Estado, ahora time.Time) {
	ultima := laMasReciente(est.UltimaActividad, s.d.Actividad.Ultima())
	if s.d.Actividad.EnVuelo() == 0 && ahora.Sub(ultima) >= s.d.Cfg.Inactividad {
		s.solicitarApagado(estado.Apagado, fmt.Sprintf("sin peticiones durante %s", s.d.Cfg.Inactividad))
		return
	}
	s.guardarActividad(ultima)
	pod, err := s.d.Nube.Obtener(ctx, est.PodID)
	switch {
	case errors.Is(err, runpod.ErrNoExiste), err == nil && pod.Estado != "RUNNING":
		s.solicitarApagado(estado.Error, "el pod dejo de existir o dejo de estar RUNNING")
	case err != nil:
		s.d.Log.Warn("no se pudo comprobar el pod", "error", s.d.Redactor.Limpiar(err.Error()))
	}
}

func (s *Servicio) guardarActividad(ultima time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.est.Fase == estado.Listo && ultima.After(s.est.UltimaActividad) {
		s.est.UltimaActividad = ultima
		s.guardarLocked()
	}
}

// Recuperar reconcilia el estado de disco con RunPod al arrancar el proceso, para no dejar un pod
// huérfano tras un reinicio. Un pod Listo y sano se retoma, cualquier otro se termina.
func (s *Servicio) Recuperar(ctx context.Context) error {
	vivos, err := s.d.Nube.ListarVivos(ctx, s.d.Cfg.NombrePod)
	if err != nil {
		s.mu.Lock()
		s.est.Fase, s.est.Mensaje = estado.Error, "no se pudo reconciliar con RunPod al arrancar"
		s.guardarLocked()
		s.mu.Unlock()
		return fmt.Errorf("reconciliar: %w", err)
	}
	s.mu.Lock()
	est := s.est
	s.mu.Unlock()
	switch {
	case len(vivos) == 0:
		s.mu.Lock()
		s.est = estado.Estado{Fase: estado.Apagado, Mensaje: s.mensajeSinPod(est)}
		s.guardarLocked()
		s.mu.Unlock()
	case est.Fase == estado.Listo && len(vivos) == 1 && vivos[0].ID == est.PodID && vivos[0].Estado == "RUNNING":
		return s.retomar(ctx, est.PodID)
	default:
		s.mu.Lock()
		if est.PodID == "" {
			s.est.PodID = vivos[0].ID
		}
		s.mu.Unlock()
		s.solicitarApagado(estado.Apagado, "reconciliacion: pod sin dueno terminado al arrancar")
	}
	return nil
}

func (s *Servicio) mensajeSinPod(previo estado.Estado) string {
	if previo.Fase == estado.Listo || previo.Fase == estado.Encendiendo || previo.Fase == estado.Apagando {
		return "reconciliado: el pod ya no existia"
	}
	return previo.Mensaje
}

// retomar reabre el túnel de un pod que seguía Listo y comprueba los modelos. Si algo falla, lo apaga.
func (s *Servicio) retomar(ctx context.Context, id string) error {
	pod, err := s.d.Nube.Obtener(ctx, id)
	if err != nil || pod.SSH == nil {
		s.solicitarApagado(estado.Apagado, "reconciliacion: no se pudo retomar el pod")
		return nil
	}
	s.abrirTunel(acceso.Destino{PodID: id, Host: pod.SSH.Host, Puerto: pod.SSH.Puerto, Usuario: pod.SSH.Usuario})
	if err := s.esperarModelos(ctx); err != nil {
		s.solicitarApagado(estado.Apagado, "reconciliacion: los modelos no responden tras reiniciar")
		return nil
	}
	s.mu.Lock()
	s.registrarActividadLocked()
	s.mu.Unlock()
	s.d.Log.Info("pod retomado tras reiniciar", "pod", id)
	return nil
}
