package runpod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// RutaVolumen es donde se monta el volumen de red dentro del pod. La API v2 no tiene valor por defecto:
// el campo path es obligatorio, así que se fija aquí. Es el mismo directorio que usa el script del pod.
const RutaVolumen = "/workspace"

const (
	estadoTerminado = "TERMINATED"
	maxPaginas      = 50
	rutaPods        = "/pods"
)

var (
	patronID      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	patronHost    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	patronUsuario = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	patronCentro  = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{0,30}[A-Z0-9]$`)
	patronVolumen = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
	patronCola    = regexp.MustCompile(`^/v2/queued-pods/[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// DestinoSSH es el destino directo de un pod. Host, puerto y usuario ya están validados.
type DestinoSSH struct {
	Host    string
	Puerto  int
	Usuario string
}

// Pod es la vista de un pod que usa modelos-control. No incluye env a propósito: RunPod lo devuelve
// en las respuestas y puede contener secretos.
type Pod struct {
	ID           string
	Nombre       string
	Estado       string
	Costo        float64
	CostoLegible bool
	Nube         string
	GPU          string
	Centro       string // centro de datos que informa RunPod, vacío si aún no lo asignó
	CreadoEn     time.Time
	IniciadoEn   time.Time
	SSH          *DestinoSSH
}

// Vivo dice si el pod todavía puede cobrar. EXITED cuenta como vivo: un pod parado con disco cobra.
func (p Pod) Vivo() bool { return p.Estado != estadoTerminado }

// Inicio devuelve cuándo empezó el pod: el arranque si existe y, si no, la creación.
func (p Pod) Inicio() time.Time {
	if !p.IniciadoEn.IsZero() {
		return p.IniciadoEn
	}
	return p.CreadoEn
}

type puertoJSON struct {
	Privado int     `json:"private"`
	Publico *int    `json:"public"`
	IP      *string `json:"ip"`
}

type podJSON struct {
	ID     string   `json:"id"`
	Nombre string   `json:"name"`
	Estado string   `json:"status"`
	Costo  *float64 `json:"cost"`
	Nube   string   `json:"cloud"`
	GPU    *struct {
		ID string `json:"id"`
	} `json:"gpu"`
	Centro *string `json:"dataCenterId"`
	SSH    *struct {
		Directo *struct {
			Host    string `json:"host"`
			Puerto  int    `json:"port"`
			Usuario string `json:"username"`
		} `json:"direct"`
	} `json:"ssh"`
	Runtime *struct {
		Puertos []puertoJSON `json:"ports"`
	} `json:"runtime"`
	CreadoEn   *string `json:"createdAt"`
	IniciadoEn *string `json:"startedAt"`
}

func decodificarPod(datos []byte) (Pod, error) {
	var j podJSON
	if err := json.Unmarshal(datos, &j); err != nil {
		return Pod{}, fmt.Errorf("%w: json de pod", ErrRespuestaInvalida)
	}
	return j.aPod()
}

func (j podJSON) aPod() (Pod, error) {
	if !patronID.MatchString(j.ID) {
		return Pod{}, fmt.Errorf("%w: id de pod", ErrRespuestaInvalida)
	}
	p := Pod{ID: j.ID, Nombre: j.Nombre, Estado: j.Estado, Nube: j.Nube, CreadoEn: parsearHora(j.CreadoEn), IniciadoEn: parsearHora(j.IniciadoEn)}
	if j.GPU != nil {
		p.GPU = j.GPU.ID
	}
	if j.Centro != nil {
		p.Centro = *j.Centro
	}
	if j.Costo != nil && !math.IsNaN(*j.Costo) && !math.IsInf(*j.Costo, 0) {
		p.Costo, p.CostoLegible = *j.Costo, true
	}
	destino, err := j.destino()
	if err != nil {
		return Pod{}, err
	}
	p.SSH = destino
	return p, nil
}

func parsearHora(s *string) time.Time {
	if s == nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// destino prefiere ssh.direct y cae a runtime.ports (puerto privado 22). Nil si aún no hay destino.
func (j podJSON) destino() (*DestinoSSH, error) {
	var d *DestinoSSH
	if j.SSH != nil && j.SSH.Directo != nil {
		d = &DestinoSSH{Host: j.SSH.Directo.Host, Puerto: j.SSH.Directo.Puerto, Usuario: j.SSH.Directo.Usuario}
	} else if j.Runtime != nil {
		for _, pt := range j.Runtime.Puertos {
			if pt.Privado == 22 && pt.Publico != nil && pt.IP != nil {
				d = &DestinoSSH{Host: *pt.IP, Puerto: *pt.Publico, Usuario: "root"}
				break
			}
		}
	}
	if d == nil {
		return nil, nil
	}
	if !patronHost.MatchString(d.Host) || !patronUsuario.MatchString(d.Usuario) || d.Puerto < 1 || d.Puerto > 65535 {
		return nil, fmt.Errorf("%w: destino ssh", ErrRespuestaInvalida)
	}
	return d, nil
}

// SolicitudPod es lo único que se manda al crear un pod. La nube siempre es SECURE: este cliente
// no cambia de nube por su cuenta. Centro y Volumen van juntos: ambos vacíos crean el pod sin volumen,
// y con ambos se fija el centro de datos y se monta el volumen de red en RutaVolumen.
type SolicitudPod struct {
	Nombre       string
	Imagen       string
	GPU          string
	DiscoGB      int
	LlavePublica string
	Centro       string
	Volumen      string
}

type gpuCrear struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// cuerpoCrear lista exactamente las claves permitidas: CreatePodRequest rechaza con 422 cualquier otra.
type cuerpoCrear struct {
	Nombre   string            `json:"name"`
	Imagen   string            `json:"image"`
	GPU      gpuCrear          `json:"gpu"`
	Nube     string            `json:"cloud"`
	Disco    int               `json:"disk"`
	Puertos  []string          `json:"ports"`
	Env      map[string]string `json:"env"`
	StartSSH bool              `json:"startSsh"`
	Centros  []string          `json:"dataCenterIds,omitempty"`
	Montajes *montajes         `json:"mounts,omitempty"`
}

type montajes struct {
	Red []montajeRed `json:"network"`
}

type montajeRed struct {
	VolumenID string `json:"volumeId"`
	Ruta      string `json:"path"`
}

func (s SolicitudPod) cuerpo() cuerpoCrear {
	c := cuerpoCrear{Nombre: s.Nombre, Imagen: s.Imagen, GPU: gpuCrear{ID: s.GPU, Count: 1}, Nube: "SECURE",
		Disco: s.DiscoGB, Puertos: []string{"22/tcp"}, Env: map[string]string{"PUBLIC_KEY": s.LlavePublica}, StartSSH: true}
	if s.Volumen != "" {
		c.Centros = []string{s.Centro}
		c.Montajes = &montajes{Red: []montajeRed{{VolumenID: s.Volumen, Ruta: RutaVolumen}}}
	}
	return c
}

// validarVolumen exige que Centro y Volumen vengan juntos y con forma válida, para no mandar a RunPod
// un montaje sin centro ni un valor armado a mano que se cuele en el JSON.
func (s SolicitudPod) validarVolumen() error {
	if s.Centro == "" && s.Volumen == "" {
		return nil
	}
	if !patronCentro.MatchString(s.Centro) || !patronVolumen.MatchString(s.Volumen) {
		return errors.New("runpod: el volumen y el centro de datos deben venir juntos y validos")
	}
	return nil
}

// Crear hace POST /v2/pods. Un 202 (pod en cola) nunca se pidió: se cancela y se devuelve ErrEncolado.
func (c *Cliente) Crear(ctx context.Context, s SolicitudPod) (Pod, error) {
	if err := s.validarVolumen(); err != nil {
		return Pod{}, err
	}
	r, err := c.hacer(ctx, http.MethodPost, rutaPods, nil, s.cuerpo())
	if err != nil {
		return Pod{}, err
	}
	if r.estado == http.StatusAccepted {
		c.cancelarEnCola(ctx, r.cab.Get("Location"))
		return Pod{}, ErrEncolado
	}
	return decodificarPod(r.cuerpo)
}

func (c *Cliente) cancelarEnCola(ctx context.Context, ubicacion string) {
	if patronCola.MatchString(ubicacion) {
		_, _ = c.hacer(ctx, http.MethodDelete, strings.TrimPrefix(ubicacion, "/v2"), nil, nil)
	}
}

// Obtener hace GET /v2/pods/{id}, el único que trae ssh.direct y runtime completos.
func (c *Cliente) Obtener(ctx context.Context, id string) (Pod, error) {
	if !patronID.MatchString(id) {
		return Pod{}, fmt.Errorf("%w: id de pod", ErrRespuestaInvalida)
	}
	r, err := c.hacer(ctx, http.MethodGet, rutaPods+"/"+id, nil, nil)
	if err != nil {
		return Pod{}, err
	}
	return decodificarPod(r.cuerpo)
}

// ListarVivos recorre toda la paginación y devuelve los pods no terminados con ese nombre. Los demás
// pods de la cuenta (por ejemplo los de la tesis) se ignoran sin interpretarlos.
func (c *Cliente) ListarVivos(ctx context.Context, nombre string) ([]Pod, error) {
	var vivos []Pod
	cursor := ""
	for range maxPaginas {
		consulta := url.Values{}
		if cursor != "" {
			consulta.Set("cursor", cursor)
		}
		r, err := c.hacer(ctx, http.MethodGet, rutaPods, consulta, nil)
		if err != nil {
			return nil, err
		}
		var pagina struct {
			Pods       []json.RawMessage `json:"pods"`
			Paginacion struct {
				Siguiente *string `json:"nextCursor"`
				HayMas    bool    `json:"hasNextPage"`
			} `json:"pagination"`
		}
		if err := json.Unmarshal(r.cuerpo, &pagina); err != nil {
			return nil, fmt.Errorf("%w: lista de pods", ErrRespuestaInvalida)
		}
		pods, err := filtrarVivos(pagina.Pods, nombre)
		if err != nil {
			return nil, err
		}
		vivos = append(vivos, pods...)
		if !pagina.Paginacion.HayMas || pagina.Paginacion.Siguiente == nil {
			return vivos, nil
		}
		cursor = *pagina.Paginacion.Siguiente
	}
	return nil, fmt.Errorf("%w: demasiadas paginas", ErrRespuestaInvalida)
}

func filtrarVivos(crudos []json.RawMessage, nombre string) ([]Pod, error) {
	var salida []Pod
	for _, crudo := range crudos {
		var ligero struct {
			Nombre string `json:"name"`
			Estado string `json:"status"`
		}
		if json.Unmarshal(crudo, &ligero) != nil || ligero.Nombre != nombre || ligero.Estado == estadoTerminado {
			continue
		}
		p, err := decodificarPod(crudo)
		if err != nil {
			return nil, err
		}
		salida = append(salida, p)
	}
	return salida, nil
}

// Terminar hace DELETE /v2/pods/{id}. Un 404 devuelve ErrNoExiste (quien llama confirma con la lista).
func (c *Cliente) Terminar(ctx context.Context, id string) error {
	if !patronID.MatchString(id) {
		return fmt.Errorf("%w: id de pod", ErrRespuestaInvalida)
	}
	_, err := c.hacer(ctx, http.MethodDelete, rutaPods+"/"+id, nil, nil)
	return err
}
