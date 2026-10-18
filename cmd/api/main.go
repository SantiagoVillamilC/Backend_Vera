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
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/custodiosim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/directoriosim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/httpserver"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/observabilidad"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/rielsim"
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
	proveedorIA, err := nuevoProveedorIA(ctx, cfg.Vera)
	if err != nil {
		return fmt.Errorf("proveedor IA: %w", err)
	}

	pool, err := database.NewPool(ctx, cfg.AppDatabaseURL)
	if err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	defer pool.Close()
	consentimientos := auth.NuevoServicioDeConsentimientos(pool)
	controlIA := copilot.NuevoControlConsentimiento(consentimientos, copilot.NuevoCanceladorConsentimiento(pool))
	proveedorIA = controlIA.ProtegerProveedor(proveedorIA)

	// Rellenos de lo que aún no existe (KYC real); el directorio, el extracto del custodio y el riel son reales cuando hay URL.
	var riel nucleo.Riel = nucleo.RielNoDisponible{}
	var lectorRiel nucleo.LectorExtractoRiel = nucleo.RielNoDisponible{}
	if cfg.RielSimuladorURL != "" {
		cliente := rielsim.NuevoRiel(cfg.RielSimuladorURL, nil)
		riel = cliente
		lectorRiel = cliente
	}
	var directorio nucleo.DirectorioLlaves = nucleo.DirectorioNoDisponible{}
	if cfg.DirectorioSimuladorURL != "" {
		directorio = directoriosim.NuevoDirectorio(cfg.DirectorioSimuladorURL, nil)
	}
	resolutorDestino := auth.NuevoResolutorDestino(pool, cfg.Auth.EnfriamientoCambioNumero)
	servicio := nucleo.NewServicio(
		pool,
		lectorKYC(pool),
		riel,
		custodiosim.NuevoLectorExtracto(cfg.CustodioExtractoURL, nil),
		observabilidad.RastreadorSlog{},
		nucleo.ConResolutorDestino(resolutorDestino),
		nucleo.ConDirectorio(directorio),
		nucleo.ConLectorExtractoRiel(lectorRiel),
		nucleo.ConPoliticaReintentoOutbox(nucleo.PoliticaReintentoOutbox{
			Base:   cfg.OutboxBackoffBase,
			Factor: cfg.OutboxBackoffFactor,
			Tope:   cfg.OutboxBackoffMax,
		}),
	)

	catalogoIA, err := copilot.NuevoCatalogo(map[string]copilot.ManejadorHerramienta{
		"consultar_transferencia": copilot.ConsultaTransferenciaIA(servicio),
	}, slog.Default())
	if err != nil {
		return fmt.Errorf("catálogo IA: %w", err)
	}

	// Fase 0: la identidad autenticada es el usuario de prueba. El swap a JWT es solo acá.
	clienteRedisAuth := clienteRedis(cfg)
	canal, err := canalOTP(cfg)
	if err != nil {
		return fmt.Errorf("canal OTP: %w", err)
	}
	contadoresAuth, err := contadoresDeAuth(clienteRedisAuth)
	if err != nil {
		return fmt.Errorf("contadores de auth: %w", err)
	}
	clavesContadores, err := auth.NuevasClaves(cfg.Auth.ContadoresPepper)
	if err != nil {
		return fmt.Errorf("claves de contadores: %w", err)
	}
	lectorOptIn := auth.NuevoLectorOptInDeConsentimientos(pool)
	gestorDesafios, err := auth.NuevoGestorDesafios(pool, canal, auth.NuevoRegistradorEventos(pool), lectorOptIn, cfg.Auth.OTPPepper,
		auth.ConRacionamiento(contadoresAuth, clavesContadores))
	if err != nil {
		return fmt.Errorf("gestor de desafíos: %w", err)
	}
	gestorEstados := auth.NuevoGestorDeEstados(auth.NuevoRegistradorEventos(pool))
	activador, err := auth.NuevoActivadorDeCuenta(pool, gestorDesafios, gestorEstados, cfg.Auth.OTPPepper)
	if err != nil {
		return fmt.Errorf("activador de cuenta: %w", err)
	}
	derivador, err := derivadorDeContrasenas(cfg)
	if err != nil {
		return err
	}
	verificador, err := auth.NuevoVerificadorDeCredencialesPostgres(pool, derivador, contadoresAuth, clavesContadores, auth.NuevoRegistradorEventos(pool), auth.ParametrosArgon2Produccion())
	if err != nil {
		return fmt.Errorf("verificador de credenciales: %w", err)
	}
	gestorSesiones := auth.NuevoGestorSesiones(pool, auth.NuevoRegistradorEventos(pool))
	tokensAuth, err := tokensDeAcceso(cfg)
	if err != nil {
		return fmt.Errorf("tokens de acceso: %w", err)
	}
	servicioLogin, err := auth.NuevoServicioDeLogin(verificador, pool, gestorSesiones, tokensAuth)
	if err != nil {
		return fmt.Errorf("servicio de login: %w", err)
	}
	denylistAuth, err := denylistDeSesiones(clienteRedisAuth)
	if err != nil {
		return fmt.Errorf("denylist de sesiones: %w", err)
	}
	identidadPeticion, err := identidadJWT(cfg, denylistAuth)
	if err != nil {
		return fmt.Errorf("identidad JWT: %w", err)
	}
	servicioSesion, err := auth.NuevoServicioDeSesion(pool, gestorSesiones, tokensAuth, denylistAuth)
	if err != nil {
		return fmt.Errorf("servicio de sesión: %w", err)
	}
	servicioRecuperacion, err := auth.NuevoServicioDeRecuperacion(pool, gestorDesafios, auth.NuevoRegistradorEventos(pool), contadoresAuth, clavesContadores, cfg.Auth.OTPPepper)
	if err != nil {
		return fmt.Errorf("servicio de recuperación: %w", err)
	}
	servicioRestablecimiento, err := auth.NuevoServicioDeRestablecimiento(pool, gestorDesafios, gestorSesiones, derivador, denylistAuth, auth.NuevoRegistradorEventos(pool), lectorOptIn, contadoresAuth, clavesContadores, cfg.Auth.OTPPepper, cfg.Auth.ResetTokenTTL)
	if err != nil {
		return fmt.Errorf("servicio de restablecimiento: %w", err)
	}
	servicioCambioNumero, err := auth.NuevoServicioDeCambioDeNumero(pool, gestorDesafios, gestorSesiones, derivador, denylistAuth, auth.NuevoRegistradorEventos(pool), lectorOptIn, contadoresAuth, clavesContadores, cfg.Auth.OTPPepper, cfg.Auth.ContactoSoporte, cfg.Auth.EnfriamientoCambioNumero)
	if err != nil {
		return fmt.Errorf("servicio de cambio de número: %w", err)
	}
	restablecimiento := httpserver.NuevoRestablecimientoHandler(servicioRestablecimiento, servicioRestablecimiento)
	handlerKYC, err := nuevoKYCHandler(pool, cfg, servicio)
	if err != nil {
		return fmt.Errorf("módulo KYC: %w", err)
	}
	deps := httpserver.DependenciasAuth{
		Identidad:     identidadPeticion,
		Auth:          httpserver.NuevoDispositivosHandler(servicioSesion),
		KYC:           handlerKYC,
		Consent:       httpserver.NuevoConsentimientosHandler(consentimientos),
		Redis:         clienteRedisAuth,
		CanalOTP:      canal,
		Contadores:    contadoresAuth,
		Destinatarios: httpserver.NuevoDestinatariosHandler(resolutorDestino, contadoresAuth, clavesContadores, cfg.Auth.DestinatariosLimite, cfg.Auth.DestinatariosVentana),
		Desafios:      httpserver.NuevoOTPHandler(gestorDesafios),
		Verificacion:  httpserver.NuevoVerificacionHandler(activador),
		Login:         httpserver.NuevoLoginHandler(servicioLogin),
		Refresh:       httpserver.NuevoSesionHandler(servicioSesion),
		Recuperacion:  httpserver.NuevoRecuperacionHandler(servicioRecuperacion),
		Recuperar2FA:  restablecimiento,
		ResetClave:    restablecimiento,
		PhoneChange:   httpserver.NuevoCambioDeNumeroHandler(servicioCambioNumero),
		Firmador:      tokensAuth,
		Denylist:      denylistAuth,
		Subcuentas:    servicio,
	}
	rellenoRegistro, err := auth.NuevoRellenoDeRegistro(pool, auth.NuevoRegistradorEventos(pool), contadoresAuth, clavesContadores, cfg.Auth.OTPPepper)
	if err != nil {
		return fmt.Errorf("relleno del registro: %w", err)
	}
	registrador, err := auth.NuevoRegistradorDePersonas(pool, derivador, auth.NuevoRegistradorEventos(pool), gestorDesafios, deps.Subcuentas, auth.ConRellenoDeRegistro(rellenoRegistro))
	if err != nil {
		return fmt.Errorf("registrador de personas: %w", err)
	}
	deps.Registro = httpserver.NuevoRegistroHandler(registrador)
	deps.Companies = httpserver.NuevoEmpresasHandler(registrador)

	opcionOutbox, despachadorAvisos := opcionOutboxDeAuth(pool, canal, cfg)
	despachadorAvisos.Registrar(auth.TipoAvisoAbrirSubcuentas, auth.ManejadorAbrirSubcuentas(deps.Subcuentas))

	// Start con Background, no con ctx: cancelarlo corta los jobs en vuelo.
	riverClient, err := jobs.NewClient(pool, servicio, jobs.Config{
		OutboxInterval:       cfg.OutboxInterval,
		ConciliationInterval: cfg.ConciliationInterval,
		ConciliationWindow:   cfg.ConciliationWindow,
		ExpirationInterval:   cfg.ExpirationInterval,
		ExpirationTTL:        cfg.ExpirationTTL,
	}, opcionLimpiezaDeAuth(pool, cfg), opcionOutbox)
	if err != nil {
		return fmt.Errorf("river: %w", err)
	}
	if err := riverClient.Start(context.Background()); err != nil {
		return fmt.Errorf("river start: %w", err)
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           httpserver.NewRouterConVera(pool, servicio, tokenEstatico(cfg.CustodioToken), deps, httpserver.DependenciasVera{Proveedor: proveedorIA, Catalogo: catalogoIA, Consentimiento: controlIA}, cfg.PSEURL),
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
