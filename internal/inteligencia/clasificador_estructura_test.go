package inteligencia

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// archivosDelClasificador son los que forman la inferencia y su cargador.
var archivosDelClasificador = []string{
	"clasificador.go",
	"modelo.go",
	"similitud.go",
	"artefacto.go",
}

// importsPermitidos es una lista blanca, no una lista negra: si aparece red, reloj, entorno o el
// repositorio, la prueba falla aunque el paquete siga compilando.
var importsPermitidos = map[string]struct{}{
	"bytes":         {},
	"crypto/sha256": {},
	"embed":         {},
	"encoding/hex":  {},
	"encoding/json": {},
	"errors":        {},
	"fmt":           {},
	"io":            {},
	"math":          {},
	"sort":          {},
	"strconv":       {},
	"strings":       {},
	"unicode":       {},
}

// identificadoresProhibidos cubren el monto y la identidad, que el clasificador no puede ver.
var identificadoresProhibidos = map[string]struct{}{
	"Monto":          {},
	"MontoCentavos":  {},
	"Saldo":          {},
	"Usuario":        {},
	"UsuarioID":      {},
	"Correo":         {},
	"Documento":      {},
	"Identificacion": {},
	"Descripcion":    {},
	"Llave":          {},
	"Contraparte":    {},
}

func TestElClasificadorNoImportaRedNiRelojNiRepositorio(t *testing.T) {
	fset := token.NewFileSet()
	for _, archivo := range archivosDelClasificador {
		ruta := filepath.Join(".", archivo)
		declaracion, err := parser.ParseFile(fset, ruta, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsear %s: %v", archivo, err)
		}
		for _, importado := range declaracion.Imports {
			via, err := strconv.Unquote(importado.Path.Value)
			if err != nil {
				t.Fatalf("import ilegible en %s: %v", archivo, err)
			}
			if _, permitido := importsPermitidos[via]; !permitido {
				t.Fatalf("%s importa %q, que no está en la lista blanca del clasificador", archivo, via)
			}
		}
	}
}

func TestElClasificadorNoNombraMontoNiIdentidad(t *testing.T) {
	fset := token.NewFileSet()
	for _, archivo := range archivosDelClasificador {
		ruta := filepath.Join(".", archivo)
		declaracion, err := parser.ParseFile(fset, ruta, nil, 0)
		if err != nil {
			t.Fatalf("parsear %s: %v", archivo, err)
		}
		ast.Inspect(declaracion, func(nodo ast.Node) bool {
			identificador, ok := nodo.(*ast.Ident)
			if !ok {
				return true
			}
			if _, prohibido := identificadoresProhibidos[identificador.Name]; prohibido {
				t.Fatalf("%s usa el identificador %q", archivo, identificador.Name)
			}
			return true
		})
	}
}

func TestPredecirSoloRecibeNombreYCategoriaDeMCC(t *testing.T) {
	fset := token.NewFileSet()
	declaracion, err := parser.ParseFile(fset, "clasificador.go", nil, 0)
	if err != nil {
		t.Fatalf("parsear clasificador.go: %v", err)
	}
	var funcion *ast.FuncDecl
	for _, nodo := range declaracion.Decls {
		declarada, ok := nodo.(*ast.FuncDecl)
		if !ok || declarada.Name.Name != "Predecir" {
			continue
		}
		funcion = declarada
	}
	if funcion == nil {
		t.Fatal("no se encontró Predecir en clasificador.go")
	}
	parametros := funcion.Type.Params.List
	if len(parametros) != 2 {
		t.Fatalf("Predecir tiene %d grupos de parámetros, want 2", len(parametros))
	}
	primero, ok := parametros[0].Type.(*ast.StarExpr)
	if !ok || len(parametros[0].Names) != 1 {
		t.Fatalf("el primer parámetro no es un puntero único")
	}
	modelo, ok := primero.X.(*ast.Ident)
	if !ok || modelo.Name != "Modelo" {
		t.Fatalf("el primer parámetro no es *Modelo")
	}
	segundo, ok := parametros[1].Type.(*ast.Ident)
	if !ok || segundo.Name != "string" || len(parametros[1].Names) != 2 {
		t.Fatalf("los dos últimos parámetros deben ser dos string")
	}
	if parametros[1].Names[0].Name != "nombre" || parametros[1].Names[1].Name != "mccCategoria" {
		t.Fatalf("los parámetros son %v, want nombre y mccCategoria", parametros[1].Names)
	}
}
