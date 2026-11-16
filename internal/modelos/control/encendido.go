package control

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/acceso"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/runpod"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

var patronTokenSeguro = regexp.MustCompile(`^[A-Za-z0-9._~+=/-]{32,200}$`)

// Puertos que escuchan los modelos dentro del pod.
const (
	puertoPodOllama = "127.0.0.1:11434"
	puertoPodSTT    = "127.0.0.1:8767"
	puertoPodVoz    = "127.0.0.1:8766"
)

// encender corre en su propia goroutine. Si falla y nadie lo canceló, apaga el pod y deja el motivo.
func (s *Servicio) encender(ctx context.Context) {
	err := s.arrancar(ctx)
	if err == nil || ctx.Err() != nil {
		return
	}
	final := estado.Error
	if errors.Is(err, errSinExistencias) {
		final = estado.SinExistencias
	}
	mensaje := secreto.Recortar(s.d.Redactor.Limpiar(err.Error()), 400)
	s.d.Log.Warn("el encendido fallo", "motivo", mensaje)
	s.solicitarApagado(final, mensaje)
}

func (s *Servicio) arrancar(ctx context.Context) error {
	if err := s.terminarPrevios(ctx); err != nil {
		return err
	}
	pod, err := s.crearPod(ctx)
	if err != nil {
		return err
	}
	destino, err := s.esperarPod(ctx, pod.ID)
	if err != nil {
		return err
	}
	if err := s.esperarSSH(ctx, destino); err != nil {
		return err
	}
	if err := s.arrancarModelos(ctx, destino); err != nil {
		return err
	}
	s.abrirTunel(destino)
	if err := s.esperarModelos(ctx); err != nil {
		return err
	}
	s.marcarListo()
	return nil
}

// terminarPrevios borra pods vera-modelos que ya existan (un huérfano) antes de crear otro.
func (s *Servicio) terminarPrevios(ctx context.Context) error {
	vivos, err := s.d.Nube.ListarVivos(ctx, s.d.Cfg.NombrePod)
	if err != nil {
		return fmt.Errorf("no se pudo consultar RunPod: %w", err)
	}
	if len(vivos) == 0 {
		return nil
	}
	s.d.Log.Warn("hay pods previos con el nombre del servicio, se terminan antes de crear otro", "cantidad", len(vivos))
	if r := s.term.Terminar(ctx); !r.Terminado {
		return fmt.Errorf("no se pudo terminar el pod previo: %s", r.Motivo)
	}
	return nil
}

func (s *Servicio) esperarPod(ctx context.Context, id string) (acceso.Destino, error) {
	limite := s.d.Reloj().Add(s.d.Cfg.EsperaPod)
	for {
		pod, err := s.d.Nube.Obtener(ctx, id)
		if err != nil && (errors.Is(err, runpod.ErrNoExiste) || esDefinitivo(err)) {
			return acceso.Destino{}, fmt.Errorf("no se pudo consultar el pod: %w", err)
		}
		if err == nil {
			switch pod.Estado {
			case "RUNNING":
				if pod.SSH != nil {
					return acceso.Destino{PodID: id, Host: pod.SSH.Host, Puerto: pod.SSH.Puerto, Usuario: pod.SSH.Usuario}, nil
				}
			case "ERROR", "EXITED", "TERMINATED":
				return acceso.Destino{}, fmt.Errorf("el pod paso a %s antes de quedar listo", pod.Estado)
			}
		}
		if !s.d.Reloj().Before(limite) {
			return acceso.Destino{}, errors.New("el pod no llego a RUNNING con ssh directo a tiempo")
		}
		if err := s.d.Dormir(ctx, s.d.IntervaloSondeo); err != nil {
			return acceso.Destino{}, err
		}
	}
}

func esDefinitivo(err error) bool {
	var api *runpod.ErrorAPI
	return errors.As(err, &api) && !api.Reintentable()
}

func (s *Servicio) esperarSSH(ctx context.Context, d acceso.Destino) error {
	limite := s.d.Reloj().Add(s.d.Cfg.EsperaSSH)
	espera := time.Second
	for {
		err := s.d.Acceso.Comprobar(ctx, d)
		if err == nil {
			return nil
		}
		if errors.Is(err, acceso.ErrHuella) {
			s.d.Log.Error("ALERTA: la huella ssh del pod no coincide con la registrada, se aborta", "pod", d.PodID)
			return fmt.Errorf("huella ssh distinta, posible suplantacion: %w", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !s.d.Reloj().Before(limite) {
			return fmt.Errorf("el pod no acepto ssh a tiempo: %w", err)
		}
		if err := s.d.Dormir(ctx, espera); err != nil {
			return err
		}
		espera = min(espera*2, 15*time.Second)
	}
}

// preambulo arma lo que entra por stdin del "bash -s" remoto: los tokens quedan fuera de argv.
func (s *Servicio) preambulo() (string, error) {
	if !patronTokenSeguro.MatchString(s.d.TokenSTT) || !patronTokenSeguro.MatchString(s.d.TokenVoz) {
		return "", fmt.Errorf("%w: los tokens de los modelos tienen caracteres no admitidos", ErrConfiguracion)
	}
	var b strings.Builder
	b.WriteString("export WHISPER_TOKEN='" + s.d.TokenSTT + "'\n")
	b.WriteString("export KOKORO_TOKEN='" + s.d.TokenVoz + "'\n")
	if s.d.Cfg.RepoSHA != "" {
		b.WriteString("export REPO_SHA='" + s.d.Cfg.RepoSHA + "'\n")
	}
	b.WriteString("exec bash " + s.d.Cfg.ScriptRemoto + "\n")
	return b.String(), nil
}

func (s *Servicio) arrancarModelos(ctx context.Context, d acceso.Destino) error {
	stdin, err := s.preambulo()
	if err != nil {
		return err
	}
	ctx, cancelar := context.WithTimeout(ctx, s.d.Cfg.EsperaArranque)
	defer cancelar()
	if err := s.d.Acceso.Subir(ctx, d, s.d.Cfg.ScriptArranque, s.d.Cfg.ScriptRemoto); err != nil {
		return fmt.Errorf("arranque de los modelos, copia del script: %w", err)
	}
	if err := s.d.Acceso.Ejecutar(ctx, d, "bash -s", strings.NewReader(stdin)); err != nil {
		return fmt.Errorf("arranque de los modelos, el script remoto fallo: %w", err)
	}
	return nil
}

func (s *Servicio) abrirTunel(d acceso.Destino) {
	reenvios := []acceso.Reenvio{
		{Local: s.d.Cfg.InternoOllama, Remoto: puertoPodOllama},
		{Local: s.d.Cfg.InternoSTT, Remoto: puertoPodSTT},
		{Local: s.d.Cfg.InternoVoz, Remoto: puertoPodVoz},
	}
	// El túnel vive más que el encendido: se ata al contexto base y lo cierra el apagado.
	t := s.d.Acceso.AbrirTunel(s.base, d, reenvios, s.alFatalTunel)
	s.mu.Lock()
	s.tunel = t
	s.mu.Unlock()
}

func (s *Servicio) alFatalTunel(err error) {
	mensaje := secreto.Recortar(s.d.Redactor.Limpiar(err.Error()), 300)
	s.d.Log.Error("ALERTA: el tunel ssh no se puede reabrir", "motivo", mensaje)
	go s.solicitarApagado(estado.Error, "tunel ssh caido: "+mensaje)
}

func (s *Servicio) esperarModelos(ctx context.Context) error {
	limite := s.d.Reloj().Add(s.d.Cfg.EsperaModelos)
	for {
		err := s.d.Sonda(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !s.d.Reloj().Before(limite) {
			return fmt.Errorf("los modelos no respondieron por el tunel a tiempo: %s", s.d.Redactor.Limpiar(err.Error()))
		}
		if err := s.d.Dormir(ctx, s.d.IntervaloSondeo); err != nil {
			return err
		}
	}
}

func (s *Servicio) marcarListo() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.est.Fase != estado.Encendiendo {
		return
	}
	ahora := s.d.Reloj()
	s.est.Fase, s.est.Mensaje, s.est.UltimaActividad = estado.Listo, "", ahora
	s.d.Actividad.Marcar(ahora)
	s.guardarLocked()
	s.d.Log.Info("modelos listos", "pod", s.est.PodID, "gpu", s.est.GPU)
}
