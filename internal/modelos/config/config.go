// Package config carga la configuración de modelos-control desde variables de entorno MODELOS_*,
// con valores por defecto seguros. Los secretos nunca se leen aquí: solo se guardan sus rutas.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// NombrePod es el nombre fijo con el que se identifica el pod del servicio.
const NombrePod = "vera-modelos"

var (
	patronGPU    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 \-]{0,62}[A-Za-z0-9]$`)
	patronImagen = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]{0,199}$`)
	patronRuta   = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,200}$`)
	patronSHA    = regexp.MustCompile(`^[A-Za-z0-9._-]{0,64}$`)
)

// Config es la configuración efectiva. Se construye con Cargar y no se modifica después.
type Config struct {
	NombrePod string
	RunPodURL string
	GPUs      []string
	Imagen    string
	DiscoGB   int

	TarifaTopeUSDH float64
	Inactividad    time.Duration
	SesionMaxima   time.Duration
	EsperaPod      time.Duration
	EsperaSSH      time.Duration
	EsperaArranque time.Duration
	EsperaModelos  time.Duration
	Vigilancia     time.Duration

	DirEstado string
	Control   string

	EscuchaVoz, EscuchaSTT, EscuchaOllama string
	InternoVoz, InternoSTT, InternoOllama string

	ArchivoClaveRunPod  string
	ArchivoTokenControl string
	ArchivoTokens       string
	LlaveSSH            string
	LlavePublicaSSH     string
	SSHBin, SCPBin      string
	ScriptArranque      string
	ScriptRemoto        string
	RepoSHA             string
}

type lector struct {
	buscar func(string) (string, bool)
	errs   []error
}

func (l *lector) fallo(clave, motivo string) {
	l.errs = append(l.errs, fmt.Errorf("%s: %s", clave, motivo))
}

func (l *lector) texto(clave, defecto string) string {
	if v, ok := l.buscar(clave); ok {
		return strings.TrimSpace(v)
	}
	return defecto
}

func (l *lector) duracion(clave string, defecto, piso, techo time.Duration) time.Duration {
	crudo := l.texto(clave, "")
	if crudo == "" {
		return defecto
	}
	d, err := time.ParseDuration(crudo)
	if err != nil || d < piso || d > techo {
		l.fallo(clave, fmt.Sprintf("debe ser una duracion entre %s y %s (por ejemplo 10m)", piso, techo))
		return defecto
	}
	return d
}

func (l *lector) decimal(clave string, defecto, piso, techo float64) float64 {
	crudo := l.texto(clave, "")
	if crudo == "" {
		return defecto
	}
	v, err := strconv.ParseFloat(crudo, 64)
	if err != nil || v < piso || v > techo {
		l.fallo(clave, fmt.Sprintf("debe ser un numero entre %g y %g", piso, techo))
		return defecto
	}
	return v
}

func (l *lector) entero(clave string, defecto, piso, techo int) int {
	crudo := l.texto(clave, "")
	if crudo == "" {
		return defecto
	}
	v, err := strconv.Atoi(crudo)
	if err != nil || v < piso || v > techo {
		l.fallo(clave, fmt.Sprintf("debe ser un entero entre %d y %d", piso, techo))
		return defecto
	}
	return v
}

func (l *lector) direccion(clave, defecto string) string {
	valor := l.texto(clave, defecto)
	host, puerto, err := net.SplitHostPort(valor)
	ip := net.ParseIP(host)
	p, errPuerto := strconv.Atoi(puerto)
	if err != nil || ip == nil || !ip.IsLoopback() || errPuerto != nil || p < 1 || p > 65535 {
		l.fallo(clave, "debe ser una direccion loopback IP:puerto, por ejemplo 127.0.0.1:8790")
		return defecto
	}
	return valor
}

func (l *lector) ruta(clave, defecto string) string {
	valor := l.texto(clave, defecto)
	if valor != "" && !patronRuta.MatchString(valor) {
		l.fallo(clave, "debe ser una ruta absoluta sencilla")
		return defecto
	}
	return valor
}

func (l *lector) gpus(defecto []string) []string {
	crudo := l.texto("MODELOS_GPUS", "")
	if crudo == "" {
		return defecto
	}
	var salida []string
	for _, g := range strings.Split(crudo, ",") {
		g = strings.TrimSpace(g)
		if !patronGPU.MatchString(g) {
			l.fallo("MODELOS_GPUS", "cada GPU solo admite letras, digitos, espacios y guiones")
			return defecto
		}
		salida = append(salida, g)
	}
	return salida
}

func (l *lector) urlRunPod(defecto string) string {
	valor := l.texto("MODELOS_RUNPOD_URL", defecto)
	u, err := url.Parse(valor)
	loopback := err == nil && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
	if err != nil || u.User != nil || (u.Scheme != "https" && (u.Scheme != "http" || !loopback)) {
		l.fallo("MODELOS_RUNPOD_URL", "debe ser https (http solo hacia loopback en pruebas)")
		return defecto
	}
	return valor
}

// Cargar lee MODELOS_* con buscar (por ejemplo os.LookupEnv) y valida todo junto.
func Cargar(buscar func(string) (string, bool)) (Config, error) {
	l := &lector{buscar: buscar}
	c := Config{NombrePod: NombrePod}
	c.RunPodURL = l.urlRunPod("https://api.runpod.io/v2")
	c.GPUs = l.gpus([]string{"NVIDIA RTX A5000", "NVIDIA RTX A4000"})
	c.Imagen = l.texto("MODELOS_IMAGEN", "runpod/pytorch:1.0.2-cu1281-torch280-ubuntu2404")
	if !patronImagen.MatchString(c.Imagen) {
		l.fallo("MODELOS_IMAGEN", "referencia de imagen invalida")
	}
	c.DiscoGB = l.entero("MODELOS_DISCO_GB", 40, 20, 500)
	c.TarifaTopeUSDH = l.decimal("MODELOS_TARIFA_TOPE_USD_H", 0.35, 0.01, 5)
	c.Inactividad = l.duracion("MODELOS_INACTIVIDAD", 10*time.Minute, time.Minute, 24*time.Hour)
	c.SesionMaxima = l.duracion("MODELOS_SESION_MAXIMA", 4*time.Hour, 10*time.Minute, 24*time.Hour)
	c.EsperaPod = l.duracion("MODELOS_ESPERA_POD", 10*time.Minute, time.Second, time.Hour)
	c.EsperaSSH = l.duracion("MODELOS_ESPERA_SSH", 10*time.Minute, time.Second, time.Hour)
	c.EsperaArranque = l.duracion("MODELOS_ESPERA_ARRANQUE", 25*time.Minute, time.Second, 2*time.Hour)
	c.EsperaModelos = l.duracion("MODELOS_ESPERA_MODELOS", 10*time.Minute, time.Second, time.Hour)
	c.Vigilancia = l.duracion("MODELOS_VIGILANCIA", 30*time.Second, 10*time.Millisecond, 10*time.Minute)
	c.DirEstado = l.ruta("MODELOS_DIR_ESTADO", "/var/lib/modelos-control")
	c.Control = l.direccion("MODELOS_PUERTO_CONTROL", "127.0.0.1:8790")
	c.EscuchaVoz = l.direccion("MODELOS_ESCUCHA_VOZ", "127.0.0.1:8766")
	c.EscuchaSTT = l.direccion("MODELOS_ESCUCHA_STT", "127.0.0.1:8767")
	c.EscuchaOllama = l.direccion("MODELOS_ESCUCHA_OLLAMA", "127.0.0.1:11434")
	c.InternoVoz = l.direccion("MODELOS_INTERNO_VOZ", "127.0.0.1:18766")
	c.InternoSTT = l.direccion("MODELOS_INTERNO_STT", "127.0.0.1:18767")
	c.InternoOllama = l.direccion("MODELOS_INTERNO_OLLAMA", "127.0.0.1:18434")
	c.ArchivoClaveRunPod = l.ruta("MODELOS_ARCHIVO_CLAVE_RUNPOD", "/etc/vera/runpod.env")
	c.ArchivoTokenControl = l.ruta("MODELOS_ARCHIVO_TOKEN_CONTROL", "/etc/vera/modelos-control.token")
	c.ArchivoTokens = l.ruta("MODELOS_ARCHIVO_TOKENS", "/opt/centinela/api/modelos.env")
	c.LlaveSSH = l.ruta("MODELOS_LLAVE_SSH", "/opt/centinela/api/tunel_ed25519")
	c.LlavePublicaSSH = l.ruta("MODELOS_LLAVE_PUBLICA_SSH", c.LlaveSSH+".pub")
	c.SSHBin = l.ruta("MODELOS_SSH_BIN", "/usr/bin/ssh")
	c.SCPBin = l.ruta("MODELOS_SCP_BIN", "/usr/bin/scp")
	c.ScriptArranque = l.ruta("MODELOS_SCRIPT_ARRANQUE", "/opt/centinela/infra-modelos-runpod/arrancar-modelos.sh")
	c.ScriptRemoto = l.ruta("MODELOS_SCRIPT_REMOTO", "/tmp/vera-arranque.sh")
	c.RepoSHA = l.texto("MODELOS_REPO_SHA", "")
	if !patronSHA.MatchString(c.RepoSHA) {
		l.fallo("MODELOS_REPO_SHA", "solo letras, digitos, punto, guion y guion bajo")
	}
	l.validarUnicas(c)
	return c, errors.Join(l.errs...)
}

func (l *lector) validarUnicas(c Config) {
	vistas := map[string]bool{}
	for _, d := range []string{c.Control, c.EscuchaVoz, c.EscuchaSTT, c.EscuchaOllama, c.InternoVoz, c.InternoSTT, c.InternoOllama} {
		if vistas[d] {
			l.fallo("direcciones", "hay direcciones repetidas: "+d)
		}
		vistas[d] = true
	}
}

// Resumen devuelve la configuración efectiva en JSON, sin leer ni imprimir ningún secreto.
func (c Config) Resumen() string {
	datos := map[string]any{
		"nombre_pod": c.NombrePod, "runpod_url": c.RunPodURL, "gpus": c.GPUs, "imagen": c.Imagen, "disco_gb": c.DiscoGB,
		"tarifa_tope_usd_h": c.TarifaTopeUSDH, "inactividad": c.Inactividad.String(), "sesion_maxima": c.SesionMaxima.String(),
		"espera_pod": c.EsperaPod.String(), "espera_ssh": c.EsperaSSH.String(), "espera_arranque": c.EsperaArranque.String(),
		"espera_modelos": c.EsperaModelos.String(), "vigilancia": c.Vigilancia.String(), "dir_estado": c.DirEstado,
		"control": c.Control, "escucha": map[string]string{"voz": c.EscuchaVoz, "stt": c.EscuchaSTT, "ollama": c.EscuchaOllama},
		"interno": map[string]string{"voz": c.InternoVoz, "stt": c.InternoSTT, "ollama": c.InternoOllama},
		"rutas_de_secretos": map[string]string{"clave_runpod": c.ArchivoClaveRunPod, "token_control": c.ArchivoTokenControl,
			"tokens_modelos": c.ArchivoTokens, "llave_ssh": c.LlaveSSH, "llave_publica_ssh": c.LlavePublicaSSH},
		"ssh_bin": c.SSHBin, "scp_bin": c.SCPBin, "script_arranque": c.ScriptArranque, "script_remoto": c.ScriptRemoto,
		"repo_sha": c.RepoSHA,
	}
	salida, err := json.MarshalIndent(datos, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(salida)
}
