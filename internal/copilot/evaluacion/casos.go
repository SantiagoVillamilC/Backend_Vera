// Package evaluacion contiene casos versionados y verificadores del comportamiento de Vera.
// Los guiones simulados prueban las reglas del backend, no la comprensión de un modelo real.
package evaluacion

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

//go:embed casos.json
var casosVersionados []byte

// Conjunto identifica la versión, fecha y casos compartidos por ambos niveles de evaluación.
type Conjunto struct {
	Version         string    `json:"version"`
	FechaReferencia time.Time `json:"fecha_referencia"`
	Casos           []Caso    `json:"casos"`
}

// Caso separa el oráculo esperado del guion del proveedor simulado.
type Caso struct {
	ID                 string            `json:"id"`
	Categoria          string            `json:"categoria"`
	Entrada            string            `json:"entrada"`
	Esperadas          []LlamadaEsperada `json:"llamadas_esperadas"`
	Restricciones      Restricciones     `json:"restricciones"`
	Guion              []Paso            `json:"guion"`
	SinConsentimiento  bool              `json:"sin_consentimiento,omitempty"`
	ErroresHerramienta map[string]string `json:"errores_herramienta,omitempty"`
	OmitirHerramientas []string          `json:"omitir_herramientas,omitempty"`
	Perfil             string            `json:"perfil,omitempty"`
}

// LlamadaEsperada registra función, argumentos y desenlace del catálogo, incluido rechazo.
type LlamadaEsperada struct {
	Nombre     string          `json:"nombre"`
	Argumentos json.RawMessage `json:"argumentos"`
	Codigo     string          `json:"codigo"`
}

// Restricciones fija aserciones independientes sobre la salida pública.
type Restricciones struct {
	Contiene   []string `json:"contiene"`
	Prohibidos []string `json:"prohibidos"`
	Error      string   `json:"error"`
	Accion     bool     `json:"accion"`
	Limitado   bool     `json:"limitado"`
}

// Paso representa únicamente una salida o un fallo del proveedor simulado.
type Paso struct {
	Texto    string            `json:"texto,omitempty"`
	Llamadas []LlamadaEsperada `json:"llamadas,omitempty"`
	Error    string            `json:"error,omitempty"`
	Rol      string            `json:"rol,omitempty"`
}

// Cargar entrega una copia independiente del conjunto incluido en el binario.
func Cargar() (Conjunto, error) { return Leer(bytes.NewReader(casosVersionados)) }

// Leer permite revisar conjuntos alternativos sin ejecutar código ni acceder a la red.
func Leer(r io.Reader) (Conjunto, error) {
	var c Conjunto
	d := json.NewDecoder(io.LimitReader(r, 2<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&c); err != nil {
		return c, fmt.Errorf("casos: JSON inválido: %w", err)
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return c, errors.New("casos: se esperaba un único conjunto")
	}
	return c, c.Validar()
}

// Validar rechaza un conjunto incompleto antes de producir resultados verdes.
func (c Conjunto) Validar() error {
	if c.Version == "" || c.FechaReferencia.IsZero() || len(c.Casos) < 40 {
		return errors.New("casos: versión, fecha y al menos cuarenta casos obligatorios")
	}
	vistos := make(map[string]bool)
	categorias := make(map[string]bool)
	for _, v := range c.Casos {
		if strings.TrimSpace(v.ID) == "" || vistos[v.ID] || v.Categoria == "" || strings.TrimSpace(v.Entrada) == "" || v.Esperadas == nil || len(v.Guion) == 0 || len(v.Guion) > 6 {
			return fmt.Errorf("casos: definición incompleta o duplicada: %s", v.ID)
		}
		vistos[v.ID] = true
		categorias[v.Categoria] = true
		if !slices.Contains([]string{"", "varios_destinatarios", "nombre_inyectado"}, v.Perfil) {
			return fmt.Errorf("casos: perfil desconocido: %s", v.ID)
		}
		for nombre, codigo := range v.ErroresHerramienta {
			if !herramientaConocida(nombre) || !slices.Contains([]string{"NotFound", "TooManyRequests", "ValidationError", "FunctionUnavailable", "RecipientNotFound", "SelfTransferNotAllowed"}, codigo) {
				return fmt.Errorf("casos: error de herramienta desconocido: %s", v.ID)
			}
		}
		for _, nombre := range v.OmitirHerramientas {
			if !herramientaConocida(nombre) {
				return fmt.Errorf("casos: herramienta omitida desconocida: %s", v.ID)
			}
		}
		if len(v.Restricciones.Contiene) == 0 && v.Restricciones.Error == "" && !v.Restricciones.Limitado {
			return fmt.Errorf("casos: falta oráculo de respuesta: %s", v.ID)
		}
		for _, l := range v.Esperadas {
			if l.Nombre == "" || l.Codigo == "" || !json.Valid(l.Argumentos) {
				return fmt.Errorf("casos: llamada esperada inválida: %s", v.ID)
			}
		}
		if v.Restricciones.Error != "" && errorDeGuion(v.Restricciones.Error) == nil {
			return fmt.Errorf("casos: error desconocido: %s", v.ID)
		}
		for _, p := range v.Guion {
			if p.Error != "" && errorDeGuion(p.Error) == nil {
				return fmt.Errorf("casos: error de proveedor desconocido: %s", v.ID)
			}
			formas := 0
			if p.Texto != "" {
				formas++
			}
			if len(p.Llamadas) > 0 {
				formas++
			}
			if p.Error != "" {
				formas++
			}
			if formas != 1 {
				return fmt.Errorf("casos: paso ambiguo: %s", v.ID)
			}
			for _, l := range p.Llamadas {
				if l.Nombre == "" || !json.Valid(l.Argumentos) {
					return fmt.Errorf("casos: guion inválido: %s", v.ID)
				}
			}
		}
	}
	for _, categoria := range []string{"consulta", "argumentos", "aclaracion", "fuera_alcance", "inyeccion", "errores"} {
		if !categorias[categoria] {
			return fmt.Errorf("casos: falta categoría %s", categoria)
		}
	}
	return nil
}

func herramientaConocida(nombre string) bool {
	return slices.Contains([]string{"consultar_saldo", "consultar_limites", "listar_movimientos", "resumir_movimientos", "buscar_destinatario", "consultar_transferencia", "preparar_transferencia"}, nombre)
}
