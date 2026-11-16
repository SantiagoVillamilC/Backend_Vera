// Package acceso ejecuta ssh y scp hacia el pod y mantiene el túnel. Las huellas se guardan por pod y
// una huella distinta es un error fatal: nunca se borra una entrada ni se desactiva la verificación.
package acceso

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

// ErrHuella indica que la huella SSH del pod no coincide con la registrada.
var ErrHuella = errors.New("acceso: la huella ssh del pod cambio")

const maxSalida = 8 << 10

var (
	patronID      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	patronHost    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	patronUsuario = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	patronRemoto  = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,200}$`)
)

// Destino es el pod al que se conecta.
type Destino struct {
	PodID   string
	Host    string
	Puerto  int
	Usuario string
}

func (d Destino) validar() error {
	if !patronID.MatchString(d.PodID) || !patronHost.MatchString(d.Host) || !patronUsuario.MatchString(d.Usuario) || d.Puerto < 1 || d.Puerto > 65535 {
		return errors.New("acceso: destino ssh invalido")
	}
	return nil
}

// Huellas entrega el archivo known_hosts propio de cada pod.
type Huellas interface {
	RutaKnownHosts(podID string) (string, error)
}

// Config reúne lo que necesita SSH. SSHBin y SCPBin permiten sustituir los binarios en pruebas.
type Config struct {
	SSHBin, SCPBin, Llave string
	Huellas               Huellas
	Log                   *slog.Logger
	Redactor              secreto.Redactor
	// EsperaReintentoTunel es la pausa inicial al reabrir un túnel caído (crece hasta 30 s).
	EsperaReintentoTunel time.Duration
}

// SSH ejecuta comandos remotos y copia archivos.
type SSH struct{ cfg Config }

// Nuevo crea el ejecutor.
func Nuevo(cfg Config) *SSH {
	if cfg.EsperaReintentoTunel <= 0 {
		cfg.EsperaReintentoTunel = time.Second
	}
	return &SSH{cfg: cfg}
}

// opciones son los -o comunes. accept-new confía en el primer uso y falla ante una huella distinta.
func (s *SSH) opciones(d Destino) ([]string, error) {
	if err := d.validar(); err != nil {
		return nil, err
	}
	conocidos, err := s.cfg.Huellas.RutaKnownHosts(d.PodID)
	if err != nil {
		return nil, err
	}
	return []string{"-i", s.cfg.Llave, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + conocidos, "-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "UpdateHostKeys=no", "-o", "ConnectTimeout=15", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
		"-o", "LogLevel=ERROR"}, nil
}

func (s *SSH) argsSSH(d Destino, extra []string, comando string) ([]string, error) {
	base, err := s.opciones(d)
	if err != nil {
		return nil, err
	}
	args := append(append([]string{"-p", strconv.Itoa(d.Puerto)}, base...), extra...)
	args = append(args, "--", d.Usuario+"@"+d.Host)
	if comando != "" {
		args = append(args, comando)
	}
	return args, nil
}

// entornoMinimo evita que el subproceso herede la clave de RunPod o los tokens del servicio.
func entornoMinimo() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "LANG", "LC_ALL"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

type tope struct {
	buf bytes.Buffer
}

func (t *tope) Write(p []byte) (int, error) {
	if falta := maxSalida - t.buf.Len(); falta > 0 {
		t.buf.Write(p[:min(len(p), falta)])
	}
	return len(p), nil
}

func (s *SSH) preparar(ctx context.Context, bin string, args []string, stdin io.Reader) (*exec.Cmd, *tope) {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // binario de configuracion y argumentos validados
	cmd.Env = entornoMinimo()
	cmd.Stdin = stdin
	cmd.WaitDelay = 5 * time.Second
	salida := &tope{}
	cmd.Stderr = salida
	cmd.Stdout = salida
	return cmd, salida
}

// errorDeProceso convierte la salida de un proceso en un error sin secretos.
func (s *SSH) errorDeProceso(herramienta string, err error, salida string) error {
	if strings.Contains(salida, "Host key verification failed") || strings.Contains(salida, "REMOTE HOST IDENTIFICATION HAS CHANGED") {
		return fmt.Errorf("%w (%s)", ErrHuella, herramienta)
	}
	texto := secreto.Recortar(s.cfg.Redactor.Limpiar(strings.TrimSpace(salida)), 300)
	var salidaProceso *exec.ExitError
	if errors.As(err, &salidaProceso) {
		return fmt.Errorf("acceso: %s termino con codigo %d: %s", herramienta, salidaProceso.ExitCode(), texto)
	}
	return fmt.Errorf("acceso: %s fallo: %s", herramienta, s.cfg.Redactor.Limpiar(err.Error()))
}

func (s *SSH) correr(ctx context.Context, herramienta, bin string, args []string, stdin io.Reader) error {
	cmd, salida := s.preparar(ctx, bin, args, stdin)
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.errorDeProceso(herramienta, err, salida.buf.String())
}

// Comprobar dice si sshd acepta una sesión con la llave.
func (s *SSH) Comprobar(ctx context.Context, d Destino) error {
	args, err := s.argsSSH(d, nil, "true")
	if err != nil {
		return err
	}
	return s.correr(ctx, "ssh", s.cfg.SSHBin, args, nil)
}

// Ejecutar corre un comando remoto. Los secretos van por stdin, nunca en el comando.
func (s *SSH) Ejecutar(ctx context.Context, d Destino, comando string, stdin io.Reader) error {
	args, err := s.argsSSH(d, nil, comando)
	if err != nil {
		return err
	}
	return s.correr(ctx, "ssh", s.cfg.SSHBin, args, stdin)
}

// Subir copia un archivo local a una ruta absoluta remota con scp.
func (s *SSH) Subir(ctx context.Context, d Destino, local, remoto string) error {
	if !patronRemoto.MatchString(remoto) || strings.Contains(remoto, "..") {
		return errors.New("acceso: ruta remota invalida")
	}
	base, err := s.opciones(d)
	if err != nil {
		return err
	}
	args := append(append([]string{"-P", strconv.Itoa(d.Puerto)}, base...), "--", local, d.Usuario+"@"+d.Host+":"+remoto)
	return s.correr(ctx, "scp", s.cfg.SCPBin, args, nil)
}
