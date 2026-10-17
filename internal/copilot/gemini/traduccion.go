package gemini

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"google.golang.org/genai"
)

func traducirSolicitud(s copilot.SolicitudIA, temperatura float32) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	cfg := &genai.GenerateContentConfig{
		Temperature: &temperatura, CandidateCount: 1, MaxOutputTokens: 2048,
		AutomaticContinuation: genai.Ptr(false),
	}
	if s.Instrucciones != "" {
		cfg.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: s.Instrucciones}}}
	}
	for _, categoria := range []genai.HarmCategory{genai.HarmCategoryHarassment, genai.HarmCategoryHateSpeech, genai.HarmCategorySexuallyExplicit, genai.HarmCategoryDangerousContent} {
		cfg.SafetySettings = append(cfg.SafetySettings, &genai.SafetySetting{Category: categoria, Threshold: genai.HarmBlockThresholdBlockMediumAndAbove})
	}
	declaraciones := make([]*genai.FunctionDeclaration, 0, len(s.Funciones))
	nombres := make(map[string]bool)
	for _, f := range s.Funciones {
		parametros, err := objeto(f.Parametros)
		if err != nil || strings.TrimSpace(f.Nombre) == "" || nombres[f.Nombre] || parametros["type"] != "object" {
			return nil, nil, copilot.ErrSolicitudIA
		}
		nombres[f.Nombre] = true
		declaraciones = append(declaraciones, &genai.FunctionDeclaration{Name: f.Nombre, Description: f.Descripcion, ParametersJsonSchema: parametros})
	}
	if len(declaraciones) > 0 {
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: declaraciones}}
		cfg.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: genai.FunctionCallingConfigModeAuto}}
	}
	contenidos := make([]*genai.Content, 0, len(s.Historial))
	for _, turno := range s.Historial {
		rol := "user"
		switch turno.Rol {
		case "asistente":
			rol = "model"
		case "usuario", "herramienta":
		default:
			return nil, nil, copilot.ErrSolicitudIA
		}
		if len(turno.Partes) == 0 {
			return nil, nil, copilot.ErrSolicitudIA
		}
		contenido := &genai.Content{Role: rol}
		for _, parte := range turno.Partes {
			p, err := traducirParte(parte, turno.Rol)
			if err != nil {
				return nil, nil, err
			}
			contenido.Parts = append(contenido.Parts, p)
		}
		contenidos = append(contenidos, contenido)
	}
	if len(contenidos) == 0 {
		return nil, nil, copilot.ErrSolicitudIA
	}
	return contenidos, cfg, nil
}

func traducirParte(p copilot.ParteIA, rol string) (*genai.Part, error) {
	tipos := 0
	for _, presente := range []bool{p.Texto != "", p.Audio != nil, p.Llamada != nil, p.Resultado != nil} {
		if presente {
			tipos++
		}
	}
	if tipos != 1 {
		return nil, copilot.ErrSolicitudIA
	}
	g := &genai.Part{Text: p.Texto, ThoughtSignature: bytes.Clone(p.Firma)}
	switch {
	case p.Audio != nil:
		if rol != "usuario" || !strings.HasPrefix(p.Audio.MIME, "audio/") || len(p.Audio.Datos) == 0 {
			return nil, copilot.ErrSolicitudIA
		}
		g.InlineData = &genai.Blob{MIMEType: p.Audio.MIME, Data: bytes.Clone(p.Audio.Datos)}
	case p.Llamada != nil:
		args, err := objeto(p.Llamada.Argumentos)
		if err != nil || rol != "asistente" || p.Llamada.Nombre == "" {
			return nil, copilot.ErrSolicitudIA
		}
		g.FunctionCall = &genai.FunctionCall{ID: p.Llamada.ID, Name: p.Llamada.Nombre, Args: args}
	case p.Resultado != nil:
		datos, err := objeto(p.Resultado.Datos)
		if err != nil || rol != "herramienta" || p.Resultado.Nombre == "" {
			return nil, copilot.ErrSolicitudIA
		}
		g.FunctionResponse = &genai.FunctionResponse{ID: p.Resultado.ID, Name: p.Resultado.Nombre, Response: datos}
	default:
		if rol == "herramienta" {
			return nil, copilot.ErrSolicitudIA
		}
	}
	return g, nil
}

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

func traducirRespuesta(raw *genai.GenerateContentResponse) (copilot.RespuestaIA, error) {
	r := copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente"}}
	if raw == nil {
		return r, copilot.ErrRespuestaIA
	}
	if u := raw.UsageMetadata; u != nil {
		r.TokensEntrada, r.TokensSalida = u.PromptTokenCount, u.CandidatesTokenCount+u.ThoughtsTokenCount
	}
	if raw.PromptFeedback != nil && raw.PromptFeedback.BlockReason != "" && raw.PromptFeedback.BlockReason != "BLOCK_REASON_UNSPECIFIED" {
		return r, copilot.ErrBloqueoIA
	}
	if len(raw.Candidates) != 1 || raw.Candidates[0] == nil {
		return r, copilot.ErrRespuestaIA
	}
	c := raw.Candidates[0]
	switch c.FinishReason {
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "RECITATION":
		return r, copilot.ErrBloqueoIA
	case "STOP", "":
	default:
		return r, copilot.ErrRespuestaIA
	}
	for _, rating := range c.SafetyRatings {
		if rating != nil && rating.Blocked {
			return r, copilot.ErrBloqueoIA
		}
	}
	if c.Content == nil {
		return r, copilot.ErrRespuestaIA
	}
	partes := make([]copilot.ParteIA, 0, len(c.Content.Parts))
	for _, p := range c.Content.Parts {
		if p == nil || p.Thought {
			continue
		}
		parte := copilot.ParteIA{Firma: bytes.Clone(p.ThoughtSignature)}
		switch {
		case p.FunctionCall != nil:
			f := p.FunctionCall
			if f.Name == "" || f.PartialArgs != nil || f.WillContinue != nil {
				return r, copilot.ErrRespuestaIA
			}
			args := f.Args
			if args == nil {
				args = map[string]any{}
			}
			if !numerosSeguros(args) {
				return r, copilot.ErrRespuestaIA
			}
			b, err := json.Marshal(args)
			if err != nil {
				return r, copilot.ErrRespuestaIA
			}
			parte.Llamada = &copilot.LlamadaIA{ID: f.ID, Nombre: f.Name, Argumentos: b}
		case p.Text != "":
			parte.Texto = p.Text
		default:
			return r, copilot.ErrRespuestaIA
		}
		partes = append(partes, parte)
	}
	if len(partes) == 0 {
		return r, copilot.ErrRespuestaIA
	}
	r.Turno.Partes = partes
	return r, nil
}

// El SDK decodifica Args en float64. Rechazar enteros fuera del rango exacto
// evita entregar un número redondeado al catálogo. Los montos usan strings.
func numerosSeguros(valor any) bool {
	switch v := valor.(type) {
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) < 1<<53
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
