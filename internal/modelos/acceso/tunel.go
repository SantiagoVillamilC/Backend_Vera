package acceso

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Reenvio es un -L: puerto local interno hacia un puerto del pod.
type Reenvio struct{ Local, Remoto string }

// Tunel es un ssh -N supervisado que se reabre si muere.
type Tunel struct {
	cancelar context.CancelFunc
	vivo     atomic.Bool
	hecho    chan struct{}
	una      sync.Once
}

// Vivo dice si el proceso ssh del túnel está corriendo ahora.
func (t *Tunel) Vivo() bool { return t.vivo.Load() }

// Cerrar detiene el túnel y espera a que el proceso termine. Es idempotente.
func (t *Tunel) Cerrar() {
	t.una.Do(t.cancelar)
	<-t.hecho
}

// AbrirTunel lanza la supervisión. Si el proceso muere se reabre con espera creciente. Una huella
// distinta detiene la supervisión y se informa por alFatal, sin reintentar.
func (s *SSH) AbrirTunel(ctx context.Context, d Destino, reenvios []Reenvio, alFatal func(error)) *Tunel {
	ctx, cancelar := context.WithCancel(ctx) //nolint:gosec // cancelar se guarda en Tunel y lo llama Cerrar
	t := &Tunel{cancelar: cancelar, hecho: make(chan struct{})}
	go func() {
		defer close(t.hecho)
		s.supervisar(ctx, t, d, reenvios, alFatal)
	}()
	return t
}

func (s *SSH) argsTunel(d Destino, reenvios []Reenvio) ([]string, error) {
	extra := []string{"-N", "-o", "ExitOnForwardFailure=yes"}
	for _, r := range reenvios {
		extra = append(extra, "-L", r.Local+":"+r.Remoto)
	}
	return s.argsSSH(d, extra, "")
}

func (s *SSH) supervisar(ctx context.Context, t *Tunel, d Destino, reenvios []Reenvio, alFatal func(error)) {
	espera := s.cfg.EsperaReintentoTunel
	for ctx.Err() == nil {
		args, err := s.argsTunel(d, reenvios)
		if err != nil {
			alFatal(err)
			return
		}
		inicio := time.Now()
		cmd, salida := s.preparar(ctx, s.cfg.SSHBin, args, nil)
		if err := cmd.Start(); err != nil {
			alFatal(s.errorDeProceso("ssh", err, ""))
			return
		}
		t.vivo.Store(true)
		errEspera := cmd.Wait()
		t.vivo.Store(false)
		if ctx.Err() != nil {
			return
		}
		fallo := s.errorDeProceso("ssh", errEspera, salida.buf.String())
		if errors.Is(fallo, ErrHuella) {
			s.cfg.Log.Error("ALERTA: la huella ssh del pod no coincide, el tunel no se reabre", "pod", d.PodID)
			alFatal(fallo)
			return
		}
		s.cfg.Log.Warn("el tunel ssh murio, se reabre", "pod", d.PodID, "error", fallo.Error())
		if time.Since(inicio) > time.Minute {
			espera = s.cfg.EsperaReintentoTunel
		}
		if !esperarContexto(ctx, espera) {
			return
		}
		espera = min(espera*2, 30*time.Second)
	}
}

func esperarContexto(ctx context.Context, d time.Duration) bool {
	temporizador := time.NewTimer(d)
	defer temporizador.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-temporizador.C:
		return true
	}
}
