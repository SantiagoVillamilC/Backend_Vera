// Package falsos reúne dobles de prueba para modelos-control: una API de RunPod v2 falsa y un
// ssh falso. Solo lo importan pruebas.
package falsos

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// EntradaCatalogo describe una GPU del catálogo falso.
type EntradaCatalogo struct {
	Secure         bool
	Disponibilidad string
	PrecioSecure   float64
}

// RespuestaCrear programa una respuesta de POST /v2/pods. Estado 0 significa 201.
type RespuestaCrear struct {
	Estado int
	Costo  *float64
	// SinCosto omite el campo cost de la respuesta, como una respuesta ilegible.
	SinCosto bool
	Nube     string
	GPU      string
	// Detalle es el texto "detail" del error programado. Vacío usa un texto genérico.
	Detalle string
	// Centro cambia el dataCenterId que informa la respuesta (por ejemplo, un pod ubicado en otro centro).
	Centro string
	// SinCentro informa dataCenterId nulo, como un pod que el planificador aún no ubicó.
	SinCentro bool
}

type podFalso struct {
	id, nombre, estado, nube, gpu, centro string
	costo                                 *float64
	getsPendientes                        int
	creado                                time.Time
	env                                   map[string]string
}

// RunPod es una API de RunPod v2 falsa. Los campos exportados se ajustan antes de usarla.
type RunPod struct {
	*httptest.Server
	mu       sync.Mutex
	Clave    string
	Catalogo map[string]EntradaCatalogo
	Crear    []RespuestaCrear
	// CrearPorCentro programa la respuesta de POST /v2/pods según el primer dataCenterIds del cuerpo y
	// tiene prioridad sobre la cola Crear. Una clave presente se aplica en cada intento a ese centro.
	CrearPorCentro map[string]RespuestaCrear
	// Volumenes mapea id de volumen de red a su centro. Si no es nil, un montaje con un volumen
	// desconocido o de otro centro se rechaza con 400, como hace RunPod.
	Volumenes       map[string]string
	FallosDelete    int
	DeleteSinEfecto int
	GetsHastaRun    int
	PaginaTam       int
	ListarFalla     bool
	pods            map[string]*podFalso
	contador        int
	llamadas        []string
	cuerpos         [][]byte
	auth            map[string]int
}

// NuevoRunPod arranca la API falsa con la clave dada y una A5000 con existencias a 0,27 USD/h.
func NuevoRunPod(t *testing.T, clave string) *RunPod {
	t.Helper()
	f := &RunPod{Clave: clave, pods: map[string]*podFalso{}, auth: map[string]int{}, PaginaTam: 1000,
		Catalogo: map[string]EntradaCatalogo{"NVIDIA RTX A5000": {Secure: true, Disponibilidad: "HIGH", PrecioSecure: 0.27}}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.servir))
	t.Cleanup(f.Close)
	return f
}

// Llamadas devuelve una copia de las peticiones recibidas, como "METHOD /ruta".
func (f *RunPod) Llamadas() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.llamadas...)
}

// Cuerpos devuelve los cuerpos crudos de POST /v2/pods.
func (f *RunPod) Cuerpos() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.cuerpos...)
}

// Autorizaciones devuelve cada valor distinto de Authorization visto y cuántas veces.
func (f *RunPod) Autorizaciones() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	copia := map[string]int{}
	for k, v := range f.auth {
		copia[k] = v
	}
	return copia
}

// AnadirPod inserta un pod ya existente, por ejemplo un huérfano o uno ajeno.
func (f *RunPod) AnadirPod(nombre, estado string, creado time.Time) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nuevoPod(nombre, estado, "SECURE", "NVIDIA RTX A5000", ptr(0.27), creado, nil)
}

// Vivos cuenta los pods no terminados con ese nombre.
func (f *RunPod) Vivos(nombre string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.pods {
		if p.nombre == nombre && p.estado != "TERMINATED" {
			n++
		}
	}
	return n
}

// Existe dice si el pod sigue vivo.
func (f *RunPod) Existe(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pods[id]
	return ok && p.estado != "TERMINATED"
}

// EnvDe devuelve el env con el que se creó un pod.
func (f *RunPod) EnvDe(id string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pods[id]; ok {
		return p.env
	}
	return nil
}

func ptr(v float64) *float64 { return &v }

// Costo es un atajo para programar un costo en RespuestaCrear.
func Costo(v float64) *float64 { return &v }

func (f *RunPod) nuevoPod(nombre, estado, nube, gpu string, costo *float64, creado time.Time, env map[string]string) string {
	f.contador++
	id := fmt.Sprintf("pod%04d", f.contador)
	f.pods[id] = &podFalso{id: id, nombre: nombre, estado: estado, nube: nube, gpu: gpu, costo: costo,
		getsPendientes: f.GetsHastaRun, creado: creado, env: env}
	return id
}

func problema(w http.ResponseWriter, estado int, titulo, detalle string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(estado)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": titulo, "status": estado, "detail": detalle})
}

func (f *RunPod) servir(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.llamadas = append(f.llamadas, r.Method+" "+r.URL.Path)
	f.auth[r.Header.Get("Authorization")]++
	if r.Header.Get("Authorization") != "Bearer "+f.Clave {
		problema(w, 401, "Unauthorized", "missing bearer token")
		return
	}
	ruta := strings.TrimPrefix(r.URL.Path, "/v2")
	switch {
	case ruta == "/catalog/gpus" && r.Method == http.MethodGet:
		f.catalogo(w, r)
	case ruta == "/pods" && r.Method == http.MethodGet:
		f.listar(w, r)
	case ruta == "/pods" && r.Method == http.MethodPost:
		f.crear(w, r)
	case strings.HasPrefix(ruta, "/pods/") && r.Method == http.MethodGet:
		f.obtener(w, strings.TrimPrefix(ruta, "/pods/"))
	case strings.HasPrefix(ruta, "/pods/") && r.Method == http.MethodDelete:
		f.borrar(w, strings.TrimPrefix(ruta, "/pods/"))
	default:
		problema(w, 404, "Not Found", "ruta desconocida")
	}
}

func (f *RunPod) catalogo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("include") != "AVAILABILITY" || q.Get("product") != "POD" || q.Get("cloud") != "SECURE" {
		problema(w, 400, "Bad Request", "faltan include, product o cloud")
		return
	}
	gpus := []map[string]any{{"id": "NVIDIA OTRA GPU", "name": "x", "secure": true, "community": true,
		"price": map[string]float64{"secure": 9.9, "community": 9.9}, "availability": "HIGH"}}
	for id, e := range f.Catalogo {
		gpus = append(gpus, map[string]any{"id": id, "name": id, "secure": e.Secure, "community": false,
			"price": map[string]float64{"secure": e.PrecioSecure, "community": 0}, "availability": e.Disponibilidad})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"gpus": gpus})
}

func (f *RunPod) json(p *podFalso, completo bool) map[string]any {
	var centro any
	if p.centro != "" {
		centro = p.centro
	}
	m := map[string]any{"id": p.id, "name": p.nombre, "status": p.estado, "cloud": p.nube, "dataCenterId": centro,
		"gpu": map[string]any{"id": p.gpu, "count": 1}, "env": p.env, "ssh": map[string]any{"proxy": nil, "direct": nil},
		"createdAt": p.creado.UTC().Format(time.RFC3339), "startedAt": p.creado.UTC().Format(time.RFC3339)}
	if p.costo != nil {
		m["cost"] = *p.costo
	}
	if completo && p.estado == "RUNNING" {
		m["ssh"] = map[string]any{"proxy": nil, "direct": map[string]any{"host": "203.0.113.7", "port": 40022, "username": "root"}}
	}
	return m
}

func (f *RunPod) listar(w http.ResponseWriter, r *http.Request) {
	if f.ListarFalla {
		problema(w, 500, "Internal Server Error", "lista no disponible")
		return
	}
	vivos := []*podFalso{}
	for i := f.contador; i >= 1; i-- {
		if p, ok := f.pods[fmt.Sprintf("pod%04d", i)]; ok && p.estado != "TERMINATED" {
			vivos = append(vivos, p)
		}
	}
	inicio, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	fin := min(inicio+f.PaginaTam, len(vivos))
	salida := []map[string]any{}
	for _, p := range vivos[min(inicio, fin):fin] {
		salida = append(salida, f.json(p, false))
	}
	pagina := map[string]any{"hasNextPage": fin < len(vivos), "nextCursor": nil}
	if fin < len(vivos) {
		pagina["nextCursor"] = strconv.Itoa(fin)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"pods": salida, "pagination": pagina})
}

func (f *RunPod) obtener(w http.ResponseWriter, id string) {
	p, ok := f.pods[id]
	if !ok || p.estado == "TERMINATED" {
		problema(w, 404, "Not Found", "pod not found")
		return
	}
	if p.estado == "STARTING" || p.estado == "PROVISIONING" {
		if p.getsPendientes--; p.getsPendientes <= 0 {
			p.estado = "RUNNING"
		}
	}
	_ = json.NewEncoder(w).Encode(f.json(p, true))
}

func (f *RunPod) borrar(w http.ResponseWriter, id string) {
	p, ok := f.pods[id]
	switch {
	case f.FallosDelete > 0:
		f.FallosDelete--
		problema(w, 500, "Internal Server Error", "fallo transitorio")
	case !ok || p.estado == "TERMINATED":
		problema(w, 404, "Not Found", "pod not found")
	case f.DeleteSinEfecto > 0:
		f.DeleteSinEfecto--
		w.WriteHeader(204)
	default:
		p.estado = "TERMINATED"
		w.WriteHeader(204)
	}
}

type cuerpoCrear struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	GPU   struct {
		ID    string `json:"id"`
		Count int    `json:"count"`
	} `json:"gpu"`
	Cloud         string            `json:"cloud"`
	Disk          int               `json:"disk"`
	Ports         []string          `json:"ports"`
	Env           map[string]string `json:"env"`
	StartSSH      bool              `json:"startSsh"`
	DataCenterIDs []string          `json:"dataCenterIds"`
	Mounts        *struct {
		Network []struct {
			VolumeID string `json:"volumeId"`
			Path     string `json:"path"`
		} `json:"network"`
	} `json:"mounts"`
}

// validarMontajes aplica las reglas cruzadas de Mounts: path obligatorio, hasta 4 volúmenes y cada uno
// en el centro del pod. Devuelve el detalle del 400 o vacío.
func (f *RunPod) validarMontajes(c cuerpoCrear) string {
	if c.Mounts == nil {
		return ""
	}
	if len(c.Mounts.Network) > 4 {
		return "at most 4 network volumes"
	}
	for _, m := range c.Mounts.Network {
		centro, conocido := f.Volumenes[m.VolumeID]
		switch {
		case m.Path == "":
			return "mount path must be specified"
		case f.Volumenes != nil && !conocido:
			return "network volume not found"
		case f.Volumenes != nil && (len(c.DataCenterIDs) != 1 || c.DataCenterIDs[0] != centro):
			return "network volume must be in the pod's data center"
		}
	}
	return ""
}

func (f *RunPod) respuestaProgramada(c cuerpoCrear) RespuestaCrear {
	if len(c.DataCenterIDs) > 0 {
		if r, ok := f.CrearPorCentro[c.DataCenterIDs[0]]; ok {
			return r
		}
	}
	if len(f.Crear) > 0 {
		var r RespuestaCrear
		r, f.Crear = f.Crear[0], f.Crear[1:]
		return r
	}
	return RespuestaCrear{}
}

func (f *RunPod) crear(w http.ResponseWriter, r *http.Request) {
	var crudo json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&crudo); err != nil {
		problema(w, 422, "Unprocessable Entity", "json invalido")
		return
	}
	f.cuerpos = append(f.cuerpos, crudo)
	var c cuerpoCrear
	dec := json.NewDecoder(strings.NewReader(string(crudo)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		problema(w, 422, "Unprocessable Entity", "$: additional properties not allowed")
		return
	}
	if detalle := f.validarMontajes(c); detalle != "" {
		problema(w, 400, "Bad Request", detalle)
		return
	}
	resp := f.respuestaProgramada(c)
	if resp.Estado != 0 && resp.Estado != 201 {
		problema(w, resp.Estado, "Error programado", orDefecto(resp.Detalle, "creacion rechazada"))
		return
	}
	nube, gpu, costo := orDefecto(resp.Nube, c.Cloud), orDefecto(resp.GPU, c.GPU.ID), resp.Costo
	if costo == nil && !resp.SinCosto {
		costo = ptr(0.27)
	}
	id := f.nuevoPod(c.Name, "STARTING", nube, gpu, costo, time.Now(), c.Env)
	f.pods[id].centro = centroInformado(resp, c)
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(f.json(f.pods[id], false))
}

func centroInformado(resp RespuestaCrear, c cuerpoCrear) string {
	switch {
	case resp.SinCentro:
		return ""
	case resp.Centro != "":
		return resp.Centro
	case len(c.DataCenterIDs) > 0:
		return c.DataCenterIDs[0]
	}
	return ""
}

func orDefecto(valor, defecto string) string {
	if valor != "" {
		return valor
	}
	return defecto
}
