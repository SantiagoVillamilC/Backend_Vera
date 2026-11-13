package inteligencia

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/avisos"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	yaml "go.yaml.in/yaml/v3"
)

// TestElContratoDeAvisosAgregaAccionCodigoSinQuitarNada lee el YAML y exige que accion_codigo sea
// aditivo: enum igual al de Go, opcional, y accion intacta (texto libre y obligatorio).
func TestElContratoDeAvisosAgregaAccionCodigoSinQuitarNada(t *testing.T) {
	crudo, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("leer el contrato: %v", err)
	}
	var contrato struct {
		Components struct {
			Schemas map[string]struct {
				Type       string   `yaml:"type"`
				Enum       []string `yaml:"enum"`
				Required   []string `yaml:"required"`
				Properties map[string]struct {
					Type      string   `yaml:"type"`
					Reference string   `yaml:"$ref"`
					Enum      []string `yaml:"enum"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(crudo, &contrato); err != nil {
		t.Fatalf("parsear el contrato: %v", err)
	}

	codigo, existe := contrato.Components.Schemas["InteligenciaAvisoAccionCodigo"]
	if !existe {
		t.Fatal("el contrato no define InteligenciaAvisoAccionCodigo")
	}
	declarados := slices.Sorted(slices.Values(codigo.Enum))
	quiero := slices.Sorted(slices.Values(avisos.AccionCodigos()))
	if codigo.Type != "string" || !slices.Equal(declarados, quiero) {
		t.Fatalf("InteligenciaAvisoAccionCodigo = %+v, quiero string con %v", codigo, quiero)
	}

	aviso, existe := contrato.Components.Schemas["InteligenciaAviso"]
	if !existe {
		t.Fatal("el contrato no define InteligenciaAviso")
	}
	propiedad, existe := aviso.Properties["accion_codigo"]
	if !existe || propiedad.Reference != "#/components/schemas/InteligenciaAvisoAccionCodigo" {
		t.Fatalf("accion_codigo = %+v, quiero la referencia al enum", propiedad)
	}
	if slices.Contains(aviso.Required, "accion_codigo") {
		t.Fatalf("accion_codigo no puede ser obligatorio: %v", aviso.Required)
	}
	etiqueta, existe := aviso.Properties["accion"]
	if !existe || etiqueta.Type != "string" || len(etiqueta.Enum) != 0 || !slices.Contains(aviso.Required, "accion") {
		t.Fatalf("accion cambió: %+v required=%v", etiqueta, aviso.Required)
	}
}

// TestAvisoDeFilaDerivaElCodigoDelTipo: el código no se guarda, sale del tipo al leer como accion.
func TestAvisoDeFilaDerivaElCodigoDelTipo(t *testing.T) {
	t.Parallel()
	for _, tipo := range avisos.Tipos {
		fila := filaDeAviso{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Tipo: string(tipo), Clave: "transporte",
			Periodo: pgtype.Date{Time: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		}
		aviso := avisoDeFila(fila)
		if aviso.AccionCodigo == "" || aviso.AccionCodigo != avisos.AccionCodigoDe(tipo) {
			t.Errorf("%q: AccionCodigo = %q, quiero %q", tipo, aviso.AccionCodigo, avisos.AccionCodigoDe(tipo))
		}
		if aviso.Accion != avisos.AccionDe(tipo) {
			t.Errorf("%q: Accion = %q, quiero %q", tipo, aviso.Accion, avisos.AccionDe(tipo))
		}
	}
}

// TestRespuestaDeAvisoTraeAccionYAccionCodigo recorre los cinco tipos por el handler real y exige el
// código nuevo junto a la etiqueta sin cambios.
func TestRespuestaDeAvisoTraeAccionYAccionCodigo(t *testing.T) {
	t.Parallel()
	for _, tipo := range avisos.Tipos {
		aviso := AvisoEmitido{
			ID: uuid.New(), Tipo: tipo, Clave: "transporte", Titulo: avisos.TituloDe(tipo, "transporte"),
			Texto: "texto", Porque: avisos.PorqueDe(tipo), Accion: avisos.AccionDe(tipo),
			AccionCodigo: avisos.AccionCodigoDe(tipo),
			Periodo:      time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Prioridad: 1,
			CreadoEn: time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC),
		}
		rec := httptest.NewRecorder()
		req := peticionConIdentidad(t, "/v1/inteligencia/avisos/"+aviso.ID.String())
		NuevoHandler(&servicioDeLecturaFalso{avisos: []AvisoEmitido{aviso}}).
			DetalleAvisoInteligencia(rec, req, aviso.ID.String())
		if rec.Code != http.StatusOK {
			t.Fatalf("%q: código = %d: %s", tipo, rec.Code, rec.Body.String())
		}
		var respuesta struct {
			Data struct {
				Accion       string `json:"accion"`
				AccionCodigo string `json:"accion_codigo"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &respuesta); err != nil {
			t.Fatalf("JSON: %v", err)
		}
		if respuesta.Data.Accion != avisos.AccionDe(tipo) || respuesta.Data.AccionCodigo != avisos.AccionCodigoDe(tipo) {
			t.Errorf("%q: respuesta = %s", tipo, rec.Body.String())
		}
	}
}

// TestRespuestaDeAvisoOmiteAccionCodigoVacio: un tipo sin código no inventa un valor fuera del enum.
func TestRespuestaDeAvisoOmiteAccionCodigoVacio(t *testing.T) {
	t.Parallel()
	respuesta := respuestaDeAviso(AvisoEmitido{ID: uuid.New(), Tipo: "inventado", Accion: "x"})
	if respuesta.AccionCodigo != nil {
		t.Fatalf("AccionCodigo = %v, quiero nil", *respuesta.AccionCodigo)
	}
}
