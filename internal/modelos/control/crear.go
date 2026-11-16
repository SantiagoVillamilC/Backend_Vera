package control

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/runpod"
)

var errSinExistencias = errors.New("sin existencias en ninguna GPU")

const (
	intentosCreacion = 3
	esperaCreacion   = 5 * time.Second
	topeRetryAfter   = 60 * time.Second
)

// crearPod recorre las GPU en orden de preferencia. Nunca cambia de nube y falla cerrado.
func (s *Servicio) crearPod(ctx context.Context) (runpod.Pod, error) {
	var rechazos []string
	for _, gpu := range s.d.Cfg.GPUs {
		motivo, err := s.evaluarOferta(ctx, gpu)
		if err != nil {
			return runpod.Pod{}, fmt.Errorf("no se pudo consultar el catalogo de %s: %w", gpu, err)
		}
		if motivo != "" {
			rechazos = append(rechazos, gpu+": "+motivo)
			continue
		}
		pod, err := s.crearEn(ctx, gpu)
		if err == nil {
			s.registrarPod(pod, gpu)
			return s.validarCreado(pod, gpu)
		}
		if !esCapacidad(err) {
			return runpod.Pod{}, errorDeCreacion(err)
		}
		rechazos = append(rechazos, gpu+": RunPod no pudo ubicarla ("+err.Error()+")")
	}
	return runpod.Pod{}, fmt.Errorf("%w: %s", errSinExistencias, strings.Join(rechazos, "; "))
}

func (s *Servicio) evaluarOferta(ctx context.Context, gpu string) (string, error) {
	o, err := s.d.Nube.Oferta(ctx, gpu)
	if err != nil {
		return "", err
	}
	switch {
	case !o.Encontrada:
		return "no figura en el catalogo", nil
	case !o.Secure:
		return "no esta en la nube SECURE", nil
	case !o.HayExistencias():
		return "sin existencias (" + o.Disponibilidad + ")", nil
	case !o.TarifaLegible:
		return "sin tarifa legible", nil
	case o.TarifaSecure > s.d.Cfg.TarifaTopeUSDH:
		return fmt.Sprintf("la tarifa %.2f USD/h supera el tope de %.2f", o.TarifaSecure, s.d.Cfg.TarifaTopeUSDH), nil
	}
	return "", nil
}

func esCapacidad(err error) bool {
	var api *runpod.ErrorAPI
	return errors.As(err, &api) && (api.Estado == 400 || api.Estado == 403)
}

func errorDeCreacion(err error) error {
	var api *runpod.ErrorAPI
	if errors.As(err, &api) && api.Estado == 402 {
		return fmt.Errorf("saldo insuficiente en RunPod: %w", err)
	}
	return fmt.Errorf("no se pudo crear el pod: %w", err)
}

// crearEn hace el POST con reintentos acotados. Tras un fallo ambiguo (5xx o de red) busca por nombre
// si el pod se creó de todos modos, para no dejar uno huérfano ni crear dos.
func (s *Servicio) crearEn(ctx context.Context, gpu string) (runpod.Pod, error) {
	solicitud := runpod.SolicitudPod{Nombre: s.d.Cfg.NombrePod, Imagen: s.d.Cfg.Imagen, GPU: gpu, DiscoGB: s.d.Cfg.DiscoGB, LlavePublica: s.d.LlavePublica}
	for intento := 1; ; intento++ {
		pod, err := s.d.Nube.Crear(ctx, solicitud)
		if err == nil {
			s.d.Log.Warn(fmt.Sprintf("POD CREADO id=%s", pod.ID), "gpu", gpu, "costo_usd_h", pod.Costo)
			return pod, nil
		}
		var api *runpod.ErrorAPI
		esAPI := errors.As(err, &api)
		if (esAPI && !api.Reintentable()) || errors.Is(err, runpod.ErrEncolado) || ctx.Err() != nil {
			return runpod.Pod{}, err
		}
		if !esAPI || api.Estado != 429 {
			if previo, ok := s.buscarCreado(ctx); ok {
				s.d.Log.Warn(fmt.Sprintf("POD CREADO id=%s", previo.ID), "gpu", gpu, "origen", "hallado tras un fallo ambiguo")
				return previo, nil
			}
		}
		if intento >= intentosCreacion {
			return runpod.Pod{}, err
		}
		espera := esperaCreacion
		if esAPI && api.ReintentarEn > 0 {
			espera = min(api.ReintentarEn, topeRetryAfter)
		}
		if derr := s.d.Dormir(ctx, espera); derr != nil {
			return runpod.Pod{}, derr
		}
	}
}

func (s *Servicio) buscarCreado(ctx context.Context) (runpod.Pod, bool) {
	vivos, err := s.d.Nube.ListarVivos(ctx, s.d.Cfg.NombrePod)
	if err != nil || len(vivos) == 0 {
		return runpod.Pod{}, false
	}
	return vivos[0], true
}

// registrarPod persiste el id apenas existe, antes de validarlo: así un fallo posterior no lo pierde.
func (s *Servicio) registrarPod(p runpod.Pod, gpu string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.est.PodID, s.est.GPU, s.est.SesionInicio = p.ID, gpu, s.d.Reloj()
	if p.CostoLegible {
		s.est.TarifaUSDH = p.Costo
	}
	s.guardarLocked()
}

// validarCreado falla cerrado: tarifa ilegible o sobre el tope, otra nube u otra GPU terminan el pod.
func (s *Servicio) validarCreado(p runpod.Pod, gpu string) (runpod.Pod, error) {
	tope := s.d.Cfg.TarifaTopeUSDH
	switch {
	case !p.CostoLegible || math.IsNaN(p.Costo) || p.Costo <= 0:
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo sin una tarifa legible: no se puede comprobar el tope de %.2f", p.ID, tope)
	case p.Costo > tope:
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo con tarifa %.2f USD/h sobre el tope de %.2f", p.ID, p.Costo, tope)
	case p.Nube != "SECURE":
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo en la nube %q y no en SECURE", p.ID, p.Nube)
	case p.GPU != gpu:
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo con la GPU %q y no con %q", p.ID, p.GPU, gpu)
	}
	return p, nil
}
