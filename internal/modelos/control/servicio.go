// Package control es la máquina de estados de los modelos: enciende y apaga el pod de RunPod, mantiene
// el túnel, apaga por inactividad y reconcilia pods huérfanos.
package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/acceso"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/estado"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/proxy"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/runpod"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

// Errores que la API de control traduce a códigos HTTP.
var (
	ErrApagando       = errors.New("el pod se esta apagando, espera a que termine")
	ErrRequiereApagar = errors.New("hay un pod en estado de error: apaga antes de volver a encender")
	ErrConfiguracion  = errors.New("configuracion incompleta")
)

// Acceso es lo que el servicio necesita de ssh. *acceso.SSH lo cumple.
type Acceso interface {
	Comprobar(ctx context.Context, d acceso.Destino) error
	Subir(ctx context.Context, d acceso.Destino, local, remoto string) error
	Ejecutar(ctx context.Context, d acceso.Destino, comando string, stdin io.Reader) error
	AbrirTunel(ctx context.Context, d acceso.Destino, r []acceso.Reenvio, alFatal func(error)) *acceso.Tunel
}

// Dependencias reúne todo lo externo, para poder inyectar relojes y dobles en las pruebas.
type Dependencias struct {
	Cfg             config.Config
	Nube            *runpod.Cliente
	Acceso          Acceso
	Almacen         estado.Almacen
	Actividad       *proxy.Actividad
	Sonda           Sonda
	Reloj           func() time.Time
	Dormir          func(ctx context.Context, d time.Duration) error
	Log             *slog.Logger
	Redactor        secreto.Redactor
	LlavePublica    string
	TokenSTT        string
	TokenVoz        string
	EsperasTerminar []time.Duration
	IntervaloSondeo time.Duration
}

// Vista es lo que informa GET /estado.
type Vista struct {
	Estado                            string   `json:"estado"`
	PodID                             string   `json:"pod_id,omitempty"`
	GPU                               string   `json:"gpu,omitempty"`
	TarifaUSDH                        float64  `json:"tarifa_usd_h"`
	MinutosEncendido                  float64  `json:"minutos_encendido"`
	MinutosHastaApagadoPorInactividad *float64 `json:"minutos_hasta_apagado_por_inactividad"`
	MinutosHastaSesionMaxima          *float64 `json:"minutos_hasta_sesion_maxima"`
	CostoEstimadoUSD                  float64  `json:"costo_estimado_usd"`
	Mensaje                           string   `json:"mensaje,omitempty"`
}

type trabajo struct {
	cancelar context.CancelFunc
	hecho    chan struct{}
}

// Servicio es la máquina de estados. Todos sus métodos son seguros para uso concurrente.
type Servicio struct {
	d       Dependencias
	term    runpod.Terminador
	base    context.Context
	cerrar  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	est     estado.Estado
	tunel   *acceso.Tunel
	trabajo *trabajo
}

// Nuevo valida las dependencias y carga el estado persistido. No toca la red: llama a Recuperar para
// reconciliar con RunPod.
func Nuevo(d Dependencias) (*Servicio, error) {
	if d.Nube == nil || d.Acceso == nil || d.Actividad == nil || d.Sonda == nil || d.Log == nil {
		return nil, errors.New("control: faltan dependencias")
	}
	if d.Reloj == nil {
		d.Reloj = time.Now
	}
	if d.Dormir == nil {
		d.Dormir = runpod.Dormir
	}
	if d.IntervaloSondeo <= 0 {
		d.IntervaloSondeo = 5 * time.Second
	}
	if d.EsperasTerminar == nil {
		d.EsperasTerminar = runpod.EsperasCompletas()
	}
	base, cerrar := context.WithCancel(context.Background())
	s := &Servicio{d: d, base: base, cerrar: cerrar,
		term: runpod.Terminador{Cliente: d.Nube, Nombre: d.Cfg.NombrePod, Esperas: d.EsperasTerminar, Dormir: d.Dormir, Log: d.Log}}
	est, err := d.Almacen.Cargar()
	if err != nil {
		d.Log.Error("estado ilegible, se reconciliara con RunPod", "error", d.Redactor.Limpiar(err.Error()))
		est = estado.Estado{Fase: estado.Apagado, Mensaje: "estado ilegible, reconciliado con RunPod"}
	}
	s.est = est
	return s, nil
}

// Cerrar cancela los trabajos en curso. NO termina el pod: el estado queda en disco y el próximo
// arranque (o el reconcile) decide.
func (s *Servicio) Cerrar() {
	s.cerrar()
	s.wg.Wait()
	s.cerrarTunel()
}

func (s *Servicio) guardarLocked() {
	s.est.ActualizadoEn = s.d.Reloj()
	if err := s.d.Almacen.Guardar(s.est); err != nil {
		s.d.Log.Error("no se pudo guardar el estado", "error", s.d.Redactor.Limpiar(err.Error()))
	}
}

func (s *Servicio) cerrarTunel() {
	s.mu.Lock()
	t := s.tunel
	s.tunel = nil
	s.mu.Unlock()
	if t != nil {
		t.Cerrar()
	}
}

// Actividad devuelve el contador de actividad que comparten los proxys.
func (s *Servicio) Actividad() *proxy.Actividad { return s.d.Actividad }

// Disponible dice si el proxy puede reenviar: pod listo y túnel vivo.
func (s *Servicio) Disponible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.est.Fase == estado.Listo && s.tunel != nil && s.tunel.Vivo()
}

// Vista devuelve una instantánea para GET /estado.
func (s *Servicio) Vista() Vista {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.vistaLocked()
}

func minutos(d time.Duration) float64 { return d.Minutes() }

func (s *Servicio) vistaLocked() Vista {
	v := Vista{Estado: string(s.est.Fase), PodID: s.est.PodID, GPU: s.est.GPU, TarifaUSDH: s.est.TarifaUSDH, Mensaje: s.est.Mensaje}
	if s.est.PodID == "" || s.est.SesionInicio.IsZero() {
		return v
	}
	ahora := s.d.Reloj()
	encendido := max(ahora.Sub(s.est.SesionInicio), 0)
	v.MinutosEncendido = minutos(encendido)
	v.CostoEstimadoUSD = s.est.TarifaUSDH * encendido.Hours()
	restante := minutos(max(s.d.Cfg.SesionMaxima-encendido, 0))
	v.MinutosHastaSesionMaxima = &restante
	if s.est.Fase == estado.Listo {
		hasta := minutos(s.d.Cfg.Inactividad)
		if s.d.Actividad.EnVuelo() == 0 {
			ultima := laMasReciente(s.est.UltimaActividad, s.d.Actividad.Ultima())
			hasta = minutos(max(s.d.Cfg.Inactividad-ahora.Sub(ultima), 0))
		}
		v.MinutosHastaApagadoPorInactividad = &hasta
	}
	return v
}

func laMasReciente(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// Encender inicia el encendido. Devuelve true si inició uno nuevo y false si ya estaba en marcha o listo.
func (s *Servicio) Encender() (Vista, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.est.Fase {
	case estado.Listo:
		s.registrarActividadLocked()
		return s.vistaLocked(), false, nil
	case estado.Encendiendo:
		return s.vistaLocked(), false, nil
	case estado.Apagando:
		return Vista{}, false, ErrApagando
	case estado.Error:
		if s.est.PodID != "" {
			return Vista{}, false, ErrRequiereApagar
		}
	}
	if info, err := os.Stat(s.d.Cfg.ScriptArranque); err != nil || !info.Mode().IsRegular() {
		return Vista{}, false, fmt.Errorf("%w: falta el script de arranque de los modelos", ErrConfiguracion)
	}
	s.est = estado.Estado{Fase: estado.Encendiendo}
	s.guardarLocked()
	s.lanzarEncendidoLocked()
	return s.vistaLocked(), true, nil
}

func (s *Servicio) registrarActividadLocked() {
	ahora := s.d.Reloj()
	s.est.UltimaActividad = ahora
	s.d.Actividad.Marcar(ahora)
	s.guardarLocked()
}

func (s *Servicio) lanzarEncendidoLocked() {
	ctx, cancelar := context.WithCancel(s.base)
	t := &trabajo{cancelar: cancelar, hecho: make(chan struct{})}
	s.trabajo = t
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(t.hecho)
		s.encender(ctx)
	}()
}

// Apagar inicia el apagado. Es idempotente.
func (s *Servicio) Apagar() Vista {
	s.mu.Lock()
	sinPod := s.est.PodID == "" && (s.est.Fase == estado.Apagado || s.est.Fase == estado.SinExistencias || s.est.Fase == estado.Error)
	s.mu.Unlock()
	if !sinPod {
		s.solicitarApagado(estado.Apagado, "apagado por el operador")
	}
	return s.Vista()
}
