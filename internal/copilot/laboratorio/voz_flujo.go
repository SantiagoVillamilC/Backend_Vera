package laboratorio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
)

type eventoVoz struct {
	Tipo   string  `json:"tipo"`
	PCM    string  `json:"pcm,omitempty"`
	Audio  string  `json:"audio,omitempty"`
	Estado *estado `json:"estado,omitempty"`
	Fuente string  `json:"fuente,omitempty"`
	Motivo string  `json:"motivo_respaldo,omitempty"`
	Cache  bool    `json:"cache,omitempty"`
}

func (s *servidor) sintetizarFlujo(ctx context.Context, w http.ResponseWriter, voz string) {
	s.voces.Solicitudes++
	iniciado := false
	escribir := func(e eventoVoz) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !iniciado {
			w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
			w.WriteHeader(200)
			iniciado = true
		}
		if err := json.NewEncoder(w).Encode(api.Envelope[eventoVoz]{Success: true, Data: e}); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
	cache := s.vozCache == voz && len(s.audioCache) > 0
	if cache {
		if escribir(eventoVoz{Tipo: "wav", Audio: base64.StdEncoding.EncodeToString(s.audioCache)}) != nil {
			s.voces.Fallos++
			return
		}
	} else {
		audio, fuente, motivo, err := s.generarVoz(ctx, voz, func(pcm []byte) error {
			return escribir(eventoVoz{Tipo: "audio", PCM: base64.StdEncoding.EncodeToString(pcm)})
		})
		if err != nil {
			s.voces.Fallos++
			if !iniciado {
				s.error(w, err)
				return
			}
			// Una respuesta HTTP ya iniciada no puede cambiar de status. El último
			// envelope señala el fallo y el cliente descarta toda la caché parcial.
			_ = json.NewEncoder(w).Encode(api.Envelope[any]{Error: &api.APIError{Code: "VoiceInterrupted", Message: "La voz se interrumpió. El texto sigue disponible; no se reintentó."}})
			_ = http.NewResponseController(w).Flush()
			return
		}
		if !iniciado {
			if escribir(eventoVoz{Tipo: "wav", Audio: base64.StdEncoding.EncodeToString(audio)}) != nil {
				s.voces.Fallos++
				return
			}
		}
		clear(s.audioCache)
		s.audioCache = audio
		s.vozCache = voz
		s.fuenteCache, s.motivoCache = fuente, motivo
	}
	s.registrarVoz(cache)
	e := s.estado()
	_ = escribir(eventoVoz{Tipo: "fin", Estado: &e, Fuente: s.fuenteCache, Motivo: s.motivoCache, Cache: cache})
}
