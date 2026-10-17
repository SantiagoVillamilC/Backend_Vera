package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/getkin/kin-openapi/openapi3"
)

// ClaseHerramienta limita las capacidades del catálogo: no existe escritura.
type ClaseHerramienta string

// Clases permitidas; el asistente carece de una clase de escritura.
const (
	Lectura     ClaseHerramienta = "lectura"
	Preparacion ClaseHerramienta = "preparacion"
)

// ManejadorHerramienta recibe identidad del contexto y argumentos ya validados.
// Las preparaciones sólo crean acciones pendientes, nunca ejecutan dinero.
type ManejadorHerramienta func(context.Context, identidad.Identidad, ArgumentosHerramienta) (DatosHerramienta, error)

type herramienta struct {
	declaracion FuncionIA
	clase       ClaseHerramienta
	esquema     *openapi3.Schema
	manejar     ManejadorHerramienta
}

// Catalogo es inmutable después de construirse y seguro para consultas concurrentes.
// Los manejadores inyectados deben ser seguros para uso concurrente.
type Catalogo struct {
	funciones map[string]herramienta
	orden     []string
	logger    *slog.Logger
}

// NuevoCatalogo sólo permite conectar las siete funciones conocidas. La ausencia
// de un manejador devuelve FunctionUnavailable, nunca una respuesta inventada.
func NuevoCatalogo(manejadores map[string]ManejadorHerramienta, logger *slog.Logger) (*Catalogo, error) {
	if logger == nil {
		logger = slog.Default()
	}
	c := &Catalogo{funciones: make(map[string]herramienta), logger: logger}
	for _, d := range declaracionesHerramientas() {
		var esquema openapi3.Schema
		if err := json.Unmarshal(d.Parametros, &esquema); err != nil {
			return nil, fmt.Errorf("catálogo: esquema de %s: %w", d.Nombre, err)
		}
		if err := esquema.Validate(context.Background()); err != nil {
			return nil, fmt.Errorf("catálogo: esquema de %s: %w", d.Nombre, err)
		}
		clase := Lectura
		if d.Nombre == "preparar_transferencia" {
			clase = Preparacion
		}
		c.funciones[d.Nombre] = herramienta{declaracion: d, clase: clase, esquema: &esquema, manejar: manejadores[d.Nombre]}
		c.orden = append(c.orden, d.Nombre)
	}
	for nombre := range manejadores {
		if _, ok := c.funciones[nombre]; !ok {
			return nil, errors.New("catálogo: manejador fuera de la lista permitida")
		}
	}
	return c, nil
}

// Declaraciones entrega copias independientes para ProveedorIA.
func (c *Catalogo) Declaraciones() []FuncionIA {
	r := make([]FuncionIA, 0, len(c.orden))
	for _, nombre := range c.orden {
		d := c.funciones[nombre].declaracion
		d.Parametros = bytes.Clone(d.Parametros)
		r = append(r, d)
	}
	return r
}

// Clase permite inspeccionar el catálogo sin exponer sus manejadores o esquemas.
func (c *Catalogo) Clase(nombre string) (ClaseHerramienta, bool) {
	f, ok := c.funciones[nombre]
	return f.clase, ok
}

// Ejecutar devuelve errores de función como datos correlacionados para el modelo.
// Cancelación y plazo se propagan como errores Go para detener al orquestador.
func (c *Catalogo) Ejecutar(ctx context.Context, llamada LlamadaIA) (resultado ResultadoIA, err error) {
	inicio := time.Now()
	resultado = ResultadoIA{ID: llamada.ID, Nombre: llamada.Nombre}
	codigo := "OK"
	nombreLog := "desconocida"
	argumentosLog := make(map[string]bool)
	defer func() {
		c.logger.InfoContext(ctx, "función del asistente", "funcion", nombreLog, "resultado", codigo, "duracion", time.Since(inicio), "argumentos", argumentosLog)
	}()
	fallo := func(cod string) (ResultadoIA, error) {
		codigo = cod
		resultado.Datos, _ = json.Marshal(struct {
			Error struct {
				Codigo string `json:"codigo"`
			} `json:"error"`
		}{Error: struct {
			Codigo string `json:"codigo"`
		}{Codigo: cod}})
		return resultado, nil
	}
	if err := ctx.Err(); err != nil {
		codigo = "Canceled"
		return resultado, err
	}
	f, ok := c.funciones[llamada.Nombre]
	if !ok {
		return fallo("UnknownFunction")
	}
	nombreLog = llamada.Nombre
	id, ok := identidad.DeContexto(ctx)
	if !ok || id.UsuarioID == "" {
		return fallo("IdentityRequired")
	}
	args, campos, err := validarArgumentos(llamada.Nombre, f.esquema, llamada.Argumentos)
	if err != nil {
		return fallo("ValidationError")
	}
	for campo := range campos {
		argumentosLog[campo] = true
	} // presencia, nunca valores ni PII
	if f.manejar == nil {
		return fallo("FunctionUnavailable")
	}
	datos, err := f.manejar(ctx, id, args)
	if ctx.Err() != nil {
		codigo = "Canceled"
		return resultado, ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		codigo = "Canceled"
		return resultado, err
	}
	if err != nil {
		return fallo(codigoErrorHerramienta(err))
	}
	resultado.Datos, err = serializarDatos(llamada.Nombre, args, datos)
	if err != nil {
		return fallo("InvalidFunctionResult")
	}
	return resultado, nil
}

// ErrorHerramienta es un código estable, nunca un mensaje libre del backend.
type ErrorHerramienta string

func (e ErrorHerramienta) Error() string { return string(e) }

// Códigos públicos permitidos en los resultados de herramientas.
const (
	ErrHerramientaNoEncontrado ErrorHerramienta = "NotFound"
	ErrHerramientaLimite       ErrorHerramienta = "TooManyRequests"
	ErrHerramientaValidacion   ErrorHerramienta = "ValidationError"
	ErrHerramientaNoDisponible ErrorHerramienta = "FunctionUnavailable"
)

func codigoErrorHerramienta(err error) string {
	for _, permitido := range []ErrorHerramienta{ErrHerramientaNoEncontrado, ErrHerramientaLimite, ErrHerramientaValidacion, ErrHerramientaNoDisponible} {
		if errors.Is(err, permitido) {
			return string(permitido)
		}
	}
	return "FunctionUnavailable"
}
