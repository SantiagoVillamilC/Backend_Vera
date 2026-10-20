package evaluacion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

// EventoConsola proyecta sólo llamadas y resultados públicos del sandbox.
// No expone instrucciones, firmas, configuración ni cuerpos de error del proveedor.
type EventoConsola struct {
	Tipo, Nombre string
	Datos        json.RawMessage
}

// Consola mantiene una conversación local con herramientas financieras ficticias.
// No es un servicio de producción ni tiene un puerto para ejecutar dinero.
type Consola struct {
	presupuesto *proveedorMedido
	memoria     *memoriaConsola
	modelo      string
	ahora       func() time.Time
}

// NuevaConsola comparte el límite entre preguntas, incluso después de /nuevo.
func NuevaConsola(cfg ConfigReal, p copilot.ProveedorIA, observar func(EventoConsola), ahora func() time.Time) (*Consola, error) {
	if err := validarConfigReal(cfg); err != nil {
		return nil, err
	}
	if p == nil || ahora == nil {
		return nil, errors.New("consola: proveedor y reloj obligatorios")
	}
	if observar == nil {
		observar = func(EventoConsola) {}
	}
	m := &memoriaConsola{memoriaEscenario: &memoriaEscenario{}, observar: observar}
	return &Consola{presupuesto: &proveedorMedido{interno: proveedorConsola{p, observar}, max: cfg.MaxSolicitudes, intervalo: cfg.Intervalo}, memoria: m, modelo: cfg.Modelo, ahora: ahora}, nil
}

// Consumo informa solicitudes al proveedor, no mensajes escritos por el usuario.
func (c *Consola) Consumo() (int, int) { return c.presupuesto.solicitudes, c.presupuesto.max }

// NuevaConversacion borra el contexto sintético sin restablecer la cuota.
func (c *Consola) NuevaConversacion() {
	c.memoria.historial = nil
	*c.memoria.memoriaEscenario = memoriaEscenario{}
}

// Preguntar ejecuta el orquestador y catálogo de producción sobre datos ficticios.
func (c *Consola) Preguntar(ctx context.Context, pregunta string) (copilot.SalidaOrquestador, error) {
	if strings.TrimSpace(pregunta) == "" || len(pregunta) > 8192 {
		return copilot.SalidaOrquestador{}, copilot.ErrSolicitudIA
	}
	if c.presupuesto.solicitudes >= c.presupuesto.max {
		return copilot.SalidaOrquestador{}, ErrCuotaEvaluacion
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalogo, err := copilot.NuevoCatalogo(herramientasSimuladas(Caso{}, c.ahora(), c.memoria.memoriaEscenario), logger)
	if err != nil {
		return copilot.SalidaOrquestador{}, err
	}
	o, err := copilot.NuevoOrquestador(c.presupuesto, catalogo, c.memoria, autorizacionEscenario{}, c.modelo, logger, copilot.ConRelojOrquestador(c.ahora))
	if err != nil {
		return copilot.SalidaOrquestador{}, err
	}
	inicio := len(c.memoria.historial)
	accionAnterior := c.memoria.accion
	c.memoria.historial = append(c.memoria.historial, copilot.TurnoIA{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: pregunta}}})
	ctx = identidad.ConIdentidad(ctx, identidad.Identidad{ParteID: parteEscenario.String(), UsuarioID: usuarioEscenario.String()})
	salida, err := o.Responder(ctx, copilot.EntradaOrquestador{ConversacionID: conversacionEscenario, MensajeID: uuid.New()})
	if err != nil {
		c.memoria.historial = c.memoria.historial[:inicio]
		c.memoria.accion = accionAnterior
	}
	return salida, err
}

type proveedorConsola struct {
	interno  copilot.ProveedorIA
	observar func(EventoConsola)
}

func (p proveedorConsola) Generar(ctx context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	r, err := p.interno.Generar(ctx, s)
	if err == nil {
		for _, parte := range r.Turno.Partes {
			if parte.Llamada != nil {
				p.observar(EventoConsola{Tipo: "funcion", Nombre: parte.Llamada.Nombre, Datos: append(json.RawMessage(nil), parte.Llamada.Argumentos...)})
			}
		}
	}
	return r, err
}

type memoriaConsola struct {
	*memoriaEscenario
	historial []copilot.TurnoIA
	observar  func(EventoConsola)
}

func (m *memoriaConsola) Abrir(context.Context, copilot.EntradaOrquestador) (copilot.SesionCiclo, error) {
	return m, nil
}
func (m *memoriaConsola) Historial() []copilot.TurnoIA { return m.historial }
func (m *memoriaConsola) Guardar(_ context.Context, r copilot.RegistroCiclo) error {
	m.historial = append(m.historial, r.Respuesta.Turno)
	if len(r.Resultados) > 0 {
		turno := copilot.TurnoIA{Rol: "herramienta", Partes: make([]copilot.ParteIA, 0, len(r.Resultados))}
		for _, resultado := range r.Resultados {
			turno.Partes = append(turno.Partes, copilot.ParteIA{Resultado: &resultado})
			m.observar(EventoConsola{Tipo: "resultado", Nombre: resultado.Nombre, Datos: append(json.RawMessage(nil), resultado.Datos...)})
		}
		m.historial = append(m.historial, turno)
	}
	return nil
}
