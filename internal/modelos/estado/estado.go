// Package estado guarda en disco la máquina de estados de modelos-control, de modo que un reinicio
// del proceso no pierda el rastro del pod y pueda reconciliarlo.
package estado

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Fase es el estado del ciclo de vida de los modelos.
type Fase string

// Fases posibles.
const (
	Apagado        Fase = "apagado"
	Encendiendo    Fase = "encendiendo"
	Listo          Fase = "listo"
	Apagando       Fase = "apagando"
	Error          Fase = "error"
	SinExistencias Fase = "sin_existencias"
)

// ErrEstadoInvalido indica un archivo de estado corrupto o un valor que no se debe persistir.
var ErrEstadoInvalido = errors.New("estado invalido")

var (
	patronID      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	patronCentro  = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{0,30}[A-Z0-9]$`)
	patronVolumen = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
)

// IDValido dice si un id de pod es seguro para usarlo en rutas de URL y de archivo.
func IDValido(id string) bool { return patronID.MatchString(id) }

// Estado es lo que se persiste. PodID vacío significa que no hay pod conocido.
type Estado struct {
	Fase            Fase      `json:"fase"`
	PodID           string    `json:"pod_id,omitempty"`
	GPU             string    `json:"gpu,omitempty"`
	Centro          string    `json:"centro,omitempty"`
	Volumen         string    `json:"volumen,omitempty"`
	TarifaUSDH      float64   `json:"tarifa_usd_h,omitempty"`
	SesionInicio    time.Time `json:"sesion_inicio,omitempty"`
	UltimaActividad time.Time `json:"ultima_actividad,omitempty"`
	ActualizadoEn   time.Time `json:"actualizado_en,omitempty"`
	Mensaje         string    `json:"mensaje,omitempty"`
}

func (e Estado) validar() error {
	switch e.Fase {
	case Apagado, Encendiendo, Listo, Apagando, Error, SinExistencias:
	default:
		return fmt.Errorf("%w: fase %q", ErrEstadoInvalido, e.Fase)
	}
	if e.PodID != "" && !IDValido(e.PodID) {
		return fmt.Errorf("%w: id de pod", ErrEstadoInvalido)
	}
	if e.Centro != "" && !patronCentro.MatchString(e.Centro) {
		return fmt.Errorf("%w: centro de datos", ErrEstadoInvalido)
	}
	if e.Volumen != "" && !patronVolumen.MatchString(e.Volumen) {
		return fmt.Errorf("%w: id de volumen", ErrEstadoInvalido)
	}
	return nil
}

// Almacen lee y escribe el archivo de estado dentro de un directorio privado.
type Almacen struct{ dir string }

// NuevoAlmacen crea el directorio con permisos 0700 si no existe.
func NuevoAlmacen(dir string) (Almacen, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Almacen{}, fmt.Errorf("crear directorio de estado: %w", err)
	}
	return Almacen{dir: dir}, nil
}

// Dir devuelve el directorio de estado.
func (a Almacen) Dir() string { return a.dir }

func (a Almacen) ruta() string { return filepath.Join(a.dir, "estado.json") }

// Cargar lee el estado. Sin archivo devuelve Apagado.
func (a Almacen) Cargar() (Estado, error) {
	datos, err := os.ReadFile(a.ruta())
	if errors.Is(err, os.ErrNotExist) {
		return Estado{Fase: Apagado}, nil
	}
	if err != nil {
		return Estado{}, fmt.Errorf("leer estado: %w", err)
	}
	var e Estado
	if err := json.Unmarshal(datos, &e); err != nil {
		return Estado{}, fmt.Errorf("%w: json", ErrEstadoInvalido)
	}
	if err := e.validar(); err != nil {
		return Estado{}, err
	}
	return e, nil
}

// Guardar escribe el estado de forma atómica con permisos 0600.
func (a Almacen) Guardar(e Estado) error {
	if err := e.validar(); err != nil {
		return err
	}
	datos, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("serializar estado: %w", err)
	}
	temporal, err := os.CreateTemp(a.dir, "estado-*.tmp")
	if err != nil {
		return fmt.Errorf("crear temporal: %w", err)
	}
	defer func() { _ = os.Remove(temporal.Name()) }()
	if err := temporal.Chmod(0o600); err != nil {
		_ = temporal.Close()
		return fmt.Errorf("permisos del temporal: %w", err)
	}
	if _, err := temporal.Write(datos); err != nil {
		_ = temporal.Close()
		return fmt.Errorf("escribir estado: %w", err)
	}
	if err := temporal.Close(); err != nil {
		return fmt.Errorf("cerrar temporal: %w", err)
	}
	if err := os.Rename(temporal.Name(), a.ruta()); err != nil {
		return fmt.Errorf("reemplazar estado: %w", err)
	}
	return nil
}

// RutaKnownHosts devuelve el archivo de huellas SSH propio de un pod. Nunca se borra solo.
func (a Almacen) RutaKnownHosts(podID string) (string, error) {
	if !IDValido(podID) {
		return "", fmt.Errorf("%w: id de pod", ErrEstadoInvalido)
	}
	dir := filepath.Join(a.dir, "known_hosts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("crear directorio de huellas: %w", err)
	}
	return filepath.Join(dir, podID), nil
}
