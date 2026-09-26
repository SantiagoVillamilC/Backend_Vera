package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// intervaloDespacho es la cadencia con la que el worker reintenta las patas reprogramadas.
const intervaloDespacho = time.Second

func main() {
	if err := run(); err != nil {
		log.Fatalf("custodio-simulado: %v", err)
	}
}

// run arma el almacén, el despachador y el servidor del PSE simulado, con su apagado ordenado.
func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	slog.Info("config loaded", "config", cfg)

	almacen, err := AbrirAlmacen(cfg.DBPath)
	if err != nil {
		return err
	}

	cliente := &http.Client{Timeout: 10 * time.Second}
	despachador := NuevoDespachador(almacen,
		clienteNucleo{url: cfg.NucleoConfirmacionURL, token: cfg.CustodioToken, cliente: cliente},
		clienteCustodio{url: cfg.SimuladorCustodiaURL, cliente: cliente},
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	despachoListo := make(chan struct{})
	go func() {
		defer close(despachoListo)
		despachador.Correr(ctx, intervaloDespacho)
	}()
	defer func() {
		stop()
		<-despachoListo
		_ = almacen.Cerrar()
	}()

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.Addr, cfg.Port),
		Handler:           NewPaginaPSE(despachador).Rutas(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	slog.Info("custodio simulado escuchando", "addr", srv.Addr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}
