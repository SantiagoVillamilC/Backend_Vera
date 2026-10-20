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
	Tipo   string          `json:"tipo"`
	Nombre string          `json:"nombre"`
	Datos  json.RawMessage `json:"datos"`
}

// Consola mantiene una conversación local con herramientas financieras ficticias.
// No es un servicio de producción ni tiene un puerto para ejecutar dinero.
type Consola struct {
	presupuesto         *proveedorMedido
	memoria             *memoriaConsola
	modelo              string
	ahora               func() time.Time
	proveedor           copilot.ProveedorIA
	recuperar           bool
	pausaChat, pausaVoz pausaProveedor
}

// NuevaConsola comparte el límite entre preguntas, incluso después de /nuevo.
func NuevaConsola(cfg ConfigReal, p copilot.ProveedorIA, observar func(EventoConsola), ahora func() time.Time, opciones ...OpcionConsola) (*Consola, error) {
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
	c := &Consola{presupuesto: &proveedorMedido{interno: proveedorConsola{p, observar}, max: cfg.MaxSolicitudes, intervalo: cfg.Intervalo, sinMarcador: true}, memoria: m, modelo: cfg.Modelo, ahora: ahora}
	for _, opcion := range opciones {
		opcion(c)
	}
	c.proveedor = proveedorConRecuperacion{c}
	return c, nil
}

// Consumo informa solicitudes al proveedor, no mensajes escritos por el usuario.
func (c *Consola) Consumo() (int, int) { return c.presupuesto.solicitudes, c.presupuesto.max }

// Transcribir comparte con el chat el presupuesto de solicitudes del laboratorio.
func (c *Consola) Transcribir(ctx context.Context, audio *copilot.AudioIA) (string, error) {
	return copilot.TranscribirAudio(ctx, c.proveedor, audio)
}

// Sintetizar reserva una llamada del mismo presupuesto antes de generar voz.
// El servidor serializa su uso junto al chat y la transcripción.
func (c *Consola) Sintetizar(ctx context.Context, p copilot.SintetizadorVoz, texto, voz string) ([]byte, error) {
	if p == nil {
		return nil, copilot.ErrNoDisponibleIA
	}
	return c.sintetizarControlado(ctx, func() ([]byte, error) { return p.Sintetizar(ctx, texto, voz) }, nil)
}

// SintetizarFlujo cuenta toda la transmisión como una sola llamada del presupuesto común.
func (c *Consola) SintetizarFlujo(ctx context.Context, p copilot.SintetizadorVozFlujo, texto, voz string, recibir func([]byte) error) ([]byte, error) {
	if p == nil {
		return nil, copilot.ErrNoDisponibleIA
	}
	emitido := false
	return c.sintetizarControlado(ctx, func() ([]byte, error) {
		return p.SintetizarFlujo(ctx, texto, voz, func(b []byte) error { emitido = emitido || len(b) > 0; return recibir(b) })
	}, &emitido)
}

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
	opciones := []copilot.OpcionOrquestador{copilot.ConRelojOrquestador(c.ahora)}
	if c.recuperar {
		opciones = append(opciones, copilot.ConConfirmacionPreparacionServidor())
	}
	o, err := copilot.NuevoOrquestador(c.proveedor, catalogo, c.memoria, autorizacionEscenario{}, c.modelo, logger, opciones...)
	if err != nil {
		return copilot.SalidaOrquestador{}, err
	}
	inicio := len(c.memoria.historial)
	estadoAnterior := *c.memoria.memoriaEscenario
	c.memoria.historial = append(c.memoria.historial, copilot.TurnoIA{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: pregunta}}})
	ctx = identidad.ConIdentidad(ctx, identidad.Identidad{ParteID: parteEscenario.String(), UsuarioID: usuarioEscenario.String()})
	salida, err := o.Responder(ctx, copilot.EntradaOrquestador{ConversacionID: conversacionEscenario, MensajeID: uuid.New()})
	if err != nil {
		c.memoria.historial = c.memoria.historial[:inicio]
		*c.memoria.memoriaEscenario = estadoAnterior
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
