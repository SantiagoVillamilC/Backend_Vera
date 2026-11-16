package control

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
)

// Sonda comprueba por el túnel que los tres modelos responden.
type Sonda func(ctx context.Context) error

// NuevaSonda crea la sonda de los puertos internos: Ollama sin token, Whisper y Kokoro con su token.
func NuevaSonda(cfg config.Config, tokenSTT, tokenVoz string) Sonda {
	cliente := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	destinos := []struct{ nombre, dir, ruta, token string }{
		{"ollama", cfg.InternoOllama, "/api/version", ""},
		{"stt", cfg.InternoSTT, "/healthz", tokenSTT},
		{"voz", cfg.InternoVoz, "/healthz", tokenVoz},
	}
	return func(ctx context.Context) error {
		for _, d := range destinos {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+d.dir+d.ruta, nil)
			if err != nil {
				return fmt.Errorf("%s: peticion invalida", d.nombre)
			}
			if d.token != "" {
				req.Header.Set("X-Vera-Token", d.token)
			}
			res, err := cliente.Do(req)
			if err != nil {
				return fmt.Errorf("%s: sin respuesta por el tunel", d.nombre)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return fmt.Errorf("%s: HTTP %d", d.nombre, res.StatusCode)
			}
		}
		return nil
	}
}
