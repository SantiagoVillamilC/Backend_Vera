// Command evaluar-vera ejecuta casos simulados o una evaluación explícita con Gemini o con un
// servidor Ollama autoalojado.
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
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/ollama"
)

const (
	proveedorSimulado = "simulado"
	proveedorGemini   = "gemini"
	proveedorLocal    = "local"
	urlOllamaInicial  = "http://127.0.0.1:11434"
)

func main() { os.Exit(ejecutar(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func ejecutar(ctx context.Context, args []string, salida, diagnostico io.Writer) int {
	opciones := flag.NewFlagSet("evaluar-vera", flag.ContinueOnError)
	opciones.SetOutput(diagnostico)
	ruta := opciones.String("casos", "", "JSON alternativo; por defecto usa el conjunto versionado incluido en el binario")
	proveedor := opciones.String("proveedor", proveedorSimulado, "simulado, gemini o local; el nivel gratuito sólo aplica a gemini")
	confirmado := opciones.Bool("nivel-gratuito-confirmado", false, "confirma que el proyecto y modelo de Gemini tienen nivel gratuito sin facturación")
	repeticiones := opciones.Int("repeticiones", 3, "repeticiones por caso; tres como mínimo para aprobar una línea base")
	maxSolicitudes := opciones.Int("max-solicitudes", 20, "máximo de solicitudes por invocación, sin reintentos automáticos")
	intervalo := opciones.Duration("intervalo", 12*time.Second, "pausa mínima entre solicitudes; local usa cero si no se fija")
	continuar := opciones.String("continuar", "", "informe real parcial compatible que se continuará")
	base := opciones.String("base", "", "línea base real completa para comparar métricas")
	urlLocal := opciones.String("url-ollama", envOr("VERA_OLLAMA_URL", urlOllamaInicial), "origen del servidor Ollama del modo local")
	timeoutLocal := opciones.Duration("timeout-llamada", 45*time.Second, "plazo por intento del proveedor local; el orquestador corta a 45 s por caso")
	temperaturaLocal := opciones.Float64("temperatura", 0, "temperatura del proveedor local, entre 0 y 1")
	familias := opciones.String("familias", "", "familias separadas por coma; acota las muestras sin cambiar el SHA del conjunto")
	ids := opciones.String("ids", "", "ids de caso separados por coma; acota las muestras sin cambiar el SHA del conjunto")
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
	if *proveedor != proveedorSimulado && *proveedor != proveedorGemini && *proveedor != proveedorLocal {
		_, _ = fmt.Fprintln(diagnostico, "proveedor debe ser simulado, gemini o local.")
		return 2
	}
	explicito := make(map[string]bool)
	opciones.Visit(func(f *flag.Flag) { explicito[f.Name] = true })
	seleccion := evaluacion.SeleccionCasos{Familias: lista(*familias), IDs: lista(*ids)}
	conjunto, err := cargar(*ruta)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostico, "No se pudo cargar el conjunto:", err)
		return 2
	}
	var informe any
	var aprobado, completo, aceptado bool
	switch *proveedor {
	case proveedorLocal:
		if *confirmado {
			_, _ = fmt.Fprintln(diagnostico, "el nivel gratuito no aplica al proveedor local: no factura por solicitud.")
			return 2
		}
		cfg := evaluacion.ConfigReal{Modelo: strings.TrimSpace(os.Getenv("VERA_MODELO")), Repeticiones: *repeticiones, MaxSolicitudes: *maxSolicitudes, Intervalo: *intervalo, Local: true, Seleccion: seleccion}
		if !explicito["intervalo"] {
			cfg.Intervalo = 0
		}
		if err := proveedorLocalValido(*urlLocal, *timeoutLocal, *temperaturaLocal); err != nil {
			_, _ = fmt.Fprintln(diagnostico, "Configuración local inválida:", err)
			return 2
		}
		var informeLocal evaluacion.InformeReal
		informeLocal, err = ejecutarLocal(ctx, conjunto, cfg, *continuar, *base, *urlLocal, *timeoutLocal, float32(*temperaturaLocal))
		informe, aprobado, completo = informeLocal, informeLocal.Aprobado, informeLocal.Completo
		aceptado = informeLocal.AceptacionLocal != nil && informeLocal.AceptacionLocal.Cumple
	case proveedorGemini:
		if len(seleccion.Familias) > 0 || len(seleccion.IDs) > 0 {
			_, _ = fmt.Fprintln(diagnostico, "la selección de casos sólo aplica al proveedor local; Gemini mide el conjunto completo.")
			return 2
		}
		cfg := evaluacion.ConfigReal{Modelo: os.Getenv("VERA_MODELO"), Repeticiones: *repeticiones, MaxSolicitudes: *maxSolicitudes, Intervalo: *intervalo, NivelGratuitoConfirmado: *confirmado}
		var informeGemini evaluacion.InformeReal
		informeGemini, err = ejecutarReal(ctx, conjunto, cfg, *continuar, *base)
		informe, aprobado, completo = informeGemini, informeGemini.Aprobado, informeGemini.Completo
	default:
		if *continuar != "" || *base != "" {
			_, _ = fmt.Fprintln(diagnostico, "continuar y base requieren un proveedor real.")
			return 2
		}
		if len(seleccion.Familias) > 0 || len(seleccion.IDs) > 0 || *confirmado || explicito["url-ollama"] || explicito["timeout-llamada"] || explicito["temperatura"] {
			_, _ = fmt.Fprintln(diagnostico, "las banderas del proveedor real no aplican al modo simulado.")
			return 2
		}
		var simulado evaluacion.Informe
		simulado, err = evaluacion.EjecutarSimulados(ctx, conjunto)
		informe, aprobado, completo = simulado, simulado.Aprobado, true
	}
	if err != nil {
		_, _ = fmt.Fprintln(diagnostico, "No se pudo completar la evaluación:", err)
		return 2
	}
	encoder := json.NewEncoder(salida)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(informe); err != nil {
		_, _ = fmt.Fprintln(diagnostico, "No se pudo escribir el informe.")
		return 2
	}
	if !completo {
		return 3
	}
	if *proveedor == proveedorLocal {
		if !aceptado {
			return 1
		}
		return 0
	}
	if !aprobado {
		return 1
	}
	return 0
}

func envOr(clave, inicial string) string {
	if v := strings.TrimSpace(os.Getenv(clave)); v != "" {
		return v
	}
	return inicial
}

func lista(valor string) []string {
	if strings.TrimSpace(valor) == "" {
		return nil
	}
	partes := strings.Split(valor, ",")
	limpias := make([]string, 0, len(partes))
	for _, p := range partes {
		if v := strings.TrimSpace(p); v != "" {
			limpias = append(limpias, v)
		}
	}
	return limpias
}

func proveedorLocalValido(url string, timeout time.Duration, temperatura float64) error {
	if strings.TrimSpace(url) == "" || timeout <= 0 || timeout > 5*time.Minute || temperatura < 0 || temperatura > 1 {
		return errors.New("VERA_OLLAMA_URL, timeout y temperatura deben ser válidos")
	}
	return nil
}

func ejecutarLocal(ctx context.Context, c evaluacion.Conjunto, cfg evaluacion.ConfigReal, continuar, base, url string, timeout time.Duration, temperatura float32) (evaluacion.InformeReal, error) {
	if strings.TrimSpace(cfg.Modelo) == "" {
		return evaluacion.InformeReal{}, errors.New("define VERA_MODELO con el nombre que Ollama da al modelo local")
	}
	var anterior *evaluacion.InformeReal
	if continuar != "" {
		v, err := leerInformeReal(continuar)
		if err != nil {
			return evaluacion.InformeReal{}, err
		}
		anterior = &v
	}
	var lineaBase *evaluacion.InformeReal
	if base != "" {
		v, err := leerInformeReal(base)
		if err != nil {
			return evaluacion.InformeReal{}, err
		}
		lineaBase = &v
		if err := evaluacion.ValidarLineaBase(c, cfg, v); err != nil {
			return evaluacion.InformeReal{}, err
		}
	}
	p, err := ollama.Nuevo(ollama.Config{URL: url, Modelo: cfg.Modelo, Timeout: timeout, Temperatura: temperatura}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return evaluacion.InformeReal{}, errors.New("revisa -url-ollama, VERA_MODELO, -timeout-llamada y -temperatura")
	}
	r, err := evaluacion.EjecutarReal(ctx, c, cfg, p, anterior)
	if err == nil && lineaBase != nil && r.Completo {
		err = evaluacion.CompararLineaBase(&r, *lineaBase)
	}
	return r, err
}

func ejecutarReal(ctx context.Context, c evaluacion.Conjunto, cfg evaluacion.ConfigReal, continuar, base string) (evaluacion.InformeReal, error) {
	if !cfg.NivelGratuitoConfirmado {
		return evaluacion.InformeReal{}, errors.New("confirma Free Tier con -nivel-gratuito-confirmado; no se habilita facturación automáticamente")
	}
	if os.Getenv("CI") != "" {
		return evaluacion.InformeReal{}, errors.New("la evaluación real no se ejecuta en CI")
	}
	var anterior *evaluacion.InformeReal
	if continuar != "" {
		v, err := leerInformeReal(continuar)
		if err != nil {
			return evaluacion.InformeReal{}, err
		}
		anterior = &v
	}
	var lineaBase *evaluacion.InformeReal
	if base != "" {
		v, err := leerInformeReal(base)
		if err != nil {
			return evaluacion.InformeReal{}, err
		}
		lineaBase = &v
		if err := evaluacion.ValidarLineaBase(c, cfg, v); err != nil {
			return evaluacion.InformeReal{}, err
		}
	}
	p, err := gemini.Nuevo(ctx, gemini.Config{APIKey: os.Getenv("GEMINI_API_KEY"), Modelo: cfg.Modelo, Timeout: 20 * time.Second, Temperatura: 0, SinReintentos: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return evaluacion.InformeReal{}, errors.New("configura GEMINI_API_KEY y VERA_MODELO válidos; la clave no se imprime")
	}
	r, err := evaluacion.EjecutarReal(ctx, c, cfg, p, anterior)
	if err == nil && lineaBase != nil && r.Completo {
		err = evaluacion.CompararLineaBase(&r, *lineaBase)
	}
	return r, err
}

func leerInformeReal(ruta string) (evaluacion.InformeReal, error) {
	var r evaluacion.InformeReal
	f, err := os.Open(ruta) // #nosec G304 -- Archivo de informe elegido por el operador local de la CLI.
	if err != nil {
		return r, err
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, err
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return r, errors.New("informe JSON inválido")
	}
	return r, nil
}

func cargar(ruta string) (evaluacion.Conjunto, error) {
	if ruta == "" {
		return evaluacion.Cargar()
	}
	archivo, err := os.Open(ruta) // #nosec G304 -- Archivo elegido explícitamente por quien ejecuta esta CLI local; no recibe rutas HTTP ni ejecuta su contenido.
	if err != nil {
		return evaluacion.Conjunto{}, err
	}
	defer func() { _ = archivo.Close() }()
	return evaluacion.Leer(archivo)
}
