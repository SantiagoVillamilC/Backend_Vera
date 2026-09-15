package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/httpserver"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/observabilidad"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	// Desde acá el log es estructurado.
	slog.SetDefault(slog.New(logging.WithRequestID(newHandler(cfg))))
	slog.Info("config loaded", "config", cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, cfg.AppDatabaseURL)
	if err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	defer pool.Close()

	// Rellenos de lo que aún no existe (riel NF-A2-06, custodio NF-A5-01).
	servicio := nucleo.NewServicio(
		pool,
		nucleo.KYCNoDisponible{},
		nucleo.RielNoDisponible{},
		nucleo.CustodioNoDisponible{},
		observabilidad.RastreadorSlog{},
	)

	// Start con Background, no con ctx: cancelarlo corta los jobs en vuelo.
	riverClient, err := jobs.NewClient(pool, servicio, jobs.Config{
		OutboxInterval:       cfg.OutboxInterval,
		ConciliationInterval: cfg.ConciliationInterval,
		ConciliationWindow:   cfg.ConciliationWindow,
	})
	if err != nil {
		return fmt.Errorf("river: %w", err)
	}
	if err := riverClient.Start(context.Background()); err != nil {
		return fmt.Errorf("river start: %w", err)
	}

	// Fase 0: la identidad autenticada es el usuario de prueba. El swap a JWT es solo acá.
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpserver.NewRouter(pool, auth.UsuarioDePrueba{}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// Debe superar authedTimeout (60s) del router o el socket corta antes del 504.
		WriteTimeout: 90 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	log.Printf("API listening on :%s", cfg.Port)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Apagado en orden: HTTP, jobs, pool.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := riverClient.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("river stop: %w", err)
	}
	pool.Close()
	log.Println("shutdown complete")
	return nil
}
