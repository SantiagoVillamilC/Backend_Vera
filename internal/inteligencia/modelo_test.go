package inteligencia

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func artefactoDePrueba(t *testing.T, version string, idf, umbrales map[string]string,
	centroides map[string]map[string]string, lexico map[string]string) []byte {
	t.Helper()
	return artefactoConProhibidas(t, version, idf, umbrales, centroides, lexico,
		[]string{string(CategoriaComisiones)})
}

func artefactoConProhibidas(t *testing.T, version string, idf, umbrales map[string]string,
	centroides map[string]map[string]string, lexico map[string]string, prohibidas []string) []byte {
	t.Helper()
	documento := map[string]any{
		"version":               version,
		"idf":                   idf,
		"centroides":            centroides,
		"lexico":                lexico,
		"categorias_prohibidas": prohibidas,
		"umbral_coseno":         umbrales["coseno"],
		"umbral_coseno_fuerte":  umbrales["fuerte"],
		"umbral_jw":             umbrales["jw"],
	}
	datos, err := json.Marshal(documento)
	if err != nil {
		t.Fatalf("serializar el artefacto de prueba: %v", err)
	}
	return datos
}

// sinCampo devuelve el artefacto con una clave quitada, para probar los campos obligatorios.
func sinCampo(t *testing.T, datos []byte, campo string) []byte {
	t.Helper()
	var documento map[string]any
	if err := json.Unmarshal(datos, &documento); err != nil {
		t.Fatalf("decodificar el artefacto de prueba: %v", err)
	}
	delete(documento, campo)
	alterado, err := json.Marshal(documento)
	if err != nil {
		t.Fatalf("serializar el artefacto de prueba: %v", err)
	}
	return alterado
}

func umbralesDePrueba() map[string]string {
	return map[string]string{
		"coseno": "0.1",
		"fuerte": "0.5",
		"jw":     "0.85",
	}
}

func TestCargarModeloRechazaArtefactoInvalido(t *testing.T) {
	casos := map[string][]byte{
		"json truncado":      []byte(`{"version":"tfidf-1"`),
		"idf vacio":          artefactoDePrueba(t, VersionArtefacto, map[string]string{}, umbralesDePrueba(), map[string]map[string]string{"compras": {"w:A": "1"}}, nil),
		"peso no numerico":   artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "uno"}, umbralesDePrueba(), map[string]map[string]string{"compras": {"w:A": "1"}}, nil),
		"peso con coma":      artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1,5"}, umbralesDePrueba(), map[string]map[string]string{"compras": {"w:A": "1"}}, nil),
		"peso NaN":           artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "NaN"}, umbralesDePrueba(), map[string]map[string]string{"compras": {"w:A": "1"}}, nil),
		"peso Inf":           artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "+Inf"}, umbralesDePrueba(), map[string]map[string]string{"compras": {"w:A": "1"}}, nil),
		"centroide vacio":    artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"}, umbralesDePrueba(), map[string]map[string]string{"compras": {}}, nil),
		"umbral negativo":    artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"}, map[string]string{"coseno": "-0.1", "fuerte": "0.5", "jw": "0.85"}, map[string]map[string]string{"compras": {"w:A": "1"}}, nil),
		"umbral invertido":   artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"}, map[string]string{"coseno": "0.6", "fuerte": "0.5", "jw": "0.85"}, map[string]map[string]string{"compras": {"w:A": "1"}}, nil),
		"umbral no numerico": artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"}, map[string]string{"coseno": "débil", "fuerte": "0.5", "jw": "0.85"}, map[string]map[string]string{"compras": {"w:A": "1"}}, nil),
	}
	for nombre, datos := range casos {
		t.Run(nombre, func(t *testing.T) {
			if _, err := CargarModelo(datos); !errors.Is(err, ErrArtefactoInvalido) {
				t.Fatalf("err = %v, want ErrArtefactoInvalido", err)
			}
		})
	}
}

func TestCargarModeloRechazaVersionDesconocida(t *testing.T) {
	for _, version := range []string{"", "tfidf-3"} {
		t.Run("version="+version, func(t *testing.T) {
			datos := artefactoDePrueba(t, version, map[string]string{"w:A": "1"}, umbralesDePrueba(),
				map[string]map[string]string{"compras": {"w:A": "1"}}, nil)
			if _, err := CargarModelo(datos); !errors.Is(err, ErrVersionDesconocida) {
				t.Fatalf("err = %v, want ErrVersionDesconocida", err)
			}
		})
	}
}

func TestCargarModeloRechazaCategoriaFueraDelCatalogo(t *testing.T) {
	casos := map[string]map[string]map[string]string{
		"inventada":     {"categoria_inventada": {"w:A": "1"}},
		"sin_categoria": {"sin_categoria": {"w:A": "1"}},
	}
	for nombre, centroides := range casos {
		t.Run(nombre, func(t *testing.T) {
			datos := artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"}, umbralesDePrueba(), centroides, nil)
			if _, err := CargarModelo(datos); !errors.Is(err, ErrCategoriaDesconocida) {
				t.Fatalf("err = %v, want ErrCategoriaDesconocida", err)
			}
		})
	}
}

func TestCargarModeloRechazaLexicoFueraDeLosCentroides(t *testing.T) {
	datos := artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:A": "1"}}, map[string]string{"FARMACIA": "salud"})
	if _, err := CargarModelo(datos); !errors.Is(err, ErrCategoriaDesconocida) {
		t.Fatalf("err = %v, want ErrCategoriaDesconocida", err)
	}
}

func TestCargarModeloCalculaLaHuellaYLaVersion(t *testing.T) {
	datos := artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:A": "1"}}, nil)
	modelo, err := CargarModelo(datos)
	if err != nil {
		t.Fatalf("cargar: %v", err)
	}
	if len(modelo.Huella) != 64 {
		t.Fatalf("huella = %q, want 64 hex", modelo.Huella)
	}
	esperada := "tfidf:" + VersionArtefacto + ":" + modelo.Huella[:LargoHuellaCorta]
	if modelo.VersionModelo() != esperada {
		t.Fatalf("VersionModelo = %q, want %q", modelo.VersionModelo(), esperada)
	}
	if err := VerificarHuella(modelo, modelo.Huella); err != nil {
		t.Fatalf("verificar la huella correcta: %v", err)
	}
}

func TestVerificarHuellaRechazaUnaDistinta(t *testing.T) {
	datos := artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:A": "1"}}, nil)
	modelo, err := CargarModelo(datos)
	if err != nil {
		t.Fatalf("cargar: %v", err)
	}
	for _, esperada := range []string{"", strings.Repeat("0", 64)} {
		if err := VerificarHuella(modelo, esperada); !errors.Is(err, ErrHuellaDistinta) {
			t.Fatalf("huella %q: err = %v, want ErrHuellaDistinta", esperada, err)
		}
	}
}

func TestUmbralesDelModeloSalenDelArtefacto(t *testing.T) {
	datos := artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:A": "1"},
		map[string]string{"coseno": "0.2", "fuerte": "0.7", "jw": "0.9"},
		map[string]map[string]string{"compras": {"w:A": "1"}}, nil)
	modelo, err := CargarModelo(datos)
	if err != nil {
		t.Fatalf("cargar: %v", err)
	}
	if math.Abs(modelo.UmbralCoseno-0.2) > 1e-12 ||
		math.Abs(modelo.UmbralCosenoFuerte-0.7) > 1e-12 ||
		math.Abs(modelo.UmbralJW-0.9) > 1e-12 {
		t.Fatalf("umbrales = %v %v %v", modelo.UmbralCoseno, modelo.UmbralCosenoFuerte, modelo.UmbralJW)
	}
}

func exigirRechazoDeModelo(t *testing.T, datos []byte, esperado error) {
	t.Helper()
	if _, err := CargarModelo(datos); !errors.Is(err, esperado) {
		t.Fatalf("err = %v, want %v", err, esperado)
	}
}

func artefactoBase(t *testing.T) []byte {
	t.Helper()
	return artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:ALFA": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil)
}

func TestCargarModeloRechazaCampoDesconocido(t *testing.T) {
	datos := artefactoBase(t)
	var documento map[string]any
	if err := json.Unmarshal(datos, &documento); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	documento["campo_desconocido"] = "x"
	alterado, err := json.Marshal(documento)
	if err != nil {
		t.Fatalf("serializar: %v", err)
	}
	exigirRechazoDeModelo(t, alterado, ErrArtefactoInvalido)
}

func TestCargarModeloRechazaSinCategoriasProhibidas(t *testing.T) {
	exigirRechazoDeModelo(t, sinCampo(t, artefactoBase(t), "categorias_prohibidas"), ErrArtefactoInvalido)
}

func TestCargarModeloRechazaProhibidaFueraDelCatalogo(t *testing.T) {
	datos := artefactoConProhibidas(t, VersionArtefacto, map[string]string{"w:ALFA": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil, []string{"categoria_inventada"})
	exigirRechazoDeModelo(t, datos, ErrCategoriaDesconocida)
}

func TestCargarModeloRechazaProhibidaDuplicada(t *testing.T) {
	datos := artefactoConProhibidas(t, VersionArtefacto, map[string]string{"w:ALFA": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil,
		[]string{string(CategoriaComisiones), string(CategoriaComisiones)})
	exigirRechazoDeModelo(t, datos, ErrArtefactoInvalido)
}

func TestCargarModeloRechazaProhibidasDesordenadas(t *testing.T) {
	datos := artefactoConProhibidas(t, VersionArtefacto, map[string]string{"w:ALFA": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil,
		[]string{string(CategoriaOtrosGastos), string(CategoriaComisiones)})
	exigirRechazoDeModelo(t, datos, ErrArtefactoInvalido)
}

func TestCargarModeloRechazaProhibidaDeIngreso(t *testing.T) {
	datos := artefactoConProhibidas(t, VersionArtefacto, map[string]string{"w:ALFA": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil, []string{string(CategoriaSalario)})
	exigirRechazoDeModelo(t, datos, ErrArtefactoInvalido)
}

func TestCargarModeloRechazaTodasProhibidas(t *testing.T) {
	datos := artefactoConProhibidas(t, VersionArtefacto, map[string]string{"w:ALFA": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil, []string{string(CategoriaCompras)})
	exigirRechazoDeModelo(t, datos, ErrArtefactoInvalido)
}

func TestCargarModeloRechazaLexicoHaciaProhibida(t *testing.T) {
	datos := artefactoDePrueba(t, VersionArtefacto, map[string]string{"w:ALFA": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:ALFA": "1"}},
		map[string]string{"BANCO": string(CategoriaComisiones)})
	exigirRechazoDeModelo(t, datos, ErrArtefactoInvalido)
}

func TestCargarModeloRechazaVersionTfidf1(t *testing.T) {
	datos := artefactoDePrueba(t, "tfidf-1", map[string]string{"w:ALFA": "1"}, umbralesDePrueba(),
		map[string]map[string]string{"compras": {"w:ALFA": "1"}}, nil)
	exigirRechazoDeModelo(t, datos, ErrVersionDesconocida)
}

func TestCargarModeloRechazaDatosDespuesDelJSON(t *testing.T) {
	datos := append(artefactoBase(t), []byte(`{"version":"otra"}`)...)
	exigirRechazoDeModelo(t, datos, ErrArtefactoInvalido)
}
