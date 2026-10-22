// Command revisar-conversacion-vera mide veinte turnos consecutivos con datos ficticios.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
)

var preguntas = []string{
	"Hola Vera, ¿cómo estás?",
	"¿Qué puedes hacer por mí?",
	"¿Cuánto tengo disponible?",
	"¿Y cuánto puedo enviar como máximo?",
	"¿Podría pasarle 30 mil pesos a Laura? Solo estoy preguntando, no prepares nada todavía.",
	"Eh, espérame, ¿puedes decir cuánto gasté el mes pasado, por favor?",
	"Okay, ¿y sabes en qué se me fue la plata?",
	"Eh, ¿cuánto dinero me gasté entre el 11 de octubre y el 15 de octubre de 2026, sabes?",
	"Bueno, busca a Laura y prepara 30 mil pesos para ella.",
	"¿Ya se envió o tengo que confirmar algo?",
	"Gracias. Ahora explícame qué es el GMF sin calcular nada.",
	"Listo, ¿me muestras los movimientos de ayer?",
	"No, mejor los del 11 al 15 de octubre de 2026, máximo cinco.",
	"¿Puedes saber si gasté eso en comida o transporte?",
	"Prepara cincuenta para Laura.",
	"Quise decir cincuenta mil pesos para Laura, prepara la propuesta.",
	"No quiero enviar todavía. ¿Puedo seguir conversando contigo?",
	"Estoy preocupado por mis gastos, ¿por dónde empiezo a organizarme?",
	"Cambiando de tema, ¿me cuentas un chiste corto?",
	"Gracias Vera. Recuérdame qué puedo revisar antes de confirmar un envío.",
}

var preguntasColoquiales = []string{
	"Buenas, Vera. Eh... ¿sí me escuchas?",
	"Quiero organizar lo de mi plata, pero no sé ni por dónde arrancar.",
	"Ajá, muéstrame cuánto tengo.",
	"¿Eso es lo que puedo gastar o hay algo retenido?",
	"¿Y cuánto podría mandarle a Laura? Todavía no quiero preparar nada.",
	"Pues cincuenta.",
	"No, digo cincuenta mil pesos. Solo quería saber si puedo, no enviarlos.",
	"Listo, luego miro eso. Cuéntame qué diferencia hay entre saldo y límite.",
	"No entendí, explícamelo más sencillo porfa.",
	"Bueno, revisa las salidas de este mes.",
	"¿Y las del mes pasado?",
	"¿Y eso fue todo en comida?",
	"Vale, no lo sabes todavía. No inventes categorías.",
	"Oye, ¿tú puedes confirmar un envío si yo te digo que sí?",
	"Bueno, gracias por aclarar. Mejor no preparemos nada.",
	"La verdad estoy sin trabajo y quiero cuidar lo poco que tengo. ¿Qué puedo revisar?",
	"¿Puedes darme una idea general para hacer un presupuesto sin inventar mis gastos?",
	"Sí, eso me sirve.",
	"Otra cosa, ¿sabes hacer una receta de pasta?",
	"Está bien, volvamos a mi billetera. ¿Cuál era mi saldo disponible?",
}

type muestra struct {
	Turno           int           `json:"turno"`
	Pregunta        string        `json:"pregunta"`
	Texto           string        `json:"texto,omitempty"`
	Error           string        `json:"error,omitempty"`
	Respaldo        bool          `json:"respuesta_backend"`
	AccionPendiente bool          `json:"accion_pendiente"`
	Funciones       []string      `json:"funciones"`
	Solicitudes     int           `json:"solicitudes"`
	LatenciaMS      int64         `json:"latencia_ms"`
	Recuperacion    string        `json:"recuperacion,omitempty"`
	Audio           *muestraAudio `json:"audio,omitempty"`
}

type muestraAudio struct {
	Transcripcion string `json:"transcripcion,omitempty"`
	ErrorEntrada  string `json:"error_entrada,omitempty"`
	ErrorSalida   string `json:"error_salida,omitempty"`
	BytesSalida   int    `json:"bytes_salida"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	confirmado := flag.Bool("nivel-gratuito-confirmado", false, "proyecto Free Tier ya confirmado; nunca cambia facturación")
	maximo := flag.Int("max-solicitudes", 80, "tope físico, no número de turnos")
	intervalo := flag.Duration("intervalo", 5*time.Second, "separación mínima entre solicitudes físicas")
	salida := flag.String("salida", "docs/evaluaciones/conversacion-20.json", "informe con preguntas ficticias y respuestas")
	probarVoz := flag.Bool("probar-voz", false, "prueba entrada/salida de audio en saludo, consulta de envío y rango de fechas")
	soloVoz := flag.Bool("solo-voz", false, "repite únicamente esos tres casos de voz, con historial compartido")
	coloquial := flag.Bool("coloquial", false, "veinte turnos con muletillas, aclaraciones y cambios de tema")
	flag.Parse()
	if !*confirmado || os.Getenv("CI") != "" || *maximo < 1 || *maximo > 300 {
		return errors.New("confirma Free Tier y un tope de 1..300; no ejecutar en CI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	modelo := os.Getenv("VERA_MODELO")
	p, err := gemini.Nuevo(ctx, gemini.Config{APIKey: os.Getenv("GEMINI_API_KEY"), Modelo: modelo, Timeout: 25 * time.Second, TemperaturaPredeterminada: true, SinReintentos: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return errors.New("revisa la configuración local del proveedor sin compartir la clave")
	}
	funciones := []string{}
	c, err := evaluacion.NuevaConsola(evaluacion.ConfigReal{Modelo: modelo, Repeticiones: 1, MaxSolicitudes: *maximo, Intervalo: *intervalo, NivelGratuitoConfirmado: true}, p, func(e evaluacion.EventoConsola) {
		if e.Tipo == "funcion" {
			funciones = append(funciones, e.Nombre)
		}
	}, time.Now, evaluacion.ConRecuperacion(), evaluacion.ConContinuidadConversacion())
	if err != nil {
		return err
	}
	muestras := []muestra{}
	seleccion := preguntas
	if *coloquial {
		seleccion = preguntasColoquiales
	}
	if *soloVoz {
		seleccion = []string{preguntas[0], preguntas[4], preguntas[7]}
		*probarVoz = true
	}
	var voz copilot.SintetizadorVoz
	if *probarVoz {
		voz, err = gemini.NuevaVoz(os.Getenv("GEMINI_API_KEY"))
		if err != nil {
			return err
		}
	}
	for i, pregunta := range seleccion {
		funciones = []string{}
		antes, _ := c.Consumo()
		inicio := time.Now()
		texto := pregunta
		var audio *muestraAudio
		if voz != nil && (*soloVoz || i == 0 || i == 4 || i == 7) {
			audio = &muestraAudio{}
			b, e := c.Sintetizar(ctx, voz, pregunta, "Aoede")
			if e == nil {
				audio.Transcripcion, e = c.Transcribir(ctx, &copilot.AudioIA{MIME: "audio/wav", Datos: b})
				clear(b)
			}
			if e != nil {
				audio.ErrorEntrada = codigo(e)
			} else {
				texto = audio.Transcripcion
			}
		}
		r, fallo := c.Preguntar(ctx, texto)
		if audio != nil && fallo == nil {
			b, e := c.Sintetizar(ctx, voz, r.Texto, "Aoede")
			audio.BytesSalida = len(b)
			clear(b)
			if e != nil {
				audio.ErrorSalida = codigo(e)
			}
		}
		despues, _ := c.Consumo()
		m := muestra{Turno: i + 1, Pregunta: pregunta, Texto: r.Texto, Respaldo: r.RespuestaDelBackend, Funciones: funciones, Solicitudes: despues - antes, LatenciaMS: time.Since(inicio).Milliseconds(), Recuperacion: r.MotivoRecuperacion, Audio: audio, AccionPendiente: r.AccionPendiente != nil}
		if fallo != nil {
			m.Error = codigo(fallo)
		}
		muestras = append(muestras, m)
		informe := struct {
			Modelo, Version string
			CreadoEn        time.Time
			Turnos          []muestra
			Solicitudes     int
			Completo        bool
			ObjetivoTurnos  int
			IntervaloMS     int64
			AudioSintetico  bool
		}{modelo, copilot.VersionInstruccionesVera, time.Now().UTC(), muestras, despues, len(muestras) == len(seleccion), len(seleccion), intervalo.Milliseconds(), *probarVoz}
		b, e := json.MarshalIndent(informe, "", "  ")
		if e != nil {
			return e
		}
		if e = os.WriteFile(*salida, append(b, '\n'), 0o600); e != nil {
			return e
		}
		fmt.Printf("Turno %02d: error=%s respaldo=%t llamadas=%d latencia=%dms\n", i+1, m.Error, m.Respaldo, m.Solicitudes, m.LatenciaMS)
		if errors.Is(fallo, copilot.ErrLimiteIA) || errors.Is(fallo, copilot.ErrCredencialesIA) || errors.Is(fallo, evaluacion.ErrCuotaEvaluacion) || ctx.Err() != nil {
			break
		}
	}
	return nil
}

func codigo(err error) string {
	var limite *copilot.ErrorLimiteIA
	if errors.As(err, &limite) {
		switch limite.Clase {
		case copilot.LimiteDia:
			return "ProviderDailyLimit"
		case copilot.LimiteMinuto:
			return "ProviderMinuteLimit"
		default:
			return "ProviderLimit"
		}
	}
	var bloqueo *copilot.ErrorBloqueo
	if errors.As(err, &bloqueo) {
		return string(bloqueo.Motivo)
	}
	for _, v := range []struct {
		e error
		s string
	}{{copilot.ErrLimiteIA, "ProviderLimit"}, {copilot.ErrTimeoutIA, "Timeout"}, {copilot.ErrRespuestaIA, "InvalidResponse"}, {copilot.ErrSolicitudIA, "InvalidRequest"}, {copilot.ErrCredencialesIA, "Credentials"}, {evaluacion.ErrCuotaEvaluacion, "SessionLimit"}, {context.DeadlineExceeded, "Deadline"}} {
		if errors.Is(err, v.e) {
			return v.s
		}
	}
	return "Unavailable"
}
