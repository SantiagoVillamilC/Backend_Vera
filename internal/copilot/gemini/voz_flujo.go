package gemini

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// SintetizarFlujo usa una única solicitud SSE, sin reintentar fragmentos.
// Una desconexión o cancelación descarta el WAV final y evita cachear audio incompleto.
func (v *Voz) SintetizarFlujo(ctx context.Context, texto, voz string, recibir func([]byte) error) ([]byte, error) {
	if recibir == nil {
		return nil, copilot.ErrSolicitudIA
	}
	r, err := v.solicitarVoz(ctx, texto, voz, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Body.Close() }()
	return leerFlujoVoz(ctx, r.Body, recibir)
}

func leerFlujoVoz(ctx context.Context, lector io.Reader, recibir func([]byte) error) ([]byte, error) {
	scanner := bufio.NewScanner(io.LimitReader(lector, 8*1024*1024+1))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var datos strings.Builder
	pcm := make([]byte, 0)
	completo := false
	procesar := func() error {
		if datos.Len() == 0 {
			return nil
		}
		var e struct {
			Event string `json:"event_type"`
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Data []byte `json:"data"`
				MIME string `json:"mime_type"`
				Rate int    `json:"sample_rate"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(datos.String()), &e) != nil {
			return copilot.ErrRespuestaIA
		}
		datos.Reset()
		tipo := e.Event
		if tipo == "" {
			tipo = e.Type
		}
		switch tipo {
		case "interaction.completed":
			completo = true
		case "error", "interaction.failed":
			return copilot.ErrNoDisponibleIA
		case "step.delta":
			if e.Delta.Type != "audio" || len(e.Delta.Data) == 0 {
				return nil
			}
			if completo || (e.Delta.MIME != "" && e.Delta.MIME != "audio/l16") || (e.Delta.Rate != 0 && e.Delta.Rate != 24000) || len(pcm)+len(e.Delta.Data) > 4*1024*1024 {
				return copilot.ErrRespuestaIA
			}
			pcm = append(pcm, e.Delta.Data...)
			return recibir(e.Delta.Data)
		}
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		linea := scanner.Text()
		if linea == "" {
			if err := procesar(); err != nil {
				return nil, err
			}
			if completo {
				break
			}
			continue
		}
		if strings.HasPrefix(linea, "data:") {
			if datos.Len() > 0 {
				datos.WriteByte('\n')
			}
			datos.WriteString(strings.TrimSpace(strings.TrimPrefix(linea, "data:")))
		}
	}
	if scanner.Err() != nil {
		return nil, copilot.ErrRespuestaIA
	}
	if err := procesar(); err != nil {
		return nil, err
	}
	if !completo || len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil, copilot.ErrRespuestaIA
	}
	longitud := len(pcm)
	if longitud > 4*1024*1024 {
		return nil, copilot.ErrRespuestaIA
	}
	tamanoPCM := uint32(longitud)
	wav := make([]byte, 44+longitud)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], 36+tamanoPCM)
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 24000)
	binary.LittleEndian.PutUint32(wav[28:], 48000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], tamanoPCM)
	copy(wav[44:], pcm)
	return wav, nil
}

var _ copilot.SintetizadorVozFlujo = (*Voz)(nil)
