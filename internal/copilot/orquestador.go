package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// EntradaOrquestador referencia un mensaje de usuario ya persistido por el servidor.
type EntradaOrquestador struct{ ConversacionID, MensajeID, ReservaToken uuid.UUID }

// AccionOrquestador contiene exclusivamente datos releídos del almacenamiento del servidor.
type AccionOrquestador struct {
	ID                                       uuid.UUID
	Tipo, NombreMostrado, CelularEnmascarado string
	EntidadDestino                           string
	Monto                                    nucleo.Centavos
	RequiereCodigo                           bool
	ExpiraEn                                 time.Time
}

// SalidaOrquestador excluye los turnos internos, firmas y argumentos del modelo.
type SalidaOrquestador struct {
	Texto, VersionInstrucciones string
	TokensEntrada, TokensSalida int64
	Vueltas, Llamadas           int
	Limitado                    bool
	AccionPendiente             *AccionOrquestador
}

// RegistroCiclo persiste un turno del modelo y sus resultados como un bloque atómico.
type RegistroCiclo struct {
	Respuesta                    RespuestaIA
	Resultados                   []ResultadoIA
	Modelo, VersionInstrucciones string
	Latencia                     time.Duration
	Final                        bool
	AccionID                     uuid.UUID
}

// MemoriaCiclo valida titularidad y excluye ciclos simultáneos sobre la conversación.
type MemoriaCiclo interface {
	Abrir(context.Context, EntradaOrquestador) (SesionCiclo, error)
}

// SesionCiclo conserva el contexto autorizado sin mantener una transacción durante la red.
type SesionCiclo interface {
	Historial() []TurnoIA
	Guardar(context.Context, RegistroCiclo) error
	Accion(context.Context, uuid.UUID) (*AccionOrquestador, error)
	Cerrar(context.Context) error
}

// AutorizacionCiclo se consulta antes de cada vuelta y ejecución de herramientas.
type AutorizacionCiclo interface{ Exigir(context.Context) error }

// Orquestador coordina el proveedor y el catálogo; nunca ejecuta dinero.
type Orquestador struct {
	proveedor    ProveedorIA
	catalogo     *Catalogo
	memoria      MemoriaCiclo
	autorizacion AutorizacionCiclo
	modelo       string
	logger       *slog.Logger
	ahora        func() time.Time
	plazo        time.Duration
}

// OpcionOrquestador configura dependencias del ciclo antes de atender solicitudes.
type OpcionOrquestador func(*Orquestador)

// ConRelojOrquestador permite fijar la fecha de referencia de las evaluaciones.
func ConRelojOrquestador(ahora func() time.Time) OpcionOrquestador {
	return func(o *Orquestador) { o.ahora = ahora }
}

// NuevoOrquestador fija los topes de la HU: cinco vueltas, ocho funciones y 45 segundos.
// El proveedor debe venir decorado con el presupuesto de tokens compartido.
func NuevoOrquestador(p ProveedorIA, c *Catalogo, m MemoriaCiclo, a AutorizacionCiclo, modelo string, logger *slog.Logger, opciones ...OpcionOrquestador) (*Orquestador, error) {
	if p == nil || c == nil || m == nil || a == nil || strings.TrimSpace(modelo) == "" || len(modelo) > 120 {
		return nil, errors.New("asistente: configuración de orquestación incompleta")
	}
	if logger == nil {
		logger = slog.Default()
	}
	o := &Orquestador{proveedor: p, catalogo: c, memoria: m, autorizacion: a, modelo: modelo, logger: logger, ahora: time.Now, plazo: 45 * time.Second}
	for _, opcion := range opciones {
		opcion(o)
	}
	if o.ahora == nil {
		return nil, errors.New("asistente: reloj de orquestación ausente")
	}
	return o, nil
}

type claveConversacionCiclo struct{}

// ConversacionDelContexto entrega a herramientas la referencia que autorizó el servidor.
func ConversacionDelContexto(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(claveConversacionCiclo{}).(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// Responder completa el ciclo sobre el último mensaje de usuario de una conversación propia.
func (o *Orquestador) Responder(ctx context.Context, e EntradaOrquestador) (respuestaFinal SalidaOrquestador, errorFinal error) {
	defer func() {
		if recover() != nil {
			o.logger.ErrorContext(ctx, "ciclo del asistente interrumpido", "resultado", "InternalError", "version_instrucciones", VersionInstruccionesVera)
			respuestaFinal = SalidaOrquestador{}
			errorFinal = ErrNoDisponibleIA
		}
	}()
	var salida SalidaOrquestador
	if e.ConversacionID == uuid.Nil || e.MensajeID == uuid.Nil {
		return salida, ErrSolicitudIA
	}
	if _, err := parteDelAsistente(ctx); err != nil {
		return salida, err
	}
	ctx, cancel := context.WithTimeout(ctx, o.plazo)
	defer cancel()
	ctx = context.WithValue(ctx, claveConversacionCiclo{}, e.ConversacionID)
	if err := o.autorizacion.Exigir(ctx); err != nil {
		return salida, err
	}
	sesion, err := o.memoria.Abrir(ctx, e)
	if err != nil {
		return salida, err
	}
	ctx = context.WithValue(ctx, claveMensajeCiclo{}, e.MensajeID)
	defer func() {
		cierre, cancelar := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancelar()
		if err := sesion.Cerrar(cierre); err != nil {
			o.logger.ErrorContext(cierre, "cerrar ciclo del asistente", "resultado", "StorageUnavailable")
		}
	}()
	historial := sesion.Historial()
	if err := validarRolesHistorial(historial); err != nil {
		return salida, err
	}
	if len(historial) == 0 || (historial[len(historial)-1].Rol != "usuario" && historial[len(historial)-1].Rol != "herramienta") {
		return salida, ErrSolicitudIA
	}
	instrucciones := instruccionesDelCiclo(o.ahora())
	salida.VersionInstrucciones = VersionInstruccionesVera
	ids := make(map[string]bool)
	previas, funcionesPrevias, accionID, err := estadoPrevioCiclo(historial, ids)
	if err != nil {
		return SalidaOrquestador{}, err
	}
	salida.Llamadas = funcionesPrevias
	for vuelta := previas + 1; vuelta <= 5; vuelta++ {
		if err := ctx.Err(); err != nil {
			return SalidaOrquestador{}, err
		}
		if err := o.autorizacion.Exigir(ctx); err != nil {
			return SalidaOrquestador{}, err
		}
		inicio := time.Now()
		respuesta, err := o.proveedor.Generar(ctx, SolicitudIA{Instrucciones: instrucciones, Historial: historial, Funciones: o.catalogo.Declaraciones()})
		if ctx.Err() != nil {
			return SalidaOrquestador{}, ctx.Err()
		}
		if err != nil {
			return SalidaOrquestador{}, err
		}
		texto, llamadas, err := examinarRespuestaCiclo(respuesta, ids)
		if err != nil {
			return SalidaOrquestador{}, err
		}
		if revelaInstrucciones(texto) {
			o.logger.WarnContext(ctx, "respuesta del asistente bloqueada", "resultado", "InstructionDisclosure", "version_instrucciones", VersionInstruccionesVera)
			return SalidaOrquestador{}, ErrBloqueoIA
		}
		salida.Vueltas = vuelta
		salida.TokensEntrada += int64(respuesta.TokensEntrada)
		salida.TokensSalida += int64(respuesta.TokensSalida)
		registro := RegistroCiclo{Respuesta: respuesta, Modelo: o.modelo, VersionInstrucciones: VersionInstruccionesVera, Latencia: time.Since(inicio)}
		if len(llamadas) == 0 {
			salida.Texto = texto
			return o.finalizar(ctx, sesion, registro, salida, accionID)
		}
		if vuelta == 5 || salida.Llamadas+len(llamadas) > 8 {
			salida.Limitado = true
			salida.Texto = "No pude completar la consulta dentro del límite. Intenta reformular tu pregunta o dividirla en pasos más pequeños."
			o.logger.WarnContext(ctx, "tope del ciclo del asistente", "vueltas", vuelta, "llamadas", salida.Llamadas, "version_instrucciones", VersionInstruccionesVera)
			registro.Respuesta.Turno = TurnoIA{Rol: "asistente", Partes: []ParteIA{{Texto: salida.Texto}}}
			return o.finalizar(ctx, sesion, registro, salida, accionID)
		}
		resultados, err := o.ejecutar(ctx, llamadas)
		if err != nil {
			return SalidaOrquestador{}, err
		}
		salida.Llamadas += len(llamadas)
		registro.Resultados = resultados
		if err := sesion.Guardar(ctx, registro); err != nil {
			return SalidaOrquestador{}, err
		}
		historial = append(historial, respuesta.Turno)
		turno := TurnoIA{Rol: "herramienta", Partes: make([]ParteIA, 0, len(resultados))}
		for i := range resultados {
			resultado := resultados[i]
			turno.Partes = append(turno.Partes, ParteIA{Resultado: &resultado})
			if id := accionDeResultado(resultado); id != uuid.Nil {
				accionID = id
			}
		}
		historial = append(historial, turno)
	}
	return SalidaOrquestador{}, ErrRespuestaIA
}

func accionDeResultado(r ResultadoIA) uuid.UUID {
	if r.Nombre != "preparar_transferencia" {
		return uuid.Nil
	}
	var datos struct {
		Datos struct {
			AccionID uuid.UUID `json:"accion_id"`
		} `json:"datos"`
	}
	if json.Unmarshal(r.Datos, &datos) != nil {
		return uuid.Nil
	}
	return datos.Datos.AccionID
}

// Reanudar un mensaje interrumpido no reinicia sus topes ni vuelve a ejecutar funciones guardadas.
func estadoPrevioCiclo(historial []TurnoIA, ids map[string]bool) (int, int, uuid.UUID, error) {
	ultimo := -1
	for i, t := range historial {
		if t.Rol == "usuario" {
			ultimo = i
		}
	}
	if ultimo < 0 {
		return 0, 0, uuid.Nil, ErrSolicitudIA
	}
	vueltas, llamadas := 0, 0
	var accion uuid.UUID
	for i := ultimo + 1; i < len(historial); i += 2 {
		if i+1 >= len(historial) || historial[i+1].Rol != "herramienta" {
			return 0, 0, uuid.Nil, ErrRespuestaIA
		}
		_, funciones, err := examinarRespuestaCiclo(RespuestaIA{Turno: historial[i]}, ids)
		resultados := historial[i+1].Partes
		if err != nil || len(funciones) == 0 || len(funciones) != len(resultados) {
			return 0, 0, uuid.Nil, ErrRespuestaIA
		}
		for j, p := range resultados {
			if p.Resultado == nil || p.Resultado.ID != funciones[j].ID || p.Resultado.Nombre != funciones[j].Nombre {
				return 0, 0, uuid.Nil, ErrRespuestaIA
			}
			if id := accionDeResultado(*p.Resultado); id != uuid.Nil {
				accion = id
			}
		}
		vueltas++
		llamadas += len(funciones)
	}
	if vueltas >= 5 || llamadas > 8 {
		return 0, 0, uuid.Nil, ErrRespuestaIA
	}
	return vueltas, llamadas, accion, nil
}

func (o *Orquestador) finalizar(ctx context.Context, s SesionCiclo, r RegistroCiclo, salida SalidaOrquestador, accion uuid.UUID) (SalidaOrquestador, error) {
	if err := ctx.Err(); err != nil {
		return SalidaOrquestador{}, err
	}
	if accion != uuid.Nil {
		a, err := s.Accion(ctx, accion)
		if err != nil {
			return SalidaOrquestador{}, err
		}
		if a == nil || a.ID != accion {
			return SalidaOrquestador{}, ErrCicloNoDisponible
		}
		salida.AccionPendiente = a
		r.AccionID = accion
	}
	r.Final = true
	// El protocolo conserva firmas para vueltas futuras; Salida sólo proyecta texto.
	if err := s.Guardar(ctx, r); err != nil {
		return SalidaOrquestador{}, err
	}
	return salida, nil
}

func examinarRespuestaCiclo(r RespuestaIA, ids map[string]bool) (string, []LlamadaIA, error) {
	if r.Turno.Rol != "asistente" || len(r.Turno.Partes) == 0 || r.TokensEntrada < 0 || r.TokensSalida < 0 {
		return "", nil, ErrRespuestaIA
	}
	textos := make([]string, 0, len(r.Turno.Partes))
	llamadas := make([]LlamadaIA, 0)
	for _, p := range r.Turno.Partes {
		if p.Audio != nil || p.Resultado != nil || (p.Texto != "") == (p.Llamada != nil) {
			return "", nil, ErrRespuestaIA
		}
		if p.Llamada != nil {
			f := *p.Llamada
			if f.Nombre == "" || len(f.ID) > 256 || !json.Valid(f.Argumentos) || !bytes.HasPrefix(bytes.TrimSpace(f.Argumentos), []byte("{")) {
				return "", nil, ErrRespuestaIA
			}
			if f.ID != "" {
				if ids[f.ID] {
					return "", nil, ErrRespuestaIA
				}
				ids[f.ID] = true
			}
			llamadas = append(llamadas, f)
		} else {
			if !utf8.ValidString(p.Texto) {
				return "", nil, ErrRespuestaIA
			}
			textos = append(textos, p.Texto)
		}
	}
	texto := strings.TrimSpace(strings.Join(textos, "\n"))
	if len(llamadas) == 0 && (texto == "" || len(texto) > 16384) {
		return "", nil, ErrRespuestaIA
	}
	return texto, llamadas, nil
}

func (o *Orquestador) ejecutar(ctx context.Context, llamadas []LlamadaIA) ([]ResultadoIA, error) {
	resultados := make([]ResultadoIA, len(llamadas))
	for inicio := 0; inicio < len(llamadas); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clase, _ := o.catalogo.Clase(llamadas[inicio].Nombre)
		fin := inicio + 1
		if clase != Preparacion {
			for fin < len(llamadas) {
				clase, _ = o.catalogo.Clase(llamadas[fin].Nombre)
				if clase == Preparacion {
					break
				}
				fin++
			}
		}
		errores := make([]error, fin-inicio)
		ejecutar := func(i int) {
			if err := o.autorizacion.Exigir(ctx); err != nil {
				errores[i-inicio] = err
				return
			}
			resultados[i], errores[i-inicio] = o.catalogo.Ejecutar(ctx, llamadas[i])
		}
		if fin-inicio == 1 {
			ejecutar(inicio)
		} else {
			var wg sync.WaitGroup
			for i := inicio; i < fin; i++ {
				wg.Go(func() { ejecutar(i) })
			}
			wg.Wait()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		for _, err := range errores {
			if err != nil {
				return nil, err
			}
		}
		inicio = fin
	}
	return resultados, nil
}
