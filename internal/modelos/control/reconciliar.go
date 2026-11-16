package control

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/runpod"
)

const (
	// graciaCreacion cubre un POST en vuelo: el pod existe pero el id aún no llegó al archivo de estado.
	graciaCreacion = 10 * time.Minute
	// graciaInactividad evita pisar al servicio vivo, que revisa la inactividad cada medio minuto.
	graciaInactividad = 2 * time.Minute
	graciaArranque    = 5 * time.Minute
)

// ParamsReconciliar reúne lo que necesita el subcomando reconcile, que corre sin el servicio principal.
type ParamsReconciliar struct {
	Cfg     config.Config
	Nube    *runpod.Cliente
	Almacen estado.Almacen
	Reloj   func() time.Time
	Dormir  func(ctx context.Context, d time.Duration) error
	Log     *slog.Logger
	Esperas []time.Duration
}

// Informe resume una pasada de reconcile.
type Informe struct {
	Terminados  []string
	Conservados []string
	Fallos      []string
}

// Reconciliar termina cualquier pod vera-modelos que supere la sesión máxima o la inactividad, o que no
// corresponda al estado esperado, de modo que un fallo del proceso principal no deje un pod gastando.
// Solo toca pods con el nombre del servicio. Si el servicio principal no está vivo, también corrige el estado.
func Reconciliar(ctx context.Context, p ParamsReconciliar) (Informe, error) {
	vivos, err := p.Nube.ListarVivos(ctx, p.Cfg.NombrePod)
	if err != nil {
		return Informe{}, fmt.Errorf("reconcile: no se pudo listar los pods: %w", err)
	}
	est, errEstado := p.Almacen.Cargar()
	if errEstado != nil {
		p.Log.Warn("estado ilegible, todo pod del servicio se considera sin dueno", "error", errEstado.Error())
		est = estado.Estado{Fase: estado.Apagado}
	}
	ahora := p.Reloj()
	inf, ids := clasificar(p, est, vivos, ahora)
	if len(vivos) == 0 && est.PodID != "" {
		p.corregirEstado(est, []string{est.PodID}) // el pod ya no existe: no dejar el estado en Listo
	}
	if len(ids) == 0 {
		return inf, nil
	}
	term := runpod.Terminador{Cliente: p.Nube, Nombre: p.Cfg.NombrePod, Esperas: p.Esperas, Dormir: p.Dormir, Log: p.Log}
	res := term.TerminarSolo(ctx, ids...)
	if !res.Terminado {
		inf.Fallos, inf.Terminados = res.Restantes, nil
		return inf, fmt.Errorf("reconcile: no se pudo terminar %v: %s", res.Restantes, res.Motivo)
	}
	p.corregirEstado(est, ids)
	return inf, nil
}

func clasificar(p ParamsReconciliar, est estado.Estado, vivos []runpod.Pod, ahora time.Time) (Informe, []string) {
	var inf Informe
	var ids []string
	for _, pod := range vivos {
		motivo := motivoDeTerminar(p.Cfg, est, pod, len(vivos), ahora)
		if motivo == "" {
			inf.Conservados = append(inf.Conservados, pod.ID)
			continue
		}
		p.Log.Warn("reconcile: se termina un pod", "pod", pod.ID, "motivo", motivo)
		inf.Terminados = append(inf.Terminados, pod.ID+": "+motivo)
		ids = append(ids, pod.ID)
	}
	return inf, ids
}

func motivoDeTerminar(cfg config.Config, est estado.Estado, pod runpod.Pod, total int, ahora time.Time) string {
	edad := time.Duration(0)
	if !pod.Inicio().IsZero() {
		edad = ahora.Sub(pod.Inicio())
	}
	if edad >= cfg.SesionMaxima {
		return "supera la sesion maxima"
	}
	if pod.ID != est.PodID {
		if total == 1 && est.Fase == estado.Encendiendo && est.PodID == "" && edad < graciaCreacion {
			return ""
		}
		return "pod sin dueno segun el estado"
	}
	switch est.Fase {
	case estado.Listo:
		return motivoListo(cfg, est, ahora)
	case estado.Encendiendo:
		if ahora.Sub(est.ActualizadoEn) > cfg.EsperaPod+cfg.EsperaSSH+cfg.EsperaArranque+cfg.EsperaModelos+graciaArranque {
			return "encendido atascado"
		}
		return ""
	}
	return "el estado " + string(est.Fase) + " no admite un pod vivo"
}

func motivoListo(cfg config.Config, est estado.Estado, ahora time.Time) string {
	ultima := laMasReciente(est.UltimaActividad, est.ActualizadoEn)
	if ahora.Sub(ultima) > cfg.Inactividad+graciaInactividad {
		return "inactividad sin que el servicio principal lo apague"
	}
	return ""
}

// corregirEstado actualiza el archivo solo si el servicio principal no está vivo (nadie tiene el bloqueo).
func (p ParamsReconciliar) corregirEstado(est estado.Estado, terminados []string) {
	liberar, ok, err := p.Almacen.BloquearServicio()
	if err != nil || !ok {
		return
	}
	defer liberar()
	for _, id := range terminados {
		if id == est.PodID {
			if err := p.Almacen.Guardar(estado.Estado{Fase: estado.Apagado, Mensaje: "reconcile: pod terminado", ActualizadoEn: p.Reloj()}); err != nil && !errors.Is(err, estado.ErrEstadoInvalido) {
				p.Log.Warn("no se pudo actualizar el estado", "error", err.Error())
			}
			return
		}
	}
}
