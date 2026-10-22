package inteligencia

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const huellaVectoresClasificador = "6a948bb0d0fb2de7a328538fa0510af380fca0bb452085ee19080209b6ad001b"

// clasesObligatorias son las que el documento tiene que cubrir. Borrar el caso difícil falla.
var clasesObligatorias = []string{
	"coseno_fuerte",
	"fuerte_con_lexico_en_contra",
	"coseno_debil_con_lexico",
	"coseno_debil_sin_lexico",
	"abstencion",
	"nombre_vacio",
	"solo_genericos",
	"mcc_cambia",
	"mcc_no_cambia",
	"empate_alfabetico",
	"tildes_y_minusculas",
	"artefacto",
}

type documentoDeVectoresClasificador struct {
	Version         int                        `json:"version"`
	ArtefactoSHA256 string                     `json:"artefacto_sha256"`
	Clases          []string                   `json:"clases"`
	Modelos         map[string]json.RawMessage `json:"modelos"`
	Casos           []casoDeClasificadorVector `json:"casos"`
}

type casoDeClasificadorVector struct {
	ID           string `json:"id"`
	Clase        string `json:"clase"`
	Modelo       string `json:"modelo"`
	Nombre       string `json:"nombre"`
	MCCCategoria string `json:"mcc_categoria"`
	Esperado     struct {
		Categoria  *string `json:"categoria"`
		Abstencion bool    `json:"abstencion"`
		Rama       string  `json:"rama"`
	} `json:"esperado"`
}

func cargarVectoresDelClasificador(t *testing.T) documentoDeVectoresClasificador {
	t.Helper()
	ruta := filepath.Join("testdata", "vectores-clasificador-tfidf.json")
	datos, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer vectores del clasificador: %v", err)
	}
	suma := sha256.Sum256(datos)
	if hex.EncodeToString(suma[:]) != huellaVectoresClasificador {
		t.Fatalf("el archivo de vectores del clasificador cambió: sha256 = %s", hex.EncodeToString(suma[:]))
	}
	var documento documentoDeVectoresClasificador
	if err := json.Unmarshal(datos, &documento); err != nil {
		t.Fatalf("decodificar vectores del clasificador: %v", err)
	}
	return documento
}

func TestVectoresDelClasificadorSeReproducen(t *testing.T) {
	documento := cargarVectoresDelClasificador(t)
	artefacto, err := ModeloTFIDFEmbebido()
	if err != nil {
		t.Fatalf("cargar el artefacto: %v", err)
	}
	if documento.ArtefactoSHA256 != artefacto.Huella {
		t.Fatalf("los vectores anclan el artefacto %s y el embebido es %s",
			documento.ArtefactoSHA256, artefacto.Huella)
	}
	for _, caso := range documento.Casos {
		t.Run(caso.ID, func(t *testing.T) {
			modelo := artefacto
			if caso.Modelo != "artefacto" {
				crudo, existe := documento.Modelos[caso.Modelo]
				if !existe {
					t.Fatalf("el caso referencia el modelo %q, que no está en el documento", caso.Modelo)
				}
				cargado, err := CargarModelo(crudo)
				if err != nil {
					t.Fatalf("cargar el modelo %q: %v", caso.Modelo, err)
				}
				modelo = cargado
			}
			categoria, decidio := Predecir(modelo, caso.Nombre, caso.MCCCategoria)
			if decidio != !caso.Esperado.Abstencion {
				t.Fatalf("decidió = %v, want %v", decidio, !caso.Esperado.Abstencion)
			}
			if caso.Esperado.Abstencion {
				if categoria != "" {
					t.Fatalf("categoría = %q en una abstención", categoria)
				}
				return
			}
			if caso.Esperado.Categoria == nil {
				t.Fatalf("el caso decidió pero el vector no trae categoría")
			}
			if string(categoria) != *caso.Esperado.Categoria {
				t.Fatalf("categoría = %q, want %q", categoria, *caso.Esperado.Categoria)
			}
		})
	}
}

func TestVectoresDelClasificadorCubrenLasClases(t *testing.T) {
	documento := cargarVectoresDelClasificador(t)
	presentes := map[string]struct{}{}
	for _, clase := range documento.Clases {
		presentes[clase] = struct{}{}
	}
	for _, clase := range clasesObligatorias {
		if _, existe := presentes[clase]; !existe {
			t.Fatalf("falta la clase obligatoria %q en los vectores", clase)
		}
	}
	for _, caso := range documento.Casos {
		if _, existe := presentes[caso.Clase]; !existe {
			t.Fatalf("el caso %q declara la clase %q, que no está en el documento", caso.ID, caso.Clase)
		}
	}
}
