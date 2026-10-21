package inteligencia

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const huellaVectoresCategorizacion = "b990677d96311676f86257f3606d114b2eac67d4fe1234ac8b0425f68b15a70d"

type documentoDeVectores struct {
	Catalogo []string     `json:"catalogo"`
	Casos    []casoVector `json:"casos"`
}

type casoVector struct {
	ID      string `json:"id"`
	Entrada struct {
		Canal            string  `json:"canal"`
		Llave            string  `json:"llave"`
		Nombre           string  `json:"nombre"`
		MCC              string  `json:"mcc"`
		TipoDestino      string  `json:"tipo_destino"`
		CategoriaInterna *string `json:"categoria_interna"`
	} `json:"entrada"`
	Recursos struct {
		Memoria         map[string]string `json:"memoria"`
		CatalogoLlaves  map[string]string `json:"catalogo_llaves"`
		CatalogoNombres map[string]string `json:"catalogo_nombres"`
		Lexico          [][2]string       `json:"lexico"`
		CacheLLM        map[string]string `json:"cache_llm"`
	} `json:"recursos"`
	Escalones []int `json:"escalones"`
	Esperado  struct {
		Categoria    *string `json:"categoria"`
		Escalon      int     `json:"escalon"`
		PendienteLLM bool    `json:"pendiente_llm"`
	} `json:"esperado"`
}

func cargarVectores(t *testing.T) documentoDeVectores {
	t.Helper()
	ruta := filepath.Join("testdata", "vectores-dorados-categorizacion.json")
	datos, err := os.ReadFile(ruta)
	if err != nil {
		t.Fatalf("leer vectores: %v", err)
	}
	suma := sha256.Sum256(datos)
	if hex.EncodeToString(suma[:]) != huellaVectoresCategorizacion {
		t.Fatalf("el archivo de vectores cambió: sha256 = %s", hex.EncodeToString(suma[:]))
	}
	var documento documentoDeVectores
	if err := json.Unmarshal(datos, &documento); err != nil {
		t.Fatalf("decodificar vectores: %v", err)
	}
	return documento
}

func TestVectoresDeCategorizacionSeReproducen(t *testing.T) {
	documento := cargarVectores(t)
	if len(documento.Casos) != 22 {
		t.Fatalf("casos = %d, want 22", len(documento.Casos))
	}
	for _, caso := range documento.Casos {
		t.Run(caso.ID, func(t *testing.T) {
			decision := Decidir(entradaDelCaso(caso), recursosDelCaso(caso), escalonesDelCaso(caso))
			if (caso.Esperado.Categoria == nil) != (decision.Categoria == "") {
				t.Fatalf("categoría = %q, want %v", decision.Categoria, caso.Esperado.Categoria)
			}
			if caso.Esperado.Categoria != nil && decision.Categoria != Categoria(*caso.Esperado.Categoria) {
				t.Errorf("categoría = %q, want %q", decision.Categoria, *caso.Esperado.Categoria)
			}
			if decision.Escalon != caso.Esperado.Escalon {
				t.Errorf("escalón = %d, want %d", decision.Escalon, caso.Esperado.Escalon)
			}
			if decision.PendienteLLM != caso.Esperado.PendienteLLM {
				t.Errorf("pendiente = %v, want %v", decision.PendienteLLM, caso.Esperado.PendienteLLM)
			}
		})
	}
}

func TestElCatalogoDelDocumentoEsElFijo(t *testing.T) {
	documento := cargarVectores(t)
	visibles := make(map[Categoria]struct{}, len(catalogoFijo))
	for _, entrada := range CatalogoFijo() {
		if entrada.Grupo != "ninguno" {
			visibles[entrada.Codigo] = struct{}{}
		}
	}
	if len(documento.Catalogo) != len(visibles) {
		t.Fatalf("catálogo del documento = %d, want %d visibles", len(documento.Catalogo), len(visibles))
	}
	for _, codigo := range documento.Catalogo {
		if _, existe := visibles[Categoria(codigo)]; !existe {
			t.Errorf("código %q no está en el catálogo visible", codigo)
		}
	}
}

func entradaDelCaso(caso casoVector) Entrada {
	interna := ""
	if caso.Entrada.CategoriaInterna != nil {
		interna = *caso.Entrada.CategoriaInterna
	}
	return Entrada{
		Canal:            Canal(caso.Entrada.Canal),
		Llave:            caso.Entrada.Llave,
		Nombre:           caso.Entrada.Nombre,
		MCC:              caso.Entrada.MCC,
		TipoDestino:      TipoDestino(caso.Entrada.TipoDestino),
		CategoriaInterna: interna,
	}
}

func recursosDelCaso(caso casoVector) Recursos {
	lexico := make([]ParLexico, 0, len(caso.Recursos.Lexico))
	for _, par := range caso.Recursos.Lexico {
		lexico = append(lexico, ParLexico{Palabra: par[0], Categoria: Categoria(par[1])})
	}
	return Recursos{
		Memoria:         categoriasDelMapa(caso.Recursos.Memoria),
		CatalogoLlaves:  categoriasDelMapa(caso.Recursos.CatalogoLlaves),
		CatalogoNombres: categoriasDelMapa(caso.Recursos.CatalogoNombres),
		Lexico:          lexico,
		CacheLLM:        categoriasDelMapa(caso.Recursos.CacheLLM),
	}
}

func escalonesDelCaso(caso casoVector) Escalones {
	escalones := make(Escalones, len(caso.Escalones))
	for _, escalon := range caso.Escalones {
		escalones[escalon] = struct{}{}
	}
	return escalones
}

func categoriasDelMapa(mapa map[string]string) map[string]Categoria {
	salida := make(map[string]Categoria, len(mapa))
	for clave, valor := range mapa {
		salida[clave] = Categoria(valor)
	}
	return salida
}
