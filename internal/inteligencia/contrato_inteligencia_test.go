package inteligencia

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// contratoDeInteligencia es la parte del contrato que la cascada tiene que respetar. El YAML es la
// fuente y estas pruebas fallan si la copia declarada en Go se separa de él.
type contratoDeInteligencia struct {
	Components struct {
		Schemas map[string]struct {
			Enum []string `yaml:"enum"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func cargarContrato(t *testing.T) contratoDeInteligencia {
	t.Helper()
	crudo, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("leer el contrato: %v", err)
	}
	var contrato contratoDeInteligencia
	if err := yaml.Unmarshal(crudo, &contrato); err != nil {
		t.Fatalf("parsear el contrato: %v", err)
	}
	return contrato
}

func enumDelContrato(t *testing.T, contrato contratoDeInteligencia, esquema string) []string {
	t.Helper()
	definicion, existe := contrato.Components.Schemas[esquema]
	if !existe {
		t.Fatalf("el contrato no define el esquema %q", esquema)
	}
	if len(definicion.Enum) == 0 {
		t.Fatalf("el esquema %q del contrato no declara enum", esquema)
	}
	return definicion.Enum
}

// TestContratoDeOrigenesYCatalogo compara el enum de orígenes y los dos del catálogo contra las
// listas que usa la cascada. Un origen que el lote escriba y el contrato no admita, o una categoría
// que la app ofrezca y el catálogo no tenga, rompen la prueba.
func TestContratoDeOrigenesYCatalogo(t *testing.T) {
	contrato := cargarContrato(t)

	origenes := enumDelContrato(t, contrato, "OrigenEnriquecido")
	if !slices.Equal(origenes, OrigenesEscribibles) {
		t.Fatalf("OrigenEnriquecido = %v, quiero %v", origenes, OrigenesEscribibles)
	}

	catalogo := make([]string, 0, len(catalogoFijo))
	visibles := make([]string, 0, len(categoriasVisibles))
	for _, entrada := range CatalogoFijo() {
		catalogo = append(catalogo, string(entrada.Codigo))
		if EsCategoriaVisible(entrada.Codigo) {
			visibles = append(visibles, string(entrada.Codigo))
		}
	}
	if codigos := enumDelContrato(t, contrato, "Categoria"); !slices.Equal(codigos, catalogo) {
		t.Fatalf("Categoria = %v, quiero %v", codigos, catalogo)
	}
	if codigos := enumDelContrato(t, contrato, "CategoriaOfrecida"); !slices.Equal(codigos, visibles) {
		t.Fatalf("CategoriaOfrecida = %v, quiero %v", codigos, visibles)
	}
}

// TestElContratoExigeElEscalon exige que el escalón viaje en cada movimiento: sin él la app no puede
// mostrar qué peldaño decidió, y la abstención del clasificador se leería como un pago sin cascada.
func TestElContratoExigeElEscalon(t *testing.T) {
	crudo, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("leer el contrato: %v", err)
	}
	var contrato struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string `yaml:"required"`
				Properties map[string]struct {
					Reference string `yaml:"$ref"`
					Minimum   *int   `yaml:"minimum"`
					Maximum   *int   `yaml:"maximum"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(crudo, &contrato); err != nil {
		t.Fatalf("parsear el contrato: %v", err)
	}
	movimiento, existe := contrato.Components.Schemas["InteligenciaMovimiento"]
	if !existe {
		t.Fatal("el contrato no define InteligenciaMovimiento")
	}
	if !slices.Contains(movimiento.Required, "escalon") {
		t.Fatalf("InteligenciaMovimiento required = %v, quiero escalon", movimiento.Required)
	}
	escalon, existe := movimiento.Properties["escalon"]
	if !existe || escalon.Minimum == nil || escalon.Maximum == nil ||
		*escalon.Minimum != EscalonMemoria || *escalon.Maximum != EscalonSinCategoria {
		t.Fatalf("escalon = %+v, quiero el rango %d..%d", escalon, EscalonMemoria, EscalonSinCategoria)
	}
	origen, existe := movimiento.Properties["origen_categoria"]
	if !existe || origen.Reference != "#/components/schemas/OrigenEnriquecido" {
		t.Fatalf("origen_categoria = %+v, quiero la referencia a OrigenEnriquecido", origen)
	}
}
