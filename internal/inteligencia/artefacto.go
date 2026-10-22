package inteligencia

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// El artefacto y su manifiesto viajan dentro del binario. Se entrenan con el catálogo público v2 y
// se copian byte a byte, así que el sha256 del archivo es la identidad del modelo.
//
//go:embed modelo/tfidf-v2.json
var artefactoTFIDF []byte

//go:embed modelo/tfidf-v2.manifest.json
var manifiestoArtefactoTFIDF []byte

// HuellaArtefactoTFIDF es el sha256 del artefacto congelado. Una prueba lo recalcula sobre los bytes
// embebidos, así que editar el artefacto a mano falla aunque esta constante se actualice.
const HuellaArtefactoTFIDF = "fe69c5efda1f885cb9bd0de0fcd835d7cf098c976f9eb868fddf29d79e864253"

// Manifiesto describe el artefacto embebido: de dónde salió y qué contiene.
type Manifiesto struct {
	Version          int    `json:"version"`
	Archivo          string `json:"archivo"`
	ArtefactoVersion string `json:"artefacto_version"`
	SHA256Artefacto  string `json:"sha256_artefacto"`
	Bytes            int    `json:"bytes"`
	GeneradoPor      string `json:"generado_por"`
	Fuente           struct {
		Catalogo        string `json:"catalogo"`
		SHA256Catalogo  string `json:"sha256_catalogo"`
		VersionCatalogo string `json:"version_catalogo"`
	} `json:"fuente"`
	Filtro struct {
		Categorias []string `json:"categorias"`
		Excluidas  []string `json:"excluidas"`
	} `json:"filtro"`
	Conteos struct {
		NombresLeidos     int `json:"nombres_leidos"`
		NombresEntrenados int `json:"nombres_entrenados"`
		IDF               int `json:"idf"`
		Centroides        int `json:"centroides"`
		Lexico            int `json:"lexico"`
		PodaPorCentroide  int `json:"poda_por_centroide"`
	} `json:"conteos"`
	Categorias           []string          `json:"categorias"`
	CategoriasProhibidas []string          `json:"categorias_prohibidas"`
	SinLexico            bool              `json:"sin_lexico"`
	CatalogoFirmado      []string          `json:"catalogo_firmado"`
	LexicoDescartado     map[string]string `json:"lexico_descartado"`
	Umbrales             map[string]string `json:"umbrales"`
	Nota                 string            `json:"nota"`
}

// ModeloTFIDFEmbebido carga el artefacto embebido y comprueba que coincida con su manifiesto.
func ModeloTFIDFEmbebido() (*Modelo, error) {
	return CargarModeloVerificado(artefactoTFIDF, manifiestoArtefactoTFIDF)
}

// CargarModeloVerificado carga el artefacto y exige que su sha256 y sus conteos sean los del
// manifiesto. Un artefacto con otra huella no es el que se congeló, así que no se usa.
func CargarModeloVerificado(artefacto, manifiesto []byte) (*Modelo, error) {
	declarado, err := cargarManifiesto(manifiesto)
	if err != nil {
		return nil, err
	}
	suma := sha256.Sum256(artefacto)
	huella := hex.EncodeToString(suma[:])
	if declarado.SHA256Artefacto == "" || declarado.SHA256Artefacto != huella {
		return nil, fmt.Errorf("%w: el manifiesto declara %q y el artefacto tiene %q",
			ErrHuellaDistinta, declarado.SHA256Artefacto, huella)
	}
	modelo, err := CargarModelo(artefacto)
	if err != nil {
		return nil, err
	}
	if declarado.ArtefactoVersion != modelo.Version {
		return nil, fmt.Errorf("%w: el manifiesto declara %q y el artefacto %q",
			ErrVersionDesconocida, declarado.ArtefactoVersion, modelo.Version)
	}
	if err := VerificarHuella(modelo, HuellaArtefactoTFIDF); err != nil {
		return nil, err
	}
	if declarado.Conteos.IDF != len(modelo.IDF) ||
		declarado.Conteos.Centroides != len(modelo.Centroides) ||
		declarado.Conteos.Lexico != len(modelo.Lexico) {
		return nil, fmt.Errorf("%w: los conteos del manifiesto no coinciden con el artefacto",
			ErrArtefactoInvalido)
	}
	declaradas := append([]string(nil), declarado.Categorias...)
	sort.Strings(declaradas)
	reales := modelo.Categorias()
	if len(declaradas) != len(reales) {
		return nil, fmt.Errorf("%w: el manifiesto declara %d categorías y el artefacto %d",
			ErrArtefactoInvalido, len(declaradas), len(reales))
	}
	for i, categoria := range reales {
		if declaradas[i] != string(categoria) {
			return nil, fmt.Errorf("%w: el manifiesto declara %q y el artefacto %q",
				ErrCategoriaDesconocida, declaradas[i], categoria)
		}
	}
	if err := verificarProhibidasDeclaradas(declarado, modelo); err != nil {
		return nil, err
	}
	return modelo, nil
}

// cargarManifiesto decodifica el manifiesto y rechaza campos que el contrato no conoce. Un campo
// nuevo sin dueño en el struct pasaría inadvertido con `json.Unmarshal`, que es justo lo que se
// quiere evitar con `sin_lexico` y `categorias_prohibidas`.
func cargarManifiesto(datos []byte) (Manifiesto, error) {
	var declarado Manifiesto
	decodificador := json.NewDecoder(bytes.NewReader(datos))
	decodificador.DisallowUnknownFields()
	if err := decodificador.Decode(&declarado); err != nil {
		return Manifiesto{}, fmt.Errorf("%w: manifiesto: %w", ErrArtefactoInvalido, err)
	}
	if err := decodificador.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifiesto{}, fmt.Errorf("%w: manifiesto: hay datos después del JSON", ErrArtefactoInvalido)
	}
	return declarado, nil
}

// verificarProhibidasDeclaradas exige que el manifiesto describa la misma lista de prohibidas que el
// artefacto y que un manifiesto con `sin_lexico` no conviva con semillas cargadas.
func verificarProhibidasDeclaradas(declarado Manifiesto, modelo *Modelo) error {
	reales := make([]string, len(modelo.prohibidas))
	for i, categoria := range modelo.prohibidas {
		reales[i] = string(categoria)
	}
	if len(declarado.CategoriasProhibidas) != len(reales) {
		return fmt.Errorf("%w: el manifiesto declara %d categorías prohibidas y el artefacto %d",
			ErrArtefactoInvalido, len(declarado.CategoriasProhibidas), len(reales))
	}
	for i := range reales {
		if declarado.CategoriasProhibidas[i] != reales[i] {
			return fmt.Errorf("%w: el manifiesto declara prohibida %q y el artefacto %q",
				ErrArtefactoInvalido, declarado.CategoriasProhibidas[i], reales[i])
		}
	}
	if declarado.SinLexico && len(modelo.Lexico) != 0 {
		return fmt.Errorf("%w: el manifiesto declara sin_lexico y el artefacto trae %d semillas",
			ErrArtefactoInvalido, len(modelo.Lexico))
	}
	return nil
}
