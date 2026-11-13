package main

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// La matriz por entorno es la fuente de verdad de las banderas de la API
// (CF-A0-02). Estas pruebas atan tres cosas que se desincronizan solas: la
// matriz, el .env.example y lo que LoadConfig resuelve de verdad.
const (
	rutaBanderas   = "../../infra/servidor/banderas.tsv"
	rutaEnvEjemplo = "../../.env.example"
	rutaPlantilla  = "../../.env.%s.example"
)

var destinosDeLaMatriz = []string{"local", "demo", "produccion"}

// nombreDeVariableDeEntorno filtra las filas que describen un escalón que no
// se enciende con una bandera (MCC y catálogo).
var nombreDeVariableDeEntorno = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// filaBanderas es una fila de infra/servidor/banderas.tsv.
type filaBanderas struct {
	variable string
	columnas map[string]string
	nota     string
}

func (f filaBanderas) esVariable() bool {
	return nombreDeVariableDeEntorno.MatchString(f.variable)
}

func leerBanderas(t *testing.T) []filaBanderas {
	t.Helper()

	crudo, err := os.ReadFile(rutaBanderas)
	if err != nil {
		t.Fatalf("leer %s: %v", rutaBanderas, err)
	}

	var filas []filaBanderas
	vistas := map[string]bool{}
	sinVariable := 0
	for numero, linea := range strings.Split(string(crudo), "\n") {
		if strings.TrimSpace(linea) == "" || strings.HasPrefix(strings.TrimSpace(linea), "#") {
			continue
		}
		campos := strings.Split(linea, "\t")
		if len(campos) != 5 {
			t.Fatalf("%s:%d tiene %d columnas separadas por tabulador, se esperan cinco",
				rutaBanderas, numero+1, len(campos))
		}
		if campos[0] == "variable" {
			continue
		}
		columna := map[string]string{
			"local":      campos[1],
			"demo":       campos[2],
			"produccion": campos[3],
		}
		fila := filaBanderas{variable: campos[0], columnas: columna, nota: campos[4]}
		if fila.nota == "" {
			t.Errorf("%s:%d no explica la bandera", rutaBanderas, numero+1)
		}
		if !fila.esVariable() {
			sinVariable++
			filas = append(filas, fila)
			continue
		}
		if vistas[fila.variable] {
			t.Errorf("%s: la variable %s aparece dos veces", rutaBanderas, fila.variable)
		}
		vistas[fila.variable] = true
		filas = append(filas, fila)
	}

	if len(vistas) == 0 {
		t.Fatalf("%s no declara ninguna bandera", rutaBanderas)
	}
	if sinVariable != 2 {
		t.Errorf("%s describe %d escalones sin bandera, se esperan 2 (MCC y catálogo)", rutaBanderas, sinVariable)
	}
	return filas
}

// leerParesDeEnv lee un archivo VAR=valor y devuelve el mapa, sin comentarios.
func leerParesDeEnv(t *testing.T, ruta string) map[string]string {
	t.Helper()

	crudo, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer %s: %v", ruta, err)
	}
	pares := map[string]string{}
	for _, linea := range strings.Split(string(crudo), "\n") {
		limpia := strings.TrimSpace(linea)
		if limpia == "" || strings.HasPrefix(limpia, "#") || !strings.Contains(limpia, "=") {
			continue
		}
		clave, valor, _ := strings.Cut(limpia, "=")
		pares[clave] = valor
	}
	return pares
}

// valoresPorDefecto traduce lo que resolvió LoadConfig a las mismas formas
// textuales que usa un archivo de entorno.
func valoresPorDefecto(cfg *Config) map[string]string {
	return map[string]string{
		"CLASIFICADOR":             string(cfg.Clasificador),
		"LEXICO":                   string(cfg.Lexico),
		"AVISOS_ACTIVO":            strconv.FormatBool(cfg.AvisosActivo),
		"AVISOS_HORA":              cfg.AvisosHora,
		"AVISOS_CUPO":              strconv.Itoa(cfg.AvisosCupo),
		"VERA_PROVEEDOR":           cfg.Vera.Proveedor,
		"VERA_MODELO":              cfg.Vera.Modelo,
		"METRICAS_ACTIVO":          strconv.FormatBool(cfg.MetricasActivo),
		"ENRIQUECIMIENTO_INTERVAL": cfg.EnriquecimientoInterval.String(),
	}
}

// mismoValor compara la forma textual del archivo con la de LoadConfig. Las
// duraciones se comparan ya parseadas: 5m y 5m0s son el mismo intervalo.
func mismoValor(variable, enArchivo, deConfig string) bool {
	if strings.HasSuffix(variable, "_INTERVAL") {
		enArchivoDur, errArchivo := time.ParseDuration(enArchivo)
		deConfigDur, errConfig := time.ParseDuration(deConfig)
		return errArchivo == nil && errConfig == nil && enArchivoDur == deConfigDur
	}
	return enArchivo == deConfig
}

// valorQueGobierna es el valor que la prueba aplica a una bandera para
// comprobar que LoadConfig la lee de verdad. Tiene que diferir del valor por
// defecto: si coincide, la prueba pasaría sin que nadie leyera nada.
type valorQueGobierna struct {
	valor   string
	acomodo map[string]string
}

var valoresQueGobiernan = map[string]valorQueGobierna{
	"CLASIFICADOR":             {valor: "tfidf"},
	"LEXICO":                   {valor: "v2"},
	"AVISOS_ACTIVO":            {valor: "true"},
	"AVISOS_HORA":              {valor: "07:15"},
	"AVISOS_CUPO":              {valor: "5"},
	"VERA_PROVEEDOR":           {valor: "gemini", acomodo: map[string]string{"GEMINI_API_KEY": "clave-de-prueba", "VERA_MODELO": "modelo-de-prueba"}},
	"VERA_MODELO":              {valor: "modelo-de-prueba", acomodo: map[string]string{"VERA_PROVEEDOR": "gemini", "GEMINI_API_KEY": "clave-de-prueba"}},
	"METRICAS_ACTIVO":          {valor: "true", acomodo: map[string]string{"METRICAS_TOKEN": strings.Repeat("m", 32)}},
	"ENRIQUECIMIENTO_INTERVAL": {valor: "17m"},
}

// TestBanderasLasLeeLaConfig exige que LoadConfig resuelva cada bandera de la
// matriz a partir del entorno. Aplica un valor distinto del default y compara
// el campo de Config contra ese valor: si la variable no se lee, el campo se
// queda en el default y la prueba falla.
func TestBanderasLasLeeLaConfig(t *testing.T) {
	filas := leerBanderas(t)

	baseDeConfig(t)
	for _, fila := range filas {
		if fila.esVariable() {
			t.Setenv(fila.variable, "")
		}
	}
	cfgBase, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig sin banderas: %v", err)
	}
	defectos := valoresPorDefecto(cfgBase)

	enLaMatriz := map[string]bool{}
	for _, fila := range filas {
		if !fila.esVariable() {
			continue
		}
		enLaMatriz[fila.variable] = true

		caso, conocida := valoresQueGobiernan[fila.variable]
		if !conocida {
			t.Errorf("%s declara %s y la prueba no sabe qué valor aplicarle para comprobar que LoadConfig la lee",
				rutaBanderas, fila.variable)
			continue
		}
		defecto, sabeLeerDefecto := defectos[fila.variable]
		if !sabeLeerDefecto {
			t.Errorf("la prueba no sabe leer %s de LoadConfig: agrega su campo a valoresPorDefecto", fila.variable)
			continue
		}
		if mismoValor(fila.variable, caso.valor, defecto) {
			t.Errorf("%s=%q coincide con el default %q: elige otro valor para que la prueba no pase por casualidad",
				fila.variable, caso.valor, defecto)
			continue
		}

		t.Run(fila.variable, func(t *testing.T) {
			baseDeConfig(t)
			for _, otra := range filas {
				if otra.esVariable() {
					t.Setenv(otra.variable, "")
				}
			}
			for clave, valor := range caso.acomodo {
				t.Setenv(clave, valor)
			}
			t.Setenv(fila.variable, caso.valor)

			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig con %s=%q: %v", fila.variable, caso.valor, err)
			}
			resuelto := valoresPorDefecto(cfg)[fila.variable]
			if !mismoValor(fila.variable, caso.valor, resuelto) {
				t.Errorf("%s=%q y LoadConfig resolvió %q: la bandera no gobierna nada",
					fila.variable, caso.valor, resuelto)
			}
		})
	}

	for variable := range valoresQueGobiernan {
		if !enLaMatriz[variable] {
			t.Errorf("la prueba aplica %s, que %s ya no declara: quita el caso", variable, rutaBanderas)
		}
	}
}

// TestBanderasDeCadaDestinoArrancanLaConfig aplica cada columna con t.Setenv y
// exige que LoadConfig acepte esos valores.
func TestBanderasDeCadaDestinoArrancanLaConfig(t *testing.T) {
	filas := leerBanderas(t)
	for _, destino := range destinosDeLaMatriz {
		t.Run(destino, func(t *testing.T) {
			baseDeConfig(t)
			for _, fila := range filas {
				if !fila.esVariable() {
					continue
				}
				t.Setenv(fila.variable, fila.columnas[destino])
			}
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig con la columna %s: %v", destino, err)
			}
			if cfg.Vera.Proveedor == "gemini" && cfg.Vera.APIKey == "" {
				t.Fatalf("la columna %s pide gemini sin GEMINI_API_KEY en la matriz", destino)
			}
		})
	}
}

// TestEnvEjemploCoincideConLosDefaults exige que .env.example diga, variable
// por variable, lo mismo que resuelve LoadConfig sin entorno.
func TestEnvEjemploCoincideConLosDefaults(t *testing.T) {
	baseDeConfig(t)
	filas := leerBanderas(t)
	for _, fila := range filas {
		if fila.esVariable() {
			t.Setenv(fila.variable, "")
		}
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig sin banderas: %v", err)
	}

	defectos := valoresPorDefecto(cfg)
	ejemplo := leerParesDeEnv(t, rutaEnvEjemplo)
	for _, fila := range filas {
		if !fila.esVariable() {
			continue
		}
		defecto, conocida := defectos[fila.variable]
		if !conocida {
			t.Errorf("la prueba no sabe leer %s de LoadConfig: agrega su campo a valoresPorDefecto", fila.variable)
			continue
		}
		documentado, presente := ejemplo[fila.variable]
		if !presente {
			t.Errorf("%s no aparece en %s", fila.variable, rutaEnvEjemplo)
			continue
		}
		if !mismoValor(fila.variable, documentado, defecto) {
			t.Errorf("%s: %s dice %q y el default real es %q", fila.variable, rutaEnvEjemplo, documentado, defecto)
		}
	}
}

// TestPlantillasDerivanDeLaMatriz compara las tres plantillas de destino
// contra su columna y exige que no falte ninguna bandera ni sobre ninguna: una
// clave que la matriz no declara es una bandera que ningún destino emite.
func TestPlantillasDerivanDeLaMatriz(t *testing.T) {
	filas := leerBanderas(t)
	enLaMatriz := map[string]bool{}
	for _, fila := range filas {
		if fila.esVariable() {
			enLaMatriz[fila.variable] = true
		}
	}
	for _, destino := range destinosDeLaMatriz {
		t.Run(destino, func(t *testing.T) {
			ruta := strings.Replace(rutaPlantilla, "%s", destino, 1)
			plantilla := leerParesDeEnv(t, ruta)
			for clave := range plantilla {
				if !enLaMatriz[clave] {
					t.Errorf("%s declara %s, que %s no tiene: quita la clave o agrégala a la matriz",
						ruta, clave, rutaBanderas)
				}
			}
			for _, fila := range filas {
				if !fila.esVariable() {
					continue
				}
				esperado := fila.columnas[destino]
				documentado, presente := plantilla[fila.variable]
				if !presente {
					t.Errorf("%s no aparece en %s", fila.variable, ruta)
					continue
				}
				if documentado != esperado {
					t.Errorf("%s: %s dice %q y la columna %s de la matriz dice %q",
						fila.variable, ruta, documentado, destino, esperado)
				}
			}
		})
	}
}

// TestEnvEjemploDocumentaLaVoz cubre las dos variables que quedan fuera de la
// matriz por ser opcionales y no depender del destino.
func TestEnvEjemploDocumentaLaVoz(t *testing.T) {
	t.Parallel()

	ejemplo := leerParesDeEnv(t, rutaEnvEjemplo)
	for _, variable := range []string{"VERA_VOZ_URL", "VERA_VOZ_TOKEN", "LOG_LEVEL", "LOG_FORMAT"} {
		if _, presente := ejemplo[variable]; !presente {
			t.Errorf("%s no aparece en %s", variable, rutaEnvEjemplo)
		}
	}
}
