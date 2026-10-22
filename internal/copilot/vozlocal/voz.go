// Package vozlocal adapta el sintetizador neural del contenedor del laboratorio.
// No cambia el proveedor del chat ni transmite texto a servicios externos.
package vozlocal

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

// Voz produce WAV con la voz española ef_dora de Kokoro-82M.
type Voz struct {
	endpoint, token string
	cliente         *http.Client
}

// Nueva sólo permite loopback; nunca admite una URL arbitraria del navegador.
func Nueva(endpoint, token string) (*Voz, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(token) < 32 {
		return nil, copilot.ErrSolicitudIA
	}
	return &Voz{endpoint: endpoint, token: token, cliente: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Sintetizar ignora la identidad de Gemini: el laboratorio informa la fuente Dora.
func (v *Voz) Sintetizar(ctx context.Context, texto, _ string) ([]byte, error) {
	respuesta, err := v.solicitar(ctx, texto, "/sintetizar")
	if err != nil {
		return nil, err
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.Header.Get("Content-Type") != "audio/wav" {
		return nil, copilot.ErrRespuestaIA
	}
	audio, err := io.ReadAll(io.LimitReader(respuesta.Body, 4*1024*1024+45))
	if err != nil || !WAVValido(audio) {
		return nil, copilot.ErrRespuestaIA
	}
	return audio, nil
}

func (v *Voz) solicitar(ctx context.Context, texto, ruta string) (*http.Response, error) {
	if !utf8.ValidString(texto) || strings.TrimSpace(texto) == "" || len(texto) > 8192 {
		return nil, copilot.ErrSolicitudIA
	}
	b, err := json.Marshal(struct {
		Texto string `json:"texto"`
	}{texto})
	if err != nil {
		return nil, copilot.ErrSolicitudIA
	}
	for intento := 0; ; intento++ {
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint+ruta, bytes.NewReader(b))
		if err != nil {
			return nil, copilot.ErrSolicitudIA
		}
		r.Header.Set("X-Vera-Token", v.token)
		r.Header.Set("Content-Type", "application/json")
		respuesta, err := v.cliente.Do(r)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, copilot.ErrNoDisponibleIA
		}
		// Cancelar una lectura puede dejar la frase actual terminando en CPU. Una
		// espera breve y acotada evita pasar a la voz robótica por ese solapamiento.
		if respuesta.StatusCode == http.StatusConflict && intento < 4 {
			_ = respuesta.Body.Close()
			reloj := time.NewTimer(500 * time.Millisecond)
			select {
			case <-ctx.Done():
				reloj.Stop()
				return nil, ctx.Err()
			case <-reloj.C:
			}
			continue
		}
		if respuesta.StatusCode != http.StatusOK {
			_ = respuesta.Body.Close()
			return nil, copilot.ErrNoDisponibleIA
		}
		return respuesta, nil
	}
}

// SintetizarFlujo entrega frases PCM en orden y sólo conserva audio completo.
func (v *Voz) SintetizarFlujo(ctx context.Context, texto, _ string, recibir func([]byte) error) ([]byte, error) {
	if recibir == nil {
		return nil, copilot.ErrSolicitudIA
	}
	r, err := v.solicitar(ctx, texto, "/flujo")
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Body.Close() }()
	if r.Header.Get("Content-Type") != "application/x-ndjson" {
		return nil, copilot.ErrRespuestaIA
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 8*1024*1024))
	pcm := make([]byte, 0)
	for {
		var evento struct {
			Tipo string `json:"tipo"`
			PCM  []byte `json:"pcm"`
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if d.Decode(&evento) != nil {
			return nil, copilot.ErrRespuestaIA
		}
		if evento.Tipo == "fin" {
			longitud := len(pcm)
			if longitud == 0 || longitud > 4*1024*1024 {
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
		if evento.Tipo != "audio" || len(evento.PCM) == 0 || len(evento.PCM)%2 != 0 || len(pcm)+len(evento.PCM) > 4*1024*1024 {
			return nil, copilot.ErrRespuestaIA
		}
		pcm = append(pcm, evento.PCM...)
		if err := recibir(evento.PCM); err != nil {
			return nil, err
		}
	}
}

// WAVValido acepta PCM16 mono de 24 kHz completo, incluidos bloques RIFF de
// metadatos. El inicio de data no tiene por qué estar en el byte 44.
func WAVValido(b []byte) bool {
	if len(b) <= 44 || len(b) > 4*1024*1024+4096 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || int64(binary.LittleEndian.Uint32(b[4:])) != int64(len(b))-8 {
		return false
	}
	formato, datos := false, false
	for pos := 12; pos < len(b); {
		if len(b)-pos < 8 {
			return false
		}
		tamano := int(binary.LittleEndian.Uint32(b[pos+4:]))
		inicio := pos + 8
		if tamano > len(b)-inicio {
			return false
		}
		switch string(b[pos : pos+4]) {
		case "fmt ":
			if formato || tamano < 16 {
				return false
			}
			f := b[inicio : inicio+tamano]
			if binary.LittleEndian.Uint16(f) != 1 || binary.LittleEndian.Uint16(f[2:]) != 1 || binary.LittleEndian.Uint32(f[4:]) != 24000 || binary.LittleEndian.Uint32(f[8:]) != 48000 || binary.LittleEndian.Uint16(f[12:]) != 2 || binary.LittleEndian.Uint16(f[14:]) != 16 {
				return false
			}
			formato = true
		case "data":
			if datos || tamano == 0 || tamano%2 != 0 || tamano > 4*1024*1024 {
				return false
			}
			datos = true
		}
		pos = inicio + tamano + tamano%2
		if pos > len(b) {
			return false
		}
	}
	return formato && datos
}
