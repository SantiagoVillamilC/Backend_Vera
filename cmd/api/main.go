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

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/httpserver"
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

	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpserver.NewRouter(pool, cfg.JWTSecret),
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
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	pool.Close()
	log.Println("shutdown complete")
	return nil
}
