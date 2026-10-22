// Command revisar-voz-vera verifica el flujo HTTP real del laboratorio con datos ficticios.
package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/laboratorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/vozlocal"
)

var preguntas = []string{
	"Hola Vera, ¿cómo estás?", "¿Qué puedes hacer por mí?", "¿Cuánto tengo disponible?",
	"¿Y cuánto puedo enviar?", "¿Puedo pasarle treinta mil pesos a Laura? No prepares nada.",
	"¿Cuánto gasté el mes pasado?", "¿Sabes en qué se fue la plata?",
	"¿Cuánto gasté entre el 11 y el 15 de octubre de 2026?", "Busca a Laura y prepara treinta mil pesos.",
	"¿Ya se envió el dinero?", "Explícame brevemente qué es el GMF sin calcular nada.",
	"Muéstrame los movimientos de ayer.", "Mejor del 11 al 15 de octubre de 2026, máximo cinco.",
	"¿Puedes saber si gasté eso en comida?", "Pues cincuenta.", "Quise decir cincuenta mil pesos, pero no prepares nada.",
	"¿Puedo seguir conversando?", "Dame una idea general para organizar mis gastos, sin inventar mis datos.",
	"Cuéntame un chiste corto.", "Gracias Vera. ¿Qué reviso antes de confirmar un envío?",
}

type resultado struct {
	Turno        int    `json:"turno"`
	Pregunta     string `json:"pregunta"`
	Ruta         string `json:"ruta_voz"`
	Texto        string `json:"texto"`
	Fuente       string `json:"fuente,omitempty"`
	Motivo       string `json:"motivo_respaldo,omitempty"`
	Error        string `json:"error,omitempty"`
	AudioBytes   int    `json:"audio_bytes"`
	LatenciaMS   int64  `json:"latencia_voz_ms"`
	PrimeraVozMS int64  `json:"primera_voz_ms"`
	Cabecera     string `json:"cabecera_invalida_hex,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	confirmado := flag.Bool("nivel-gratuito-confirmado", false, "proyecto Free Tier confirmado; no cambia facturación")
	soloLocal := flag.Bool("solo-local", false, "chat simulado y Dora real; sin llamadas a Google")
	salida := flag.String("salida", "docs/evaluaciones/voz-natural.json", "informe de generación completa; no acredita audición ni micrófono")
	flag.Parse()
	if os.Getenv("CI") != "" || (!*soloLocal && !*confirmado) {
		return errors.New("confirma Free Tier; no ejecutar pruebas reales en CI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	modelo := os.Getenv("VERA_MODELO")
	var proveedor copilot.ProveedorIA = laboratorio.ProveedorDemo{}
	var voz copilot.SintetizadorVoz
	var err error
	if !*soloLocal {
		proveedor, err = gemini.Nuevo(ctx, gemini.Config{APIKey: os.Getenv("GEMINI_API_KEY"), Modelo: modelo, Timeout: 25 * time.Second, TemperaturaPredeterminada: true, SinReintentos: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			return errors.New("no se pudo configurar Gemini")
		}
		voz, err = gemini.NuevaVoz(os.Getenv("GEMINI_API_KEY"))
		if err != nil {
			return err
		}
	} else {
		modelo = "chat simulado; voz Kokoro real"
	}
	token := os.Getenv("VERA_LAB_TOKEN")
	local, err := vozlocal.Nueva(os.Getenv("VERA_VOZ_LOCAL_URL"), token)
	if err != nil {
		return errors.New("inicia con el contenedor de voz local")
	}
	h, err := laboratorio.Nuevo(evaluacion.ConfigReal{Modelo: modelo, Repeticiones: 1, MaxSolicitudes: 100, NivelGratuitoConfirmado: true, Intervalo: 2 * time.Second}, proveedor, token, "localhost:8787", *soloLocal, voz, laboratorio.ConVozLocal(local))
	if err != nil {
		return err
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	cliente := &http.Client{Timeout: 90 * time.Second}
	var primeraVozMS int64
	peticion := func(ruta, texto string) (json.RawMessage, error) {
		inicioAudio := time.Now()
		metodo := http.MethodPost
		if ruta == "/api/estado" {
			metodo = http.MethodGet
		}
		// #nosec G704 -- srv.URL proviene de httptest.NewServer (loopback); ruta sólo recibe constantes de este comando.
		r, err := http.NewRequestWithContext(ctx, metodo, srv.URL+ruta, strings.NewReader(texto))
		if err != nil {
			return nil, err
		}
		r.Host = "localhost:8787"
		r.Header.Set("X-Vera-Token", token)
		r.Header.Set("Content-Type", "text/plain")
		// #nosec G704 -- Sólo se llama al servidor HTTP de prueba creado arriba; no acepta destinos externos.
		resp, err := cliente.Do(r)
		if err != nil {
			return nil, errors.New("fallo de transporte HTTP local")
		}
		defer func() { _ = resp.Body.Close() }()
		var sobre struct {
			Success bool            `json:"success"`
			Data    json.RawMessage `json:"data"`
			Error   struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		decodificador := json.NewDecoder(io.LimitReader(resp.Body, 8*1024*1024))
		if decodificador.Decode(&sobre) != nil {
			return nil, errors.New("envelope inválido")
		}
		if !sobre.Success {
			return nil, errors.New(sobre.Error.Code)
		}
		if ruta == "/api/voz/flujo" {
			var pcm, wav []byte
			for {
				var evento struct {
					Tipo   string `json:"tipo"`
					PCM    string `json:"pcm"`
					Audio  string `json:"audio"`
					Fuente string `json:"fuente"`
					Motivo string `json:"motivo_respaldo"`
				}
				if json.Unmarshal(sobre.Data, &evento) != nil {
					return nil, errors.New("evento inválido")
				}
				switch evento.Tipo {
				case "wav":
					if primeraVozMS == 0 {
						primeraVozMS = time.Since(inicioAudio).Milliseconds()
					}
					wav, err = base64.StdEncoding.DecodeString(evento.Audio)
				case "audio":
					if primeraVozMS == 0 {
						primeraVozMS = time.Since(inicioAudio).Milliseconds()
					}
					var bloque []byte
					bloque, err = base64.StdEncoding.DecodeString(evento.PCM)
					pcm = append(pcm, bloque...)
				case "fin":
					longitud := len(pcm)
					if longitud > 0 && longitud%2 == 0 && longitud <= 4*1024*1024 {
						tamanoPCM := uint32(longitud)
						wav = make([]byte, 44+longitud)
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
					}
					return json.Marshal(struct {
						Audio  string `json:"audio"`
						Fuente string `json:"fuente"`
						Motivo string `json:"motivo_respaldo"`
					}{base64.StdEncoding.EncodeToString(wav), evento.Fuente, evento.Motivo})
				}
				if err != nil {
					return nil, errors.New("audio inválido")
				}
				if decodificador.Decode(&sobre) != nil {
					return nil, errors.New("stream incompleto")
				}
				if !sobre.Success {
					return nil, errors.New(sobre.Error.Code)
				}
			}
		}
		return sobre.Data, nil
	}
	resultados := make([]resultado, 0, len(preguntas))
	for i, pregunta := range preguntas {
		m := resultado{Turno: i + 1, Pregunta: pregunta, Ruta: "/api/voz"}
		if i%2 == 1 {
			m.Ruta += "/flujo"
		}
		b, err := peticion("/api/preguntar", pregunta)
		if err != nil {
			m.Error = err.Error()
			resultados = append(resultados, m)
			break
		}
		var turno struct {
			Texto string `json:"texto"`
		}
		_ = json.Unmarshal(b, &turno)
		m.Texto = turno.Texto
		inicio := time.Now()
		primeraVozMS = 0
		nombre := "Aoede"
		if *soloLocal {
			nombre = "Dora"
		}
		b, err = peticion(m.Ruta, nombre)
		m.LatenciaMS = time.Since(inicio).Milliseconds()
		m.PrimeraVozMS = primeraVozMS
		if m.PrimeraVozMS == 0 {
			m.PrimeraVozMS = m.LatenciaMS
		}
		if err != nil {
			m.Error = err.Error()
		} else {
			var audio struct {
				Audio  string `json:"audio"`
				Fuente string `json:"fuente"`
				Motivo string `json:"motivo_respaldo"`
			}
			_ = json.Unmarshal(b, &audio)
			wav, fallo := base64.StdEncoding.DecodeString(audio.Audio)
			m.Fuente, m.Motivo = audio.Fuente, audio.Motivo
			m.AudioBytes = len(wav)
			if fallo != nil || !vozlocal.WAVValido(wav) {
				m.Error = "InvalidWAV"
				m.Cabecera = hex.EncodeToString(wav[:min(128, len(wav))])
			} else {
				m.AudioBytes = len(wav)
			}
		}
		resultados = append(resultados, m)
		fmt.Printf("Turno %02d: fuente=%s bytes=%d voz=%d ms error=%s\n", m.Turno, m.Fuente, m.AudioBytes, m.LatenciaMS, m.Error)
		if m.Cabecera != "" {
			fmt.Println("Cabecera de diagnóstico:", m.Cabecera)
		}
	}
	estado, _ := peticion("/api/estado", "")
	b, err := json.MarshalIndent(struct {
		Fecha      string          `json:"fecha_utc"`
		SoloLocal  bool            `json:"chat_simulado"`
		Resultados []resultado     `json:"resultados"`
		Estado     json.RawMessage `json:"estado_final"`
	}{time.Now().UTC().Format(time.RFC3339), *soloLocal, resultados, estado}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(*salida, append(b, '\n'), 0600); err != nil {
		return err
	}
	for _, m := range resultados {
		if m.Error != "" {
			return errors.New("hay fallos; revisa el informe")
		}
	}
	return nil
}
