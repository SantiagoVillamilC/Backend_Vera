package ollama

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// Los nombres de esta traducción siguen el contrato de /api/chat: mensajes con
// rol, texto, llamadas y resultados correlacionados por nombre de herramienta.
type mensaje struct {
	Rol         string          `json:"role"`
	Contenido   string          `json:"content"`
	Llamadas    []llamadaSalida `json:"tool_calls,omitempty"`
	Herramienta string          `json:"tool_name,omitempty"`
}

type llamadaSalida struct {
	ID      string        `json:"id,omitempty"`
	Funcion funcionSalida `json:"function"`
}

type funcionSalida struct {
	Nombre     string          `json:"name"`
	Argumentos json.RawMessage `json:"arguments"`
}

type herramienta struct {
	Tipo    string      `json:"type"`
	Funcion declaracion `json:"function"`
}

type declaracion struct {
	Nombre      string          `json:"name"`
	Descripcion string          `json:"description"`
	Parametros  json.RawMessage `json:"parameters"`
}

type opciones struct {
	Temperatura float32 `json:"temperature"`
	NumCtx      int     `json:"num_ctx"`
	NumPredict  int     `json:"num_predict"`
}

// peticion no declara think: Ollama 0.40.10 con Gemma 4 deja de emitir
// tool_calls cuando el campo viaja en false, así que se omite por completo.
type peticion struct {
	Modelo       string        `json:"model"`
	Mensajes     []mensaje     `json:"messages"`
	Herramientas []herramienta `json:"tools,omitempty"`
	Stream       bool          `json:"stream"`
	Opciones     opciones      `json:"options"`
	KeepAlive    string        `json:"keep_alive"`
}

func traducirSolicitud(s copilot.SolicitudIA, cfg Config) ([]byte, error) {
	// La transcripción local usa sttlocal: Ollama sólo recibe conversación.
	if s.SoloTranscripcion || len(s.Historial) == 0 {
		return nil, copilot.ErrSolicitudIA
	}
	p := peticion{
		Modelo: cfg.Modelo, KeepAlive: keepAlive,
		Opciones: opciones{Temperatura: cfg.Temperatura, NumCtx: numCtx, NumPredict: numPredict},
	}
	if s.Instrucciones != "" {
		p.Mensajes = append(p.Mensajes, mensaje{Rol: "system", Contenido: s.Instrucciones})
	}
	nombres := make(map[string]bool, len(s.Funciones))
	for _, f := range s.Funciones {
		h, err := traducirHerramienta(f)
		if err != nil || nombres[f.Nombre] {
			return nil, copilot.ErrSolicitudIA
		}
		nombres[f.Nombre] = true
		p.Herramientas = append(p.Herramientas, h)
	}
	for _, turno := range s.Historial {
		ms, err := traducirTurno(turno)
		if err != nil {
			return nil, err
		}
		p.Mensajes = append(p.Mensajes, ms...)
	}
	cuerpo, err := json.Marshal(p)
	if err != nil {
		return nil, copilot.ErrSolicitudIA
	}
	return cuerpo, nil
}

// traducirHerramienta conserva los parámetros byte a byte: el esquema lo valida
// el proveedor tal como lo declaró el catálogo, sin reordenar ni redondear.
func traducirHerramienta(f copilot.FuncionIA) (herramienta, error) {
	if strings.TrimSpace(f.Nombre) == "" {
		return herramienta{}, copilot.ErrSolicitudIA
	}
	esquema, err := objeto(f.Parametros)
	if err != nil || esquema["type"] != "object" {
		return herramienta{}, copilot.ErrSolicitudIA
	}
	return herramienta{Tipo: "function", Funcion: declaracion{Nombre: f.Nombre, Descripcion: f.Descripcion, Parametros: f.Parametros}}, nil
}

func traducirTurno(t copilot.TurnoIA) ([]mensaje, error) {
	switch t.Rol {
	case "usuario":
		return traducirUsuario(t)
	case "asistente":
		return traducirAsistente(t)
	case "herramienta":
		return traducirHerramientaResultados(t)
	default:
		return nil, copilot.ErrSolicitudIA
	}
}

func traducirUsuario(t copilot.TurnoIA) ([]mensaje, error) {
	if len(t.Partes) == 0 {
		return nil, copilot.ErrSolicitudIA
	}
	var texto strings.Builder
	for _, parte := range t.Partes {
		// Ollama no acepta audio, llamadas ni resultados en un turno de usuario.
		if err := unaParte(parte, parte.Texto != ""); err != nil {
			return nil, err
		}
		texto.WriteString(parte.Texto)
	}
	return []mensaje{{Rol: "user", Contenido: texto.String()}}, nil
}

func traducirAsistente(t copilot.TurnoIA) ([]mensaje, error) {
	if len(t.Partes) == 0 {
		return nil, copilot.ErrSolicitudIA
	}
	m := mensaje{Rol: "assistant"}
	var texto strings.Builder
	for _, parte := range t.Partes {
		if err := unaParte(parte, parte.Texto != "" || parte.Llamada != nil); err != nil {
			return nil, err
		}
		if parte.Llamada == nil {
			texto.WriteString(parte.Texto)
			continue
		}
		argumentos, err := argumentosLlamada(parte.Llamada)
		if err != nil {
			return nil, err
		}
		m.Llamadas = append(m.Llamadas, llamadaSalida{ID: parte.Llamada.ID, Funcion: funcionSalida{Nombre: parte.Llamada.Nombre, Argumentos: argumentos}})
	}
	m.Contenido = texto.String()
	return []mensaje{m}, nil
}

// traducirHerramientaResultados emite un mensaje por resultado, en el orden
// original, porque /api/chat correlaciona por nombre y no por identificador.
func traducirHerramientaResultados(t copilot.TurnoIA) ([]mensaje, error) {
	if len(t.Partes) == 0 {
		return nil, copilot.ErrSolicitudIA
	}
	salida := make([]mensaje, 0, len(t.Partes))
	for _, parte := range t.Partes {
		if err := unaParte(parte, parte.Resultado != nil); err != nil {
			return nil, err
		}
		if strings.TrimSpace(parte.Resultado.Nombre) == "" {
			return nil, copilot.ErrSolicitudIA
		}
		contenido, err := contenidoResultado(parte.Resultado.Datos)
		if err != nil {
			return nil, copilot.ErrSolicitudIA
		}
		salida = append(salida, mensaje{Rol: "tool", Herramienta: parte.Resultado.Nombre, Contenido: contenido})
	}
	return salida, nil
}

func argumentosLlamada(l *copilot.LlamadaIA) (json.RawMessage, error) {
	if strings.TrimSpace(l.Nombre) == "" {
		return nil, copilot.ErrSolicitudIA
	}
	datos, err := objeto(l.Argumentos)
	if err != nil || !numerosSeguros(datos) {
		return nil, copilot.ErrSolicitudIA
	}
	return l.Argumentos, nil
}

func contenidoResultado(datos json.RawMessage) (string, error) {
	if _, err := objeto(datos); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, datos); err != nil {
		return "", copilot.ErrSolicitudIA
	}
	return buf.String(), nil
}

// unaParte exige exactamente una representación por parte y la que el rol admite.
func unaParte(p copilot.ParteIA, admitida bool) error {
	tipos := 0
	for _, presente := range []bool{p.Texto != "", p.Audio != nil, p.Llamada != nil, p.Resultado != nil} {
		if presente {
			tipos++
		}
	}
	if tipos != 1 || !admitida {
		return copilot.ErrSolicitudIA
	}
	return nil
}

type respuestaServidor struct {
	Mensaje struct {
		Rol       string             `json:"role"`
		Contenido string             `json:"content"`
		Llamadas  []llamadaRespuesta `json:"tool_calls"`
	} `json:"message"`
	Hecho         bool   `json:"done"`
	Motivo        string `json:"done_reason"`
	TokensEntrada int32  `json:"prompt_eval_count"`
	TokensSalida  int32  `json:"eval_count"`
}

type llamadaRespuesta struct {
	ID      string `json:"id"`
	Funcion struct {
		Nombre     string          `json:"name"`
		Argumentos json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// traducirRespuesta rechaza respuestas cortadas: done en false o un motivo
// distinto de stop significan que el turno quedó incompleto.
func traducirRespuesta(b []byte) (copilot.RespuestaIA, bool, error) {
	var raw respuestaServidor
	if err := json.Unmarshal(b, &raw); err != nil {
		return copilot.RespuestaIA{}, false, copilot.ErrRespuestaIA
	}
	r := copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente"}, TokensEntrada: raw.TokensEntrada, TokensSalida: raw.TokensSalida}
	if !raw.Hecho || (raw.Motivo != "" && raw.Motivo != "stop") {
		return r, false, copilot.ErrRespuestaIA
	}
	var partes []copilot.ParteIA
	if raw.Mensaje.Contenido != "" {
		partes = append(partes, copilot.ParteIA{Texto: raw.Mensaje.Contenido})
	}
	for _, l := range raw.Mensaje.Llamadas {
		if strings.TrimSpace(l.Funcion.Nombre) == "" {
			return r, false, copilot.ErrRespuestaIA
		}
		argumentos := l.Funcion.Argumentos
		if len(argumentos) == 0 || string(argumentos) == "null" {
			argumentos = json.RawMessage(`{}`)
		}
		datos, err := objeto(argumentos)
		if err != nil || !numerosSeguros(datos) {
			return r, false, copilot.ErrRespuestaIA
		}
		partes = append(partes, copilot.ParteIA{Llamada: &copilot.LlamadaIA{ID: l.ID, Nombre: l.Funcion.Nombre, Argumentos: argumentos}})
	}
	if len(partes) == 0 {
		return r, false, copilot.ErrRespuestaIA
	}
	r.Turno.Partes = partes
	return r, false, nil
}

// objeto decodifica con UseNumber para no convertir enteros a float64 antes de
// comprobar el rango exacto de JSON y JavaScript.
func objeto(raw json.RawMessage) (map[string]any, error) {
	if !json.Valid(raw) {
		return nil, copilot.ErrSolicitudIA
	}
	var datos map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&datos); err != nil || datos == nil {
		return nil, copilot.ErrSolicitudIA
	}
	return datos, nil
}

// numerosSeguros rechaza enteros fuera de 2^53 y cualquier valor no finito:
// un importe redondeado llegaría al catálogo como otro número.
func numerosSeguros(valor any) bool {
	switch v := valor.(type) {
	case json.Number:
		if !strings.ContainsAny(v.String(), ".eE") {
			n, err := strconv.ParseInt(v.String(), 10, 64)
			return err == nil && n < 1<<53 && n > -(1<<53)
		}
		f, err := v.Float64()
		return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && math.Abs(f) < 1<<53
	case map[string]any:
		for _, item := range v {
			if !numerosSeguros(item) {
				return false
			}
		}
	case []any:
		for _, item := range v {
			if !numerosSeguros(item) {
				return false
			}
		}
	}
	return true
}
