package main

import (
	"context"
	"crypto/subtle"
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
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/custodiosim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/directoriosim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/httpserver"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/observabilidad"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/rielsim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/repositorio"
)

// tokenEstatico es el token del custodio simulado, comparado en tiempo constante.
type tokenEstatico []byte

// TokenValido reporta si el token presentado es exactamente el configurado.
func (t tokenEstatico) TokenValido(presentado string) bool {
	return subtle.ConstantTimeCompare(t, []byte(presentado)) == 1
}

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
	if err := ValidarCorridaA6(cfg); err != nil {
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

	// Rellenos de lo que aún no existe (KYC real); el directorio, el extracto del custodio y el riel son reales cuando hay URL.
	var riel nucleo.Riel = nucleo.RielNoDisponible{}
	if cfg.RielSimuladorURL != "" {
		riel = rielsim.NuevoRiel(cfg.RielSimuladorURL, nil)
	}
	var directorio nucleo.DirectorioLlaves = nucleo.DirectorioNoDisponible{}
	if cfg.DirectorioSimuladorURL != "" {
		directorio = directoriosim.NuevoDirectorio(cfg.DirectorioSimuladorURL, nil)
	}
	resolutorDestino := auth.NuevoResolutorDestino(repositorio.New(pool))
	servicio := nucleo.NewServicio(
		pool,
		nucleo.LectorNivelKYCFijo{},
		riel,
		custodiosim.NuevoLectorExtracto(cfg.CustodioExtractoURL, nil),
		observabilidad.RastreadorSlog{},
		nucleo.ConResolutorDestino(resolutorDestino),
		nucleo.ConDirectorio(directorio),
		nucleo.ConPoliticaReintentoOutbox(nucleo.PoliticaReintentoOutbox{
			Base:   cfg.OutboxBackoffBase,
			Factor: cfg.OutboxBackoffFactor,
			Tope:   cfg.OutboxBackoffMax,
		}),
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
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpserver.NewRouter(pool, auth.UsuarioDePrueba{}, servicio, tokenEstatico(cfg.CustodioToken)),
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
