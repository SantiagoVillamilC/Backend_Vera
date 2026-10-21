// Command laboratorio-vera sirve el sandbox local de texto y voz.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/laboratorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/vozlocal"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8787", "dirección de escucha; en Docker usar 0.0.0.0:8787 con publicación sólo local")
	demo := flag.Bool("demo", false, "proveedor y transcripción simulados; sin llamadas a Gemini")
	maximo := flag.Int("max-solicitudes", 120, "máximo total de solicitudes físicas: chat, transcripción, voz y reintentos; no equivale a turnos")
	intervalo := flag.Duration("intervalo", 0, "pausa opcional entre llamadas; el laboratorio interactivo no añade espera")
	confirmado := flag.Bool("nivel-gratuito-confirmado", false, "confirma el proyecto y modelo Free Tier")
	flag.Parse()
	if flag.NArg() != 0 || *maximo < 1 || *maximo > 300 || *intervalo < 0 || *intervalo > time.Minute {
		return errors.New("opciones inválidas; usa -h para ver la ayuda")
	}
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || (host != "127.0.0.1" && host != "0.0.0.0") {
		return errors.New("usa una dirección local, o 0.0.0.0 dentro de Docker con el puerto publicado en 127.0.0.1")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	modelo := os.Getenv("VERA_MODELO")
	var p copilot.ProveedorIA
	var voz copilot.SintetizadorVoz
	if *demo {
		modelo = "Demo local"
		*confirmado = true
		*intervalo = 0
		p = laboratorio.ProveedorDemo{}
	} else {
		if !*confirmado || modelo == "" || os.Getenv("CI") != "" {
			return errors.New("configura VERA_MODELO y confirma Free Tier; el modo real no se ejecuta en CI")
		}
		p, err = gemini.Nuevo(ctx, gemini.Config{APIKey: os.Getenv("GEMINI_API_KEY"), Modelo: modelo, Timeout: 25 * time.Second, TemperaturaPredeterminada: true, SinReintentos: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			return errors.New("no se pudo configurar Gemini; revisa GEMINI_API_KEY y VERA_MODELO sin compartir la clave")
		}
		voz, err = gemini.NuevaVoz(os.Getenv("GEMINI_API_KEY"))
		if err != nil {
			return errors.New("no se pudo configurar la voz de Gemini")
		}
	}
	token := os.Getenv("VERA_LAB_TOKEN")
	if token == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return errors.New("no se pudo crear la sesión local")
		}
		token = hex.EncodeToString(b)
	}
	if len(token) < 32 || len(token) > 128 {
		return errors.New("el token de sesión local debe tener entre 32 y 128 caracteres")
	}
	var opciones []laboratorio.Opcion
	if endpoint := os.Getenv("VERA_VOZ_LOCAL_URL"); endpoint != "" {
		local, err := vozlocal.Nueva(endpoint, token)
		if err != nil {
			return errors.New("configuración de voz local inválida; usa loopback y token de sesión")
		}
		opciones = append(opciones, laboratorio.ConVozLocal(local))
	}
	var escucha net.ListenConfig
	l, err := escucha.Listen(ctx, "tcp", *addr)
	if err != nil {
		return errors.New("no se pudo abrir el puerto; cierra el laboratorio anterior o usa otro puerto")
	}
	defer func() { _ = l.Close() }()
	_, puerto, _ := net.SplitHostPort(l.Addr().String())
	h, err := laboratorio.Nuevo(evaluacion.ConfigReal{Modelo: modelo, Repeticiones: 1, MaxSolicitudes: *maximo, Intervalo: *intervalo, NivelGratuitoConfirmado: *confirmado}, p, token, "localhost:"+puerto, *demo, voz, opciones...)
	if err != nil {
		return errors.New("no se pudo iniciar la sesión del laboratorio")
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 95 * time.Second, IdleTimeout: time.Minute, BaseContext: func(net.Listener) context.Context { return ctx }}
	_, _ = fmt.Fprintf(os.Stdout, "\nVERA | Laboratorio local de texto y voz\nDatos financieros ficticios. Máximo %d llamadas por sesión.\nAbre este enlace completo:\nhttp://localhost:%s/#token=%s\n\nMantén esta ventana abierta. Ctrl+C para cerrar.\n", *maximo, puerto, token)
	fin := make(chan error, 1)
	go func() { fin <- srv.Serve(l) }()
	select {
	case err := <-fin:
		if !errors.Is(err, http.ErrServerClosed) {
			return errors.New("el servidor local se interrumpió")
		}
		return nil
	case <-ctx.Done():
		stop()
		cierre, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(cierre)
	}
}
