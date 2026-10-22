// Command comparar-voces-vera genera muestras ficticias para elegir voz por audición.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/audiopcm"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/cartesia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
)

const muestra = "Hola, soy Vera. Qué bueno hablar contigo. Tu saldo disponible es 120000.00 COP. Puedo preparar un envío de 30000.00 COP para Laura. Tú revisas los datos y confirmas en la aplicación. ¿Qué te gustaría hacer?"

type resultado struct {
	Proveedor      string `json:"proveedor"`
	Voz            string `json:"voz"`
	Locale         string `json:"locale"`
	Modelo         string `json:"modelo"`
	Archivo        string `json:"archivo,omitempty"`
	Bytes          int    `json:"audio_bytes"`
	LatenciaMS     int64  `json:"generacion_completa_ms"`
	Error          string `json:"error,omitempty"`
	EsperaSegundos int    `json:"retry_after_segundos,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	confirmado := flag.Bool("nivel-gratuito-confirmado", false, "evaluación con claves locales y datos ficticios, sin modificar facturación")
	incluirGemini := flag.Bool("incluir-gemini", true, "una sola síntesis de Aoede con exactamente el mismo texto")
	salida := flag.String("salida", "docs/evaluaciones/comparacion-cartesia", "directorio del informe y los WAV ficticios")
	maximo := flag.Int("max-caracteres-cartesia", 1500, "tope previo para la suma del texto de las tres muestras, no créditos restantes")
	flag.Parse()
	if !*confirmado || os.Getenv("CI") != "" || flag.NArg() != 0 || *maximo < 1 || *maximo > 3000 {
		return errors.New("confirma Free Tier y un límite de 1..3000 caracteres; no ejecutar en CI")
	}
	caracteres := utf8.RuneCountInString(muestra) * len(cartesia.Candidatas())
	if caracteres > *maximo {
		return errors.New("la comparación supera el presupuesto local; no se hizo ninguna solicitud")
	}
	voz, err := cartesia.NuevaVoz(os.Getenv("CARTESIA_API_KEY"))
	if err != nil {
		return errors.New("falta CARTESIA_API_KEY en el entorno local; no compartas la clave")
	}
	if err := os.MkdirAll(*salida, 0750); err != nil {
		return errors.New("no se pudo crear el directorio de comparación")
	}
	directorio, err := os.OpenRoot(*salida)
	if err != nil {
		return errors.New("no se pudo abrir el directorio de comparación")
	}
	defer func() { _ = directorio.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resultados := make([]resultado, 0, 4)
	guardar := func(p copilot.SintetizadorVoz, proveedor, nombre, locale, modelo string) resultado {
		m := resultado{Proveedor: proveedor, Voz: nombre, Locale: locale, Modelo: modelo}
		inicio := time.Now()
		audio, err := p.Sintetizar(ctx, muestra, nombre)
		m.LatenciaMS = time.Since(inicio).Milliseconds()
		if err != nil {
			m.Error = codigo(err)
			var cuota *copilot.ErrorLimiteIA
			if errors.As(err, &cuota) {
				m.EsperaSegundos = int(cuota.ReintentarEn.Seconds())
			}
			return m
		}
		if !audiopcm.Valido(audio) {
			m.Error = "InvalidWAV"
			return m
		}
		nombreArchivo := proveedor + "-" + nombre + ".wav"
		if directorio.WriteFile(nombreArchivo, audio, 0600) != nil {
			m.Error = "FileWriteFailed"
			return m
		}
		m.Archivo = nombreArchivo
		m.Bytes = len(audio)
		return m
	}
	for _, c := range cartesia.Candidatas() {
		m := guardar(voz, "cartesia", c.Nombre, c.Locale, cartesia.ModeloVoz)
		resultados = append(resultados, m)
		fmt.Printf("%s: %d bytes, %d ms, error=%s\n", m.Voz, m.Bytes, m.LatenciaMS, m.Error)
		if m.Error != "" {
			break
		} // No gastar más crédito tras un rechazo del proveedor.
	}
	if *incluirGemini && ctx.Err() == nil {
		if g, err := gemini.NuevaVoz(os.Getenv("GEMINI_API_KEY")); err == nil {
			m := guardar(g, "gemini", "Aoede", "es-CO (estilo solicitado)", gemini.ModeloVoz)
			resultados = append(resultados, m)
			fmt.Printf("Aoede: %d bytes, %d ms, error=%s\n", m.Bytes, m.LatenciaMS, m.Error)
		}
	}
	solicitudes := 0
	for _, r := range resultados {
		if r.Proveedor == "cartesia" {
			solicitudes++
		}
	}
	informe := struct {
		Fecha      string      `json:"fecha_utc"`
		Texto      string      `json:"texto_identico"`
		Version    string      `json:"version_api_cartesia"`
		Caracteres int         `json:"caracteres_cartesia_enviados"`
		Nota       string      `json:"alcance"`
		Resultados []resultado `json:"muestras"`
	}{time.Now().UTC().Format(time.RFC3339), muestra, cartesia.VersionAPI, utf8.RuneCountInString(muestra) * solicitudes, "Comparación para audición humana. Latencia hasta WAV completo; no mide primer fragmento ni calidad subjetiva. No cambia la voz del laboratorio ni su respaldo.", resultados}
	b, err := json.MarshalIndent(informe, "", "  ")
	if err != nil {
		return err
	}
	if err := directorio.WriteFile("informe.json", append(b, '\n'), 0600); err != nil {
		return errors.New("no se pudo guardar el informe")
	}
	for _, r := range resultados {
		if r.Proveedor == "cartesia" && r.Error != "" {
			return errors.New("la comparación de Cartesia quedó incompleta; revisa el informe sin compartir credenciales")
		}
	}
	return nil
}

func codigo(err error) string {
	for _, e := range []struct {
		err    error
		codigo string
	}{
		{copilot.ErrLimiteIA, "ProviderLimit"}, {copilot.ErrCredencialesIA, "InvalidCredentials"}, {copilot.ErrSolicitudIA, "InvalidRequest"}, {copilot.ErrRespuestaIA, "InvalidResponse"}, {copilot.ErrBloqueoIA, "ResponseBlocked"}, {copilot.ErrTimeoutIA, "Timeout"}, {context.DeadlineExceeded, "Timeout"}, {context.Canceled, "Canceled"},
	} {
		if errors.Is(err, e.err) {
			return e.codigo
		}
	}
	return "Unavailable"
}
