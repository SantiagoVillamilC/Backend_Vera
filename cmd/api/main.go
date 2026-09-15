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

	// El núcleo se construye con sus dependencias reales y con los rellenos de las piezas que
	// todavía no existen (riel NF-A2-06, custodio NF-A5-01). Sus métodos de negocio los agregan
	// las unidades posteriores.
	servicio := nucleo.NewServicio(
		pool,
		nucleo.KYCNoDisponible{},
		nucleo.RielNoDisponible{},
		nucleo.CustodioNoDisponible{},
	)

	// River dispara DespacharOutbox y Conciliar sobre el mismo pool y el mismo núcleo. Start con
	// context.Background(), no con ctx: cancelar el contexto de Start es un apagado duro que
	// cancela los jobs en vuelo. El apagado ordenado va por Stop, después del drenado de HTTP.
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

	// Fase 0: la identidad del grupo autenticado es el adaptador de usuario de prueba. El
	// swap a JWT real es construir acá otro IdentidadDeLaPeticion con cfg.JWTSecret; el router
	// y los handlers no cambian.
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpserver.NewRouter(pool, auth.UsuarioDePrueba{}),
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
	if err := srv.Shutdown(shutdownCtx); err != nil { // 1. deja de aceptar y drena la petición en vuelo
		return fmt.Errorf("http shutdown: %w", err)
	}
	if err := riverClient.Stop(shutdownCtx); err != nil { // 2. deja de tomar jobs y termina los que corren
		return fmt.Errorf("river stop: %w", err)
	}
	pool.Close() // 3. último: los dos pasos anteriores todavía necesitan conexiones
	log.Println("shutdown complete")
	return nil
}
