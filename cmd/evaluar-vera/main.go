// Command evaluar-vera ejecuta casos simulados o una evaluación explícita con Gemini.
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

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/evaluacion"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/gemini"
)

func main() { os.Exit(ejecutar(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

func ejecutar(ctx context.Context, args []string, salida, diagnostico io.Writer) int {
	opciones := flag.NewFlagSet("evaluar-vera", flag.ContinueOnError)
	opciones.SetOutput(diagnostico)
	ruta := opciones.String("casos", "", "JSON alternativo; por defecto usa el conjunto versionado incluido en el binario")
	modo := opciones.String("modo", "simulado", "simulado o real; real requiere clave y confirmación de Free Tier")
	confirmado := opciones.Bool("nivel-gratuito-confirmado", false, "confirma que el proyecto y modelo elegidos tienen nivel gratuito sin facturación")
	repeticiones := opciones.Int("repeticiones", 3, "repeticiones por caso; tres como mínimo para aprobar una línea base")
	maxSolicitudes := opciones.Int("max-solicitudes", 20, "máximo de solicitudes por invocación, sin reintentos automáticos")
	intervalo := opciones.Duration("intervalo", 12*time.Second, "pausa mínima entre solicitudes de Gemini")
	continuar := opciones.String("continuar", "", "informe real parcial compatible que se continuará")
	base := opciones.String("base", "", "línea base real completa para comparar métricas")
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
	if *modo != "simulado" && *modo != "real" {
		_, _ = fmt.Fprintln(diagnostico, "modo debe ser simulado o real.")
		return 2
	}
	conjunto, err := cargar(*ruta)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostico, "No se pudo cargar el conjunto:", err)
		return 2
	}
	var informe any
	var aprobado, completo bool
	if *modo == "real" {
		cfg := evaluacion.ConfigReal{Modelo: os.Getenv("VERA_MODELO"), Repeticiones: *repeticiones, MaxSolicitudes: *maxSolicitudes, Intervalo: *intervalo, NivelGratuitoConfirmado: *confirmado}
		var informeGemini evaluacion.InformeReal
		informeGemini, err = ejecutarReal(ctx, conjunto, cfg, *continuar, *base)
		informe, aprobado, completo = informeGemini, informeGemini.Aprobado, informeGemini.Completo
	} else {
		if *continuar != "" || *base != "" {
			_, _ = fmt.Fprintln(diagnostico, "continuar y base requieren modo real.")
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
	if !aprobado {
		return 1
	}
	return 0
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
