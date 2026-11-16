package control

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/config"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/runpod"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/modelos/secreto"
)

var errSinExistencias = errors.New("sin existencias en ninguna GPU")

const (
	intentosCreacion = 3
	esperaCreacion   = 5 * time.Second
	topeRetryAfter   = 60 * time.Second
	// maxDetalleRechazo acota el detalle de RunPod que se copia por cada centro al mensaje de sin_existencias.
	maxDetalleRechazo = 90
)

// recorrido acumula lo que ya se averiguó mientras se prueban centros y GPU: el veredicto del catálogo
// de cada GPU (se consulta una sola vez) y los rechazos para el mensaje final.
type recorrido struct {
	ofertas  map[string]string
	rechazos []string
}

func (r *recorrido) rechazar(etiqueta, motivo string) {
	r.rechazos = append(r.rechazos, etiqueta+": "+motivo)
}

// candidatos son los centros a probar, en orden. Sin volúmenes configurados hay un único candidato
// vacío: el pod se crea sin volumen y sin fijar centro, como antes.
func (s *Servicio) candidatos() []config.Volumen {
	if len(s.d.Cfg.Volumenes) == 0 {
		return []config.Volumen{{}}
	}
	return s.d.Cfg.Volumenes
}

// crearPod recorre los centros en orden y, dentro de cada uno, las GPU en orden de preferencia. Un
// rechazo por falta de existencias pasa al siguiente candidato. Cualquier otro error se detiene aquí.
// Nunca cambia de nube y falla cerrado.
func (s *Servicio) crearPod(ctx context.Context) (runpod.Pod, error) {
	r := &recorrido{ofertas: map[string]string{}}
	cands := s.candidatos()
	s.d.Log.Info("creando el pod", "centros", centrosDe(cands), "gpus", s.d.Cfg.GPUs)
	for i, c := range cands {
		for _, gpu := range s.d.Cfg.GPUs {
			pod, hecho, err := s.intentar(ctx, r, gpu, c, i+1)
			if err != nil || hecho {
				return pod, err
			}
		}
	}
	return runpod.Pod{}, fmt.Errorf("%w: %s", errSinExistencias, strings.Join(r.rechazos, " | "))
}

func centrosDe(cands []config.Volumen) []string {
	var centros []string
	for _, c := range cands {
		if c.Centro != "" {
			centros = append(centros, c.Centro)
		}
	}
	return centros
}

// intentar prueba una GPU en un centro. hecho=true significa que el pod quedó creado y validado.
func (s *Servicio) intentar(ctx context.Context, r *recorrido, gpu string, c config.Volumen, posicion int) (runpod.Pod, bool, error) {
	motivo, err := s.ofertaDe(ctx, r, gpu)
	if err != nil {
		return runpod.Pod{}, false, fmt.Errorf("no se pudo consultar el catalogo de %s: %w", gpu, err)
	}
	if motivo != "" {
		return runpod.Pod{}, false, nil
	}
	s.d.Log.Info("intento de creacion", "posicion", posicion, "centro", c.Centro, "gpu", gpu)
	pod, err := s.crearEn(ctx, gpu, c)
	if err == nil {
		s.registrarPod(pod, gpu, c)
		pod, err = s.validarCreado(pod, gpu, c)
		return pod, err == nil, err
	}
	if !esCapacidad(err) {
		return runpod.Pod{}, false, errorDeCreacion(err)
	}
	detalle := detalleCapacidad(err)
	s.d.Log.Warn("sin existencias en el centro, se prueba el siguiente", "centro", c.Centro, "gpu", gpu, "motivo", s.d.Redactor.Limpiar(detalle))
	r.rechazar(etiquetaDe(gpu, c), "sin capacidad ("+secreto.Recortar(s.d.Redactor.Limpiar(detalle), maxDetalleRechazo)+")")
	return runpod.Pod{}, false, nil
}

func etiquetaDe(gpu string, c config.Volumen) string {
	if c.Centro == "" {
		return gpu
	}
	return gpu + "@" + c.Centro
}

func detalleCapacidad(err error) string {
	var api *runpod.ErrorAPI
	if errors.As(err, &api) {
		return fmt.Sprintf("HTTP %d: %s", api.Estado, api.Detalle)
	}
	return err.Error()
}

// ofertaDe consulta el catálogo una sola vez por GPU y recuerda el veredicto para los demás centros.
func (s *Servicio) ofertaDe(ctx context.Context, r *recorrido, gpu string) (string, error) {
	if motivo, visto := r.ofertas[gpu]; visto {
		return motivo, nil
	}
	motivo, err := s.evaluarOferta(ctx, gpu)
	if err != nil {
		return "", err
	}
	r.ofertas[gpu] = motivo
	if motivo != "" {
		r.rechazar(gpu, motivo)
	}
	return motivo, nil
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

// esCapacidad sigue la tabla de POST /v2/pods del OpenAPI v2: 400 y 403 pasan al siguiente candidato, y
// también un 500 cuyo detalle dice que no hay instancias. Ver runpod.ErrorAPI.EsCapacidad.
func esCapacidad(err error) bool {
	var api *runpod.ErrorAPI
	return errors.As(err, &api) && api.EsCapacidad()
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
func (s *Servicio) crearEn(ctx context.Context, gpu string, c config.Volumen) (runpod.Pod, error) {
	solicitud := runpod.SolicitudPod{Nombre: s.d.Cfg.NombrePod, Imagen: s.d.Cfg.Imagen, GPU: gpu, DiscoGB: s.d.Cfg.DiscoGB,
		LlavePublica: s.d.LlavePublica, Centro: c.Centro, Volumen: c.ID}
	for intento := 1; ; intento++ {
		pod, err := s.d.Nube.Crear(ctx, solicitud)
		if err == nil {
			s.d.Log.Warn(fmt.Sprintf("POD CREADO id=%s", pod.ID), "gpu", gpu, "centro", c.Centro, "costo_usd_h", pod.Costo)
			return pod, nil
		}
		var api *runpod.ErrorAPI
		esAPI := errors.As(err, &api)
		if (esAPI && !api.Reintentable()) || errors.Is(err, runpod.ErrEncolado) || ctx.Err() != nil {
			return runpod.Pod{}, err
		}
		if !esAPI || api.Estado != 429 {
			if previo, ok := s.buscarCreado(ctx); ok {
				s.d.Log.Warn(fmt.Sprintf("POD CREADO id=%s", previo.ID), "gpu", gpu, "centro", c.Centro, "origen", "hallado tras un fallo ambiguo")
				return previo, nil
			}
		}
		if esAPI && api.EsCapacidad() {
			return runpod.Pod{}, err // un 500 de "no instances" no mejora al repetirlo: se pasa de centro
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
func (s *Servicio) registrarPod(p runpod.Pod, gpu string, c config.Volumen) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.est.PodID, s.est.GPU, s.est.SesionInicio = p.ID, gpu, s.d.Reloj()
	s.est.Centro, s.est.Volumen = c.Centro, c.ID
	if p.CostoLegible {
		s.est.TarifaUSDH = p.Costo
	}
	s.guardarLocked()
}

// validarCreado falla cerrado: tarifa ilegible o sobre el tope, otra nube u otra GPU terminan el pod.
func (s *Servicio) validarCreado(p runpod.Pod, gpu string, c config.Volumen) (runpod.Pod, error) {
	tope := s.d.Cfg.TarifaTopeUSDH
	if c.Centro != "" && p.Centro == "" {
		s.d.Log.Warn("RunPod no informo el centro de datos del pod recien creado, se acepta porque el volumen solo monta en su centro", "pod", p.ID, "centro_pedido", c.Centro)
	}
	switch {
	case !p.CostoLegible || math.IsNaN(p.Costo) || p.Costo <= 0:
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo sin una tarifa legible: no se puede comprobar el tope de %.2f", p.ID, tope)
	case p.Costo > tope:
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo con tarifa %.2f USD/h sobre el tope de %.2f", p.ID, p.Costo, tope)
	case p.Nube != "SECURE":
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo en la nube %q y no en SECURE", p.ID, p.Nube)
	case p.GPU != gpu:
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo con la GPU %q y no con %q", p.ID, p.GPU, gpu)
	case c.Centro != "" && p.Centro != "" && !strings.EqualFold(p.Centro, c.Centro):
		return runpod.Pod{}, fmt.Errorf("el pod %s se creo en el centro %q y no en %q", p.ID, p.Centro, c.Centro)
	}
	return p, nil
}
