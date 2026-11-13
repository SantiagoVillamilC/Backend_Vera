package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// DependenciasDeModelos contiene únicamente URLs configuradas por el operador.
type DependenciasDeModelos struct{ Voz, STT, Ollama string }

// EstadoDeModelo informa salud sin exponer URLs ni errores internos.
type EstadoDeModelo struct {
	Estado string `json:"estado"`
	Motivo string `json:"motivo,omitempty"`
}

// OpcionDeSalud configura las sondas opcionales.
type OpcionDeSalud func(*HealthResource)

// ConModelos configura dependencias sin agregarlas a Ready.
func ConModelos(d DependenciasDeModelos) OpcionDeSalud {
	return func(r *HealthResource) { r.modelos = d }
}

// Modelos comprueba las URLs configuradas en paralelo, con dos segundos por dependencia.
func (rs HealthResource) Modelos(w http.ResponseWriter, r *http.Request) {
	cliente := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	nombres := []string{"voz", "stt", "ollama"}
	urls := []string{rs.modelos.Voz, rs.modelos.STT, rs.modelos.Ollama}
	estados := make([]EstadoDeModelo, 3)
	var wg sync.WaitGroup
	for i, u := range urls {
		if u == "" {
			estados[i] = EstadoDeModelo{Estado: "no configurada"}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			estados[i] = EstadoDeModelo{Estado: "falla", Motivo: "dependencia no disponible"}
			req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if e != nil {
				return
			}
			res, e := cliente.Do(req)
			if e != nil {
				return
			}
			_ = res.Body.Close()
			if res.StatusCode < 500 {
				estados[i] = EstadoDeModelo{Estado: "ok"}
			}
		}()
	}
	wg.Wait()
	datos := map[string]EstadoDeModelo{}
	sano := true
	for i, n := range nombres {
		datos[n] = estados[i]
		if estados[i].Estado == "falla" {
			sano = false
		}
	}
	status := 200
	var fallo any
	if !sano {
		status = 503
		fallo = map[string]string{"code": "NotReady", "message": "One or more model dependencies are unavailable."}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Success bool                      `json:"success"`
		Data    map[string]EstadoDeModelo `json:"data"`
		Error   any                       `json:"error"`
		Meta    any                       `json:"meta"`
	}{sano, datos, fallo, nil})
}
