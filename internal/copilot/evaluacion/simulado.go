package evaluacion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
)

// ResultadoCaso publica aserciones sin copiar entradas, instrucciones ni datos financieros.
type ResultadoCaso struct {
	ID                  string   `json:"id"`
	Categoria           string   `json:"categoria"`
	Aprobado            bool     `json:"aprobado"`
	FuncionesCorrectas  bool     `json:"funciones_correctas"`
	ArgumentosCorrectos bool     `json:"argumentos_correctos"`
	Fallos              []string `json:"fallos"`
}

// Informe es reproducible y se puede archivar como evidencia de CI.
// No estima calidad del modelo, costo ni latencia del proveedor real.
type Informe struct {
	Modo                 string          `json:"modo"`
	VersionCasos         string          `json:"version_casos"`
	CasosSHA256          string          `json:"casos_sha256"`
	VersionInstrucciones string          `json:"version_instrucciones"`
	Total                int             `json:"total"`
	Correctos            int             `json:"correctos"`
	Aprobado             bool            `json:"aprobado"`
	Resultados           []ResultadoCaso `json:"resultados"`
}

// EjecutarSimulados exige que todos los casos cumplan sus restricciones.
func EjecutarSimulados(ctx context.Context, c Conjunto) (Informe, error) {
	if err := c.Validar(); err != nil {
		return Informe{}, err
	}
	datos, err := json.Marshal(c)
	if err != nil {
		return Informe{}, err
	}
	huella := sha256.Sum256(datos)
	informe := Informe{Modo: "simulado", VersionCasos: c.Version, VersionInstrucciones: copilot.VersionInstruccionesVera, Total: len(c.Casos), Resultados: make([]ResultadoCaso, 0, len(c.Casos))}
	informe.CasosSHA256 = hex.EncodeToString(huella[:])
	for _, caso := range c.Casos {
		if err := ctx.Err(); err != nil {
			return Informe{}, err
		}
		r, err := EjecutarCaso(ctx, c.FechaReferencia, caso)
		if err != nil {
			return Informe{}, err
		}
		informe.Resultados = append(informe.Resultados, r)
		if r.Aprobado {
			informe.Correctos++
		}
	}
	informe.Aprobado = informe.Correctos == informe.Total
	return informe, nil
}

type proveedorObservado struct {
	interno          copilot.ProveedorIA
	llamadas         []copilot.LlamadaIA
	vueltas          int
	fecha            time.Time
	contextoCorrecto bool
}

func (p *proveedorObservado) Generar(ctx context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
	p.vueltas++
	fecha := p.fecha.In(time.FixedZone("America/Bogota", -5*60*60)).Format(time.RFC3339)
	if !strings.Contains(s.Instrucciones, fecha) || !strings.Contains(s.Instrucciones, copilot.VersionInstruccionesVera) || len(s.Funciones) != 7 {
		p.contextoCorrecto = false
	}
	r, err := p.interno.Generar(ctx, s)
	for _, parte := range r.Turno.Partes {
		if parte.Llamada != nil {
			p.llamadas = append(p.llamadas, *parte.Llamada)
		}
	}
	return r, err
}

func pasosSimulados(c Caso) []provsim.Paso {
	pasos := make([]provsim.Paso, 0, len(c.Guion))
	for i, p := range c.Guion {
		rol := p.Rol
		if rol == "" {
			rol = "asistente"
		}
		turno := copilot.TurnoIA{Rol: rol}
		if p.Texto != "" {
			turno.Partes = append(turno.Partes, copilot.ParteIA{Texto: p.Texto})
		}
		for j, l := range p.Llamadas {
			turno.Partes = append(turno.Partes, copilot.ParteIA{Llamada: &copilot.LlamadaIA{ID: fmt.Sprintf("%s-%d-%d", c.ID, i, j), Nombre: l.Nombre, Argumentos: l.Argumentos}})
		}
		pasos = append(pasos, provsim.Paso{Respuesta: copilot.RespuestaIA{Turno: turno}, Error: errorDeGuion(p.Error)})
	}
	return pasos
}

type autorizacionEscenario struct{ denegada bool }

func (a autorizacionEscenario) Exigir(context.Context) error {
	if a.denegada {
		return copilot.ErrConsentimientoAsistente
	}
	return nil
}

type memoriaEscenario struct {
	entrada          string
	registros        []copilot.RegistroCiclo
	accion           *copilot.AccionOrquestador
	abierta, cerrada bool
}

func (m *memoriaEscenario) Abrir(context.Context, copilot.EntradaOrquestador) (copilot.SesionCiclo, error) {
	m.abierta = true
	return m, nil
}
func (m *memoriaEscenario) Historial() []copilot.TurnoIA {
	return []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: m.entrada}}}}
}
func (m *memoriaEscenario) Guardar(_ context.Context, r copilot.RegistroCiclo) error {
	m.registros = append(m.registros, r)
	return nil
}
func (m *memoriaEscenario) Accion(_ context.Context, id uuid.UUID) (*copilot.AccionOrquestador, error) {
	if m.accion == nil || m.accion.ID != id {
		return nil, copilot.ErrCicloNoDisponible
	}
	return m.accion, nil
}
func (m *memoriaEscenario) Cerrar(context.Context) error { m.cerrada = true; return nil }

// EjecutarCaso usa el orquestador y el catálogo reales con fixtures de puertos sin red.
// Los guiones y resultados esperados se leen de campos distintos del caso.
func EjecutarCaso(ctx context.Context, fecha time.Time, c Caso) (ResultadoCaso, error) {
	p := &proveedorObservado{interno: provsim.Nuevo(pasosSimulados(c)...), fecha: fecha, contextoCorrecto: true}
	m := &memoriaEscenario{entrada: c.Entrada}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalogo, err := copilot.NuevoCatalogo(herramientasSimuladas(c, fecha, m), logger)
	if err != nil {
		return ResultadoCaso{}, err
	}
	o, err := copilot.NuevoOrquestador(p, catalogo, m, autorizacionEscenario{c.SinConsentimiento}, "simulado-evaluacion", logger, copilot.ConRelojOrquestador(func() time.Time { return fecha }))
	if err != nil {
		return ResultadoCaso{}, err
	}
	id := identidad.Identidad{ParteID: parteEscenario.String(), UsuarioID: usuarioEscenario.String()}
	ctx = identidad.ConIdentidad(ctx, id)
	salida, errCiclo := o.Responder(ctx, copilot.EntradaOrquestador{ConversacionID: conversacionEscenario, MensajeID: mensajeEscenario})
	r := ResultadoCaso{ID: c.ID, Categoria: c.Categoria, FuncionesCorrectas: true, ArgumentosCorrectos: true, Fallos: make([]string, 0)}
	fallo := func(codigo string) { r.Fallos = append(r.Fallos, codigo) }
	if !errors.Is(errCiclo, errorDeGuion(c.Restricciones.Error)) {
		fallo("error_de_ciclo")
	}
	if len(p.llamadas) != len(c.Esperadas) {
		r.FuncionesCorrectas = false
		r.ArgumentosCorrectos = false
	}
	resultados := make([]copilot.ResultadoIA, 0)
	datos := make([]json.RawMessage, 0)
	for _, registro := range m.registros {
		for _, v := range registro.Resultados {
			resultados = append(resultados, v)
			datos = append(datos, v.Datos)
		}
	}
	for i, e := range c.Esperadas {
		if i >= len(p.llamadas) {
			break
		}
		actual := p.llamadas[i]
		if actual.Nombre != e.Nombre {
			r.FuncionesCorrectas = false
		}
		if !jsonEquivalente(actual.Argumentos, e.Argumentos) {
			r.ArgumentosCorrectos = false
		}
		codigo := "NoEjecutada"
		for _, v := range resultados {
			if v.ID == actual.ID {
				var sobre struct {
					Error *struct {
						Codigo string `json:"codigo"`
					} `json:"error"`
				}
				switch {
				case json.Unmarshal(v.Datos, &sobre) != nil:
					codigo = "ResultadoInvalido"
				case sobre.Error != nil:
					codigo = sobre.Error.Codigo
				default:
					codigo = "OK"
				}
			}
		}
		if codigo != e.Codigo {
			fallo("codigo_de_funcion")
		}
	}
	if !r.FuncionesCorrectas {
		fallo("eleccion_de_funcion")
	}
	if !r.ArgumentosCorrectos {
		fallo("argumentos")
	}
	for _, texto := range c.Restricciones.Contiene {
		if !strings.Contains(strings.ToLower(salida.Texto), strings.ToLower(texto)) {
			fallo("texto_requerido")
		}
	}
	for _, texto := range c.Restricciones.Prohibidos {
		if strings.Contains(strings.ToLower(salida.Texto), strings.ToLower(texto)) {
			fallo("texto_prohibido")
		}
	}
	if !montosVerificables(salida.Texto, datos) {
		fallo("monto_no_verificable")
	}
	if (salida.AccionPendiente != nil) != c.Restricciones.Accion {
		fallo("tarjeta_de_accion")
	}
	if salida.AccionPendiente != nil && !reflect.DeepEqual(salida.AccionPendiente, m.accion) {
		fallo("tarjeta_no_proviene_del_servidor")
	}
	if salida.Limitado != c.Restricciones.Limitado {
		fallo("limite_del_ciclo")
	}
	if !p.contextoCorrecto {
		fallo("instrucciones_o_fecha")
	}
	vueltas := len(c.Guion)
	if c.SinConsentimiento {
		vueltas = 0
	}
	if p.vueltas != vueltas {
		fallo("consumo_del_guion")
	}
	if m.abierta && !m.cerrada {
		fallo("cierre_de_sesion")
	}
	r.Aprobado = len(r.Fallos) == 0
	return r, nil
}

func jsonEquivalente(a, b json.RawMessage) bool {
	var x, y any
	dx, dy := json.NewDecoder(strings.NewReader(string(a))), json.NewDecoder(strings.NewReader(string(b)))
	dx.UseNumber()
	dy.UseNumber()
	return dx.Decode(&x) == nil && dy.Decode(&y) == nil && reflect.DeepEqual(x, y)
}

func errorDeGuion(codigo string) error {
	switch codigo {
	case "":
		return nil
	case "AssistantUnavailable":
		return copilot.ErrNoDisponibleIA
	case "Timeout":
		return copilot.ErrTimeoutIA
	case "Blocked":
		return copilot.ErrBloqueoIA
	case "InvalidResponse":
		return copilot.ErrRespuestaIA
	case "InvalidRequest":
		return copilot.ErrSolicitudIA
	case "ConsentRequired":
		return copilot.ErrConsentimientoAsistente
	case "ScriptExhausted":
		return copilot.ErrGuionAgotadoIA
	default:
		return nil
	}
}
