// Command modelos-control enciende y apaga bajo demanda el pod de RunPod con los modelos de Vera
// (Ollama, Whisper y Kokoro), abre el túnel SSH y expone los puertos locales que espera el backend.
//
// Uso:
//
//	modelos-control [--seco] [servir]   proceso principal (por defecto)
//	modelos-control reconcile           termina pods vencidos, huérfanos o duplicados (temporizador de systemd)
//
// Toda la configuración llega por variables MODELOS_* (ver internal/modelos/config). Con --seco se
// imprime la configuración efectiva sin tocar la red ni leer secretos.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/acceso"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/control"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/proxy"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/runpod"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

const (
	salidaUso       = 64
	salidaConfig    = 2
	tiempoReconcile = 4 * time.Minute
)

func main() {
	ctx, parar := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := ejecutar(ctx, os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr)
	parar()
	os.Exit(code)
}

// opciones permite sustituir las esperas en pruebas.
type opciones struct {
	dormir func(ctx context.Context, d time.Duration) error
}

func ejecutar(ctx context.Context, args []string, buscar func(string) (string, bool), salida, errSalida io.Writer) int {
	return ejecutarCon(ctx, args, buscar, salida, errSalida, opciones{dormir: runpod.Dormir})
}

func ejecutarCon(ctx context.Context, args []string, buscar func(string) (string, bool), salida, errSalida io.Writer, op opciones) int {
	seco, sub, err := leerArgumentos(args)
	if err != nil {
		_, _ = fmt.Fprintln(errSalida, "modelos-control:", err)
		return salidaUso
	}
	cfg, err := config.Cargar(buscar)
	if err != nil {
		_, _ = fmt.Fprintln(errSalida, "modelos-control: configuracion invalida:\n"+err.Error())
		return salidaConfig
	}
	if seco {
		_, _ = fmt.Fprintln(salida, cfg.Resumen())
		return 0
	}
	if sub == "reconcile" {
		return reconcile(ctx, cfg, buscar, salida, errSalida, op)
	}
	return servir(ctx, cfg, buscar, errSalida, op)
}

func leerArgumentos(args []string) (seco bool, sub string, err error) {
	sub = "servir"
	for _, a := range args {
		switch a {
		case "--seco":
			seco = true
		case "servir", "reconcile":
			sub = a
		default:
			return false, "", fmt.Errorf("argumento desconocido %q (usa servir, reconcile o --seco)", a)
		}
	}
	return seco, sub, nil
}

type secretos struct {
	clave, stt, voz, control, llavePublica string
}

func leerSecretos(cfg config.Config, buscar func(string) (string, bool), completo bool) (secretos, error) {
	clave, err := config.LeerClaveRunPod(cfg.ArchivoClaveRunPod, buscar)
	if err != nil {
		return secretos{}, err
	}
	s := secretos{clave: clave}
	if !completo {
		return s, nil
	}
	if s.stt, s.voz, err = config.LeerTokensModelos(cfg.ArchivoTokens); err != nil {
		return secretos{}, err
	}
	if s.control, err = config.LeerTokenControl(cfg.ArchivoTokenControl); err != nil {
		return secretos{}, err
	}
	if s.llavePublica, err = config.LeerLlavePublica(cfg.LlavePublicaSSH); err != nil {
		return secretos{}, err
	}
	return s, nil
}

// escritorOculto es la última defensa: aunque un mensaje arrastrara un secreto, no sale al registro.
type escritorOculto struct {
	w io.Writer
	r secreto.Redactor
}

func (e escritorOculto) Write(p []byte) (int, error) {
	if _, err := io.WriteString(e.w, e.r.Ocultar(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

func nuevoLog(w io.Writer, r secreto.Redactor) *slog.Logger {
	return slog.New(slog.NewJSONHandler(escritorOculto{w: w, r: r}, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func reconcile(ctx context.Context, cfg config.Config, buscar func(string) (string, bool), salida, errSalida io.Writer, op opciones) int {
	sec, err := leerSecretos(cfg, buscar, false)
	if err != nil {
		_, _ = fmt.Fprintln(errSalida, "modelos-control reconcile:", err)
		return 1
	}
	red := secreto.Nuevo(sec.clave)
	log := nuevoLog(errSalida, red)
	cliente, err := runpod.Nuevo(runpod.Config{BaseURL: cfg.RunPodURL, Clave: sec.clave})
	almacen, errAlmacen := estado.NuevoAlmacen(cfg.DirEstado)
	if err != nil || errAlmacen != nil {
		_, _ = fmt.Fprintln(errSalida, "modelos-control reconcile: no se pudo preparar el cliente o el directorio de estado")
		return 1
	}
	ctx, cancelar := context.WithTimeout(ctx, tiempoReconcile)
	defer cancelar()
	inf, err := control.Reconciliar(ctx, control.ParamsReconciliar{Cfg: cfg, Nube: cliente, Almacen: almacen, Reloj: time.Now,
		Dormir: op.dormir, Log: log, Esperas: runpod.EsperasBreves()})
	for _, t := range inf.Terminados {
		_, _ = fmt.Fprintln(salida, "terminado:", red.Limpiar(t))
	}
	for _, c := range inf.Conservados {
		_, _ = fmt.Fprintln(salida, "conservado:", c)
	}
	if err != nil {
		_, _ = fmt.Fprintln(errSalida, "modelos-control reconcile:", red.Limpiar(err.Error()))
		return 1
	}
	return 0
}

func servir(ctx context.Context, cfg config.Config, buscar func(string) (string, bool), errSalida io.Writer, op opciones) int {
	sec, err := leerSecretos(cfg, buscar, true)
	if err != nil {
		_, _ = fmt.Fprintln(errSalida, "modelos-control:", err)
		return 1
	}
	red := secreto.Nuevo(sec.clave, sec.stt, sec.voz, sec.control)
	log := nuevoLog(errSalida, red)
	almacen, err := estado.NuevoAlmacen(cfg.DirEstado)
	if err != nil {
		log.Error("no se pudo preparar el directorio de estado", "error", err.Error())
		return 1
	}
	liberar, ok, err := almacen.BloquearServicio()
	if err != nil || !ok {
		log.Error("otra instancia de modelos-control ya esta en ejecucion o el bloqueo fallo")
		return 1
	}
	defer liberar()
	svc, err := construirServicio(cfg, sec, red, log, almacen, op)
	if err != nil {
		log.Error("no se pudo construir el servicio", "error", red.Limpiar(err.Error()))
		return 1
	}
	defer svc.Cerrar()
	return correr(ctx, cfg, svc, sec.control, log)
}

func construirServicio(cfg config.Config, sec secretos, red secreto.Redactor, log *slog.Logger, almacen estado.Almacen, op opciones) (*control.Servicio, error) {
	cliente, err := runpod.Nuevo(runpod.Config{BaseURL: cfg.RunPodURL, Clave: sec.clave})
	if err != nil {
		return nil, err
	}
	ssh := acceso.Nuevo(acceso.Config{SSHBin: cfg.SSHBin, SCPBin: cfg.SCPBin, Llave: cfg.LlaveSSH, Huellas: almacen, Log: log, Redactor: red})
	return control.Nuevo(control.Dependencias{Cfg: cfg, Nube: cliente, Acceso: ssh, Almacen: almacen, Actividad: proxy.NuevaActividad(time.Now),
		Sonda: control.NuevaSonda(cfg, sec.stt, sec.voz), Dormir: op.dormir, Log: log, Redactor: red, LlavePublica: sec.llavePublica,
		TokenSTT: sec.stt, TokenVoz: sec.voz})
}

func correr(ctx context.Context, cfg config.Config, svc *control.Servicio, tokenControl string, log *slog.Logger) int {
	actividad := svc.Actividad()
	proxies, err := abrirProxies(cfg, svc, actividad, log)
	if err != nil {
		log.Error("no se pudo abrir un puerto local", "error", err.Error())
		return 1
	}
	srvControl := control.ServidorControl(cfg.Control, control.ManejadorControl(svc, tokenControl, log))
	lnControl, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Control)
	if err != nil {
		cerrarProxies(proxies)
		log.Error("no se pudo abrir la API de control", "error", err.Error())
		return 1
	}
	fallo := make(chan error, len(proxies)+1)
	for _, p := range proxies {
		go func() { fallo <- p.Servir() }()
	}
	go func() { fallo <- aErrorServidor(srvControl.Serve(lnControl)) }()
	go svc.Vigilar(ctx)
	if err := svc.Recuperar(ctx); err != nil {
		log.Error("la reconciliacion inicial fallo, el estado queda en error", "error", err.Error())
	}
	log.Info("modelos-control en marcha", "control", cfg.Control)
	codigo := 0
	select {
	case <-ctx.Done():
	case err := <-fallo:
		log.Error("un servidor se detuvo", "error", fmt.Sprint(err))
		codigo = 1
	}
	apagarServidores(srvControl, proxies)
	return codigo
}

func aErrorServidor(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func abrirProxies(cfg config.Config, svc *control.Servicio, act *proxy.Actividad, log *slog.Logger) ([]*proxy.Servidor, error) {
	pares := []struct{ escucha, interno string }{{cfg.EscuchaOllama, cfg.InternoOllama}, {cfg.EscuchaSTT, cfg.InternoSTT}, {cfg.EscuchaVoz, cfg.InternoVoz}}
	var abiertos []*proxy.Servidor
	for _, p := range pares {
		destino := &url.URL{Scheme: "http", Host: p.interno}
		s, err := proxy.Escuchar(p.escucha, proxy.Manejador(proxy.Opciones{Destino: destino, Actividad: act, Disponible: svc.Disponible, Log: log}))
		if err != nil {
			cerrarProxies(abiertos)
			return nil, err
		}
		abiertos = append(abiertos, s)
	}
	return abiertos, nil
}

func cerrarProxies(ps []*proxy.Servidor) {
	ctx, cancelar := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelar()
	for _, p := range ps {
		p.Cerrar(ctx)
	}
}

func apagarServidores(control *http.Server, proxies []*proxy.Servidor) {
	ctx, cancelar := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelar()
	_ = control.Shutdown(ctx)
	for _, p := range proxies {
		p.Cerrar(ctx)
	}
}
