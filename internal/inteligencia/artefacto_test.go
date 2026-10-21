package inteligencia

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

func manifiestoEmbebido(t *testing.T) Manifiesto {
	t.Helper()
	var declarado Manifiesto
	if err := json.Unmarshal(manifiestoArtefactoTFIDF, &declarado); err != nil {
		t.Fatalf("decodificar el manifiesto embebido: %v", err)
	}
	return declarado
}

func TestElArtefactoEmbebidoCarga(t *testing.T) {
	modelo, err := ModeloTFIDFEmbebido()
	if err != nil {
		t.Fatalf("cargar el artefacto embebido: %v", err)
	}
	declarado := manifiestoEmbebido(t)
	if modelo.Version != VersionArtefacto {
		t.Fatalf("versión = %q, want %q", modelo.Version, VersionArtefacto)
	}
	if modelo.Huella != HuellaArtefactoTFIDF {
		t.Fatalf("huella = %q, want %q", modelo.Huella, HuellaArtefactoTFIDF)
	}
	if declarado.SHA256Artefacto != HuellaArtefactoTFIDF {
		t.Fatalf("el manifiesto declara %q", declarado.SHA256Artefacto)
	}
	if len(modelo.IDF) != declarado.Conteos.IDF {
		t.Fatalf("idf = %d, want %d", len(modelo.IDF), declarado.Conteos.IDF)
	}
	if len(modelo.Centroides) != declarado.Conteos.Centroides {
		t.Fatalf("centroides = %d, want %d", len(modelo.Centroides), declarado.Conteos.Centroides)
	}
	if len(modelo.Lexico) != declarado.Conteos.Lexico {
		t.Fatalf("léxico = %d, want %d", len(modelo.Lexico), declarado.Conteos.Lexico)
	}
}

func TestElManifiestoCoincideConElArtefacto(t *testing.T) {
	suma := sha256.Sum256(artefactoTFIDF)
	if obtenida := hex.EncodeToString(suma[:]); obtenida != HuellaArtefactoTFIDF {
		t.Fatalf("el artefacto embebido tiene sha256 %s, want %s", obtenida, HuellaArtefactoTFIDF)
	}
	if _, err := CargarModeloVerificado(artefactoTFIDF, manifiestoArtefactoTFIDF); err != nil {
		t.Fatalf("cargar el artefacto verificado: %v", err)
	}
}

func TestCargarModeloVerificadoRechazaUnaHuellaDistinta(t *testing.T) {
	declarado := manifiestoEmbebido(t)
	declarado.SHA256Artefacto = "0" + declarado.SHA256Artefacto[1:]
	alterado, err := json.Marshal(declarado)
	if err != nil {
		t.Fatalf("serializar: %v", err)
	}
	if _, err := CargarModeloVerificado(artefactoTFIDF, alterado); !errors.Is(err, ErrHuellaDistinta) {
		t.Fatalf("err = %v, want ErrHuellaDistinta", err)
	}
}

func TestCargarModeloVerificadoRechazaConteosDistintos(t *testing.T) {
	declarado := manifiestoEmbebido(t)
	declarado.Conteos.IDF++
	alterado, err := json.Marshal(declarado)
	if err != nil {
		t.Fatalf("serializar: %v", err)
	}
	if _, err := CargarModeloVerificado(artefactoTFIDF, alterado); !errors.Is(err, ErrArtefactoInvalido) {
		t.Fatalf("err = %v, want ErrArtefactoInvalido", err)
	}
}

func TestElArtefactoNoTraeCategoriasDeIngreso(t *testing.T) {
	modelo, err := ModeloTFIDFEmbebido()
	if err != nil {
		t.Fatalf("cargar: %v", err)
	}
	for _, categoria := range []Categoria{
		CategoriaSalario, CategoriaTransferenciaRecibida, CategoriaReembolso, CategoriaOtrosIngresos,
	} {
		if _, existe := modelo.Centroides[categoria]; existe {
			t.Fatalf("el artefacto trae la categoría de ingreso %q", categoria)
		}
	}
	for categoria, categoriaDelLexico := range modelo.Lexico {
		if !EsCategoriaVisible(categoriaDelLexico) {
			t.Fatalf("la semilla %q apunta a %q, que no es visible", categoria, categoriaDelLexico)
		}
	}
}
