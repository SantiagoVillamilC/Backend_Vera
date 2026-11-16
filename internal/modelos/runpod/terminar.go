package runpod

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Terminador termina pods con reintentos y solo da por bueno el éxito que confirma la lista:
// que no quede ningún pod vivo con el nombre indicado. Nunca toca pods con otro nombre.
type Terminador struct {
	Cliente *Cliente
	Nombre  string
	// Esperas son las pausas tras cada intento. Hay un intento más que esperas: el último solo verifica.
	Esperas []time.Duration
	Dormir  func(ctx context.Context, d time.Duration) error
	Log     *slog.Logger
}

// Resultado resume el final de una terminación.
type Resultado struct {
	Terminado bool
	Intentos  int
	Restantes []string
	Motivo    string
}

// EsperasCompletas es el calendario de terminar_pod.py: 8 esperas crecientes (5 a 120 s) y luego
// 15 más, una cada 120 s (30 minutos).
func EsperasCompletas() []time.Duration {
	esperas := []time.Duration{5, 10, 20, 40, 80, 120, 120, 120}
	for range 15 {
		esperas = append(esperas, 120)
	}
	for i := range esperas {
		esperas[i] *= time.Second
	}
	return esperas
}

// EsperasBreves es el calendario del modo reconcile: el temporizador de systemd reintenta cada 5 minutos.
func EsperasBreves() []time.Duration {
	return []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second}
}

// Dormir espera d o hasta que el contexto termine.
func Dormir(ctx context.Context, d time.Duration) error {
	temporizador := time.NewTimer(d)
	defer temporizador.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-temporizador.C:
		return nil
	}
}

// Terminar borra los pods vivos con el nombre del Terminador. Los ids explícitos se borran aunque la
// lista falle, pero sin lista no hay verificación y por eso no se declara éxito.
func (t Terminador) Terminar(ctx context.Context, ids ...string) Resultado {
	r := Resultado{}
	vivos, errLista := t.Cliente.ListarVivos(ctx, t.Nombre)
	for i := 0; ; i++ {
		if errLista == nil && len(vivos) == 0 {
			r.Terminado, r.Restantes, r.Motivo = true, nil, ""
			return r
		}
		r.Restantes, r.Motivo = idsDe(vivos), motivoPendiente(errLista)
		if i >= len(t.Esperas) || ctx.Err() != nil {
			return r
		}
		r.Intentos++
		t.borrar(ctx, objetivos(vivos, errLista, ids))
		// Verificación inmediata: si el pod ya desapareció no hace falta esperar.
		if vivos, errLista = t.Cliente.ListarVivos(ctx, t.Nombre); errLista == nil && len(vivos) == 0 {
			continue
		}
		if err := t.Dormir(ctx, t.Esperas[i]); err != nil {
			r.Motivo = "cancelado: " + err.Error()
			return r
		}
		vivos, errLista = t.Cliente.ListarVivos(ctx, t.Nombre)
	}
}

func motivoPendiente(errLista error) string {
	if errLista != nil {
		return "no se pudo verificar con la lista: " + errLista.Error()
	}
	return "siguen vivos pods con el nombre del servicio"
}

func idsDe(pods []Pod) []string {
	salida := make([]string, 0, len(pods))
	for _, p := range pods {
		salida = append(salida, p.ID)
	}
	return salida
}

// objetivos son los ids a borrar: los vivos por nombre y, si la lista falló, los explícitos.
func objetivos(vivos []Pod, errLista error, explicitos []string) []string {
	if errLista == nil {
		return idsDe(vivos)
	}
	return explicitos
}

func (t Terminador) borrar(ctx context.Context, ids []string) {
	for _, id := range ids {
		err := t.Cliente.Terminar(ctx, id)
		var api *ErrorAPI
		switch {
		case err == nil:
			t.Log.Info("DELETE enviado", "pod", id)
		case errors.As(err, &api):
			t.Log.Warn("DELETE rechazado", "pod", id, "estado", api.Estado)
		default:
			t.Log.Warn("DELETE fallo", "pod", id, "error", err.Error())
		}
	}
}
