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
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/observabilidad"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/rielsim"
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
	// Desde acá cada línea es estructurada, incluidos los errores que run devuelve a main.
	slog.SetDefault(slog.New(logging.WithRequestID(newHandler(cfg))))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.NewPool(ctx, cfg.AppDatabaseURL)
	if err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	defer pool.Close()

	// Rellenos de lo que aún no existe (KYC real); el directorio, el extracto del custodio y el riel son reales cuando hay URL.
	var riel nucleo.Riel = nucleo.RielNoDisponible{}
	if cfg.RielSimuladorURL != "" {
		riel = rielsim.NuevoRiel(cfg.RielSimuladorURL, nil)
	}
	var directorio nucleo.DirectorioLlaves = nucleo.DirectorioNoDisponible{}
	if cfg.DirectorioSimuladorURL != "" {
		directorio = directoriosim.NuevoDirectorio(cfg.DirectorioSimuladorURL, nil)
	}
	resolutorDestino := auth.NuevoResolutorDestino(pool, cfg.Auth.EnfriamientoCambioNumero)
	servicio := nucleo.NewServicio(
		pool,
		nucleo.KYCNoDisponible{},
		nucleo.RielNoDisponible{},
		nucleo.CustodioNoDisponible{},
	)

	// Start con Background, no con ctx: cancelarlo corta los jobs en vuelo.
	riverClient, err := jobs.NewClient(pool, servicio, jobs.Config{
		OutboxInterval:       cfg.OutboxInterval,
		ConciliationInterval: cfg.ConciliationInterval,
		ConciliationWindow:   cfg.ConciliationWindow,
		ExpirationInterval:   cfg.ExpirationInterval,
		ExpirationTTL:        cfg.ExpirationTTL,
	})
	if err != nil {
		return fmt.Errorf("river: %w", err)
	}
	if err := riverClient.Start(context.Background()); err != nil {
		return fmt.Errorf("river start: %w", err)
	}

	// Fase 0: la identidad autenticada es el usuario de prueba. El swap a JWT es solo acá.
	clienteRedisAuth := clienteRedis(cfg)
	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: httpserver.NewRouter(pool, servicio, tokenEstatico(cfg.CustodioToken), httpserver.DependenciasAuth{
			Identidad: auth.UsuarioDePrueba{},
			Redis:     clienteRedisAuth,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// Debe superar authedTimeout (60s) del grupo autenticado, si no el server corta el
		// socket antes de que el Timeout del router alcance a responder un 504.
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
	// Apagado en orden: HTTP, jobs, Redis, pool.
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := riverClient.Stop(shutdownCtx); err != nil {
		return fmt.Errorf("river stop: %w", err)
	}
	if clienteRedisAuth != nil {
		if err := clienteRedisAuth.Close(); err != nil {
			return fmt.Errorf("redis close: %w", err)
		}
	}
	pool.Close()
	log.Println("shutdown complete")
	return nil
}
