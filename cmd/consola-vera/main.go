// Command consola-vera permite inspeccionar una conversación de prueba en la terminal.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/provsim"
)

type fabricaProveedor func(context.Context, string) (copilot.ProveedorIA, error)

func main() {
	os.Exit(ejecutar(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, func(ctx context.Context, modelo string) (copilot.ProveedorIA, error) {
		return gemini.Nuevo(ctx, gemini.Config{APIKey: os.Getenv("GEMINI_API_KEY"), Modelo: modelo, Timeout: 20 * time.Second, SinReintentos: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}))
}

func ejecutar(ctx context.Context, args []string, entrada io.Reader, salida, diagnostico io.Writer, crear fabricaProveedor) int {
	opciones := flag.NewFlagSet("consola-vera", flag.ContinueOnError)
	opciones.SetOutput(diagnostico)
	demo := opciones.Bool("demo", false, "muestra un ejemplo simulado sin clave ni solicitudes a Gemini")
	confirmado := opciones.Bool("nivel-gratuito-confirmado", false, "confirma el proyecto y modelo Free Tier")
	maximo := opciones.Int("max-solicitudes", 20, "máximo total de llamadas a Gemini durante esta sesión")
	intervalo := opciones.Duration("intervalo", 12*time.Second, "pausa mínima entre llamadas a Gemini")
	pregunta := opciones.String("pregunta", "", "ejecuta una pregunta y termina; sin esta opción abre la consola interactiva")
	if err := opciones.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if opciones.NArg() != 0 {
		_, _ = fmt.Fprintln(diagnostico, "No se aceptan argumentos posicionales.")
		return 2
	}
	if strings.HasPrefix(strings.TrimSpace(*pregunta), "/") {
		_, _ = fmt.Fprintln(diagnostico, "Usa -pregunta con una pregunta; los comandos / se usan en modo interactivo.")
		return 2
	}
	pantalla := &terminal{salida: salida}
	modelo := os.Getenv("VERA_MODELO")
	var proveedor copilot.ProveedorIA
	var err error
	if *demo {
		modelo = "demo simulada"
		*maximo = 2
		*intervalo = 0
		*confirmado = true
		*pregunta = "¿Cuánto tengo?"
		proveedor = provsim.Nuevo(
			provsim.Paso{Respuesta: copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{ID: "demo-saldo", Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}}}}}},
			provsim.Paso{Respuesta: copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "Tu saldo disponible es 120000.00 COP. Tienes 5000.00 COP retenidos."}}}}},
		)
	} else {
		if !*confirmado || modelo == "" || os.Getenv("CI") != "" || *maximo < 1 || *maximo > 300 || *intervalo < 0 || *intervalo > time.Minute {
			_, _ = fmt.Fprintln(diagnostico, "Configura VERA_MODELO, confirma Free Tier y usa un límite de 1 a 300 solicitudes; no se permite CI.")
			return 2
		}
		proveedor, err = crear(ctx, modelo)
		if err != nil {
			_, _ = fmt.Fprintln(diagnostico, "No se pudo configurar Gemini. Revisa GEMINI_API_KEY y VERA_MODELO sin compartir la clave.")
			return 2
		}
	}
	pantalla.printf("\n============================================================\n VERA | Consola de pruebas\n============================================================\n")
	pantalla.printf("Modelo: %s\nDatos: FICTICIOS. No consulta tu cuenta ni ejecuta dinero.\n", visible(modelo))
	pantalla.printf("Instrucciones: %s\n", copilot.VersionInstruccionesVera)
	if *demo {
		pantalla.printf("DEMO SIMULADA: no llama a Gemini.\n")
	} else {
		pantalla.printf("Gemini real | máximo %d solicitudes | sin reintentos\n", *maximo)
	}
	pantalla.printf("\n/ayuda: ejemplos  /nuevo: borrar contexto  /cuota  /salir\n")
	if pantalla.err != nil {
		return 2
	}
	sesion, err := evaluacion.NuevaConsola(evaluacion.ConfigReal{Modelo: modelo, Repeticiones: 1, MaxSolicitudes: *maximo, Intervalo: *intervalo, NivelGratuitoConfirmado: *confirmado}, proveedor, pantalla.evento, time.Now)
	if err != nil {
		pantalla.printf("Configuración de consola inválida.\n")
		return 2
	}
	scanner := bufio.NewScanner(entrada)
	scanner.Buffer(make([]byte, 1024), 8193)
	for {
		texto := *pregunta
		if texto == "" {
			pantalla.printf("\nTú > ")
			if pantalla.err != nil {
				return 2
			}
			if !scanner.Scan() {
				if scanner.Err() != nil {
					pantalla.printf("\nNo pude leer la pregunta (máximo 8192 bytes).\n")
					return 2
				}
				return 0
			}
			texto = strings.TrimSpace(scanner.Text())
			if texto == "" {
				continue
			}
		} else {
			pantalla.printf("\nTú > %s\n", visible(texto))
		}
		switch texto {
		case "/salir":
			return 0
		case "/ayuda":
			pantalla.printf("\nPrueba: ¿Cuánto tengo?\n  Lista mis movimientos de ayer\n  Resume este mes por motivo\n  Busca a Laura y prepara 50 mil\n  Prepara 50 mil a la llave 1234567890\n  Envía cincuenta a Laura\nLas respuestas usan datos ficticios. /nuevo borra el contexto.\n")
			continue
		case "/nuevo":
			sesion.NuevaConversacion()
			pantalla.printf("\nConversación nueva. La cuota consumida se conserva.\n")
			continue
		case "/cuota":
			usados, limite := sesion.Consumo()
			pantalla.printf("\nSolicitudes: %d/%d (una pregunta puede necesitar varias).\n", usados, limite)
			continue
		}
		if strings.HasPrefix(texto, "/") {
			pantalla.printf("\nComando desconocido. Usa /ayuda.\n")
			continue
		}
		if *demo {
			pantalla.printf("\n[ DEMO ] Ejecutando el ejemplo local...\n")
		} else {
			pantalla.printf("\n[ MODELO ] Procesando; puede esperar la pausa entre solicitudes...\n")
		}
		inicio := time.Now()
		respuesta, err := sesion.Preguntar(ctx, texto)
		if err != nil {
			pantalla.printf("\n[ ERROR ] %s\n", errorVisible(err))
			if *pregunta != "" || errors.Is(err, evaluacion.ErrCuotaEvaluacion) || errors.Is(err, copilot.ErrLimiteIA) || errors.Is(err, copilot.ErrCredencialesIA) || ctx.Err() != nil {
				return 1
			}
		} else {
			pantalla.printf("\n[ VERA | respuesta final ]\n%s\n", visible(respuesta.Texto))
			if respuesta.AccionPendiente != nil {
				pantalla.printf("\n[ TARJETA SIMULADA | no ejecuta dinero ]\nMonto: %s COP | Destino: %s\nRequiere confirmación en la app.\n", respuesta.AccionPendiente.Monto.PresentarUnidadesMayores(), visible(respuesta.AccionPendiente.NombreMostrado))
			}
			usados, limite := sesion.Consumo()
			pantalla.printf("\n[ RESUMEN ] %d función(es) | %d vuelta(s) | %.1f s\nSolicitudes de la sesión: %d/%d\n", respuesta.Llamadas, respuesta.Vueltas, time.Since(inicio).Seconds(), usados, limite)
		}
		if pantalla.err != nil {
			return 2
		}
		if *pregunta != "" {
			return 0
		}
	}
}

type terminal struct {
	salida io.Writer
	err    error
}

func (t *terminal) printf(formato string, args ...any) {
	if t.err == nil {
		_, t.err = fmt.Fprintf(t.salida, formato, args...)
	}
}

func (t *terminal) evento(e evaluacion.EventoConsola) {
	titulo := "FUNCIÓN ELEGIDA | argumentos"
	if e.Tipo == "resultado" {
		titulo = "BACKEND DE PRUEBA | resultado"
	}
	var datos bytes.Buffer
	if json.Indent(&datos, e.Datos, "", "  ") != nil {
		t.printf("\n[ %s ] %q\nJSON no válido\n", titulo, e.Nombre)
		return
	}
	t.printf("\n[ %s ] %q\n%s\n", titulo, e.Nombre, datos.String())
}

func visible(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}

func errorVisible(err error) string {
	for _, e := range []struct {
		err   error
		texto string
	}{
		{evaluacion.ErrCuotaEvaluacion, "Se alcanzó el límite de solicitudes de esta sesión."},
		{copilot.ErrLimiteIA, "Google agotó o limitó la cuota. La sesión se detiene sin reintentos."},
		{copilot.ErrCredencialesIA, "Credenciales rechazadas. Revisa la clave local; no la pegues en el chat."},
		{copilot.ErrTimeoutIA, "Se agotó el tiempo de espera del proveedor."},
		{copilot.ErrBloqueoIA, "El proveedor o el orquestador bloquearon la respuesta."},
		{copilot.ErrSolicitudIA, "Pregunta o solicitud inválida."},
		{copilot.ErrRespuestaIA, "El proveedor devolvió una respuesta inválida o incompleta."},
	} {
		if errors.Is(err, e.err) {
			return e.texto
		}
	}
	return "No se pudo completar la consulta. No se muestran cuerpos de error ni secretos del proveedor."
}
