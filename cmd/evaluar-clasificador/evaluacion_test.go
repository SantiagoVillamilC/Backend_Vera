package main

import (
	"math"
	"strings"
	"testing"
)

// decisorDeTabla devuelve un decisor que responde según el nombre de la fila.
func decisorDeTabla(respuestas map[string]DecisionDeOro) Decisor {
	return func(fila Fila) DecisionDeOro { return respuestas[fila.ID] }
}

func filaDePrueba(id, oro string) Fila {
	return Fila{ID: id, Nombre: "NOMBRE " + id, Oro: oro, Parte: "prueba", Estrato: "alimentos"}
}

// TestEvaluarCuentaAciertosYAbstenciones fija el núcleo del conteo.
func TestEvaluarCuentaAciertosYAbstenciones(t *testing.T) {
	t.Parallel()
	filas := []Fila{
		filaDePrueba("a", "compras"),
		filaDePrueba("b", "compras"),
		filaDePrueba("c", "salud"),
		filaDePrueba("d", "transporte"),
		{ID: "e", Nombre: "OTRO", Oro: "no_se", Parte: "prueba"},
	}
	decisiones := map[string]DecisionDeOro{
		"a": {Categoria: "compras"},
		"b": {Categoria: "salud"},
		"c": {},
		"d": {Abstuvo: true},
	}

	informe, err := Evaluar("prueba", filas, decisorDeTabla(decisiones), 0)
	if err != nil {
		t.Fatalf("Evaluar: %v", err)
	}
	if informe.Total.Filas != 5 || informe.Total.ConEtiqueta != 4 || informe.Total.SinEtiqueta != 1 {
		t.Fatalf("conteo de filas = %+v", informe.Total)
	}
	if informe.Total.Mostradas != 2 || informe.Total.Correctas != 1 {
		t.Fatalf("mostradas y correctas = %+v", informe.Total)
	}
	if informe.Total.Abstenciones != 1 || informe.Total.SinDecision != 1 {
		t.Fatalf("abstenciones y sin decisión = %+v", informe.Total)
	}
	if math.Abs(informe.Total.Precision-0.5) > 1e-9 {
		t.Fatalf("precisión = %v, quiero 0.5", informe.Total.Precision)
	}
	if math.Abs(informe.Total.Cobertura-0.5) > 1e-9 {
		t.Fatalf("cobertura = %v, quiero 0.5", informe.Total.Cobertura)
	}
}

// TestEvaluarFallaConCategoriaFueraDelCatalogo cierra la puerta a una hoja mal etiquetada.
func TestEvaluarFallaConCategoriaFueraDelCatalogo(t *testing.T) {
	t.Parallel()
	casos := map[string]string{
		"ingreso":       "salario",
		"inventada":     "delivery",
		"sin_categoria": "sin_categoria",
		"vacía":         "",
	}
	for nombre, oro := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			filas := []Fila{filaDePrueba("a", oro)}
			if _, err := Evaluar("prueba", filas, decisorDeTabla(nil), 0); err == nil {
				t.Fatalf("se esperaba un error con la etiqueta %q", oro)
			}
		})
	}
}

// TestEvaluarPersonaMostradaCuentaElRiesgo mide cuántas veces el clasificador decide sobre un nombre
// que una persona marcó como nombre propio.
func TestEvaluarPersonaMostradaCuentaElRiesgo(t *testing.T) {
	t.Parallel()
	filas := []Fila{
		{ID: "a", Nombre: "JUAN PEREZ", Oro: "otros_gastos", ParecePersona: true},
		{ID: "b", Nombre: "PANADERIA", Oro: "alimentos_mercado"},
	}
	decisiones := map[string]DecisionDeOro{
		"a": {Categoria: "otros_gastos"},
		"b": {Categoria: "alimentos_mercado"},
	}
	informe, err := Evaluar("prueba", filas, decisorDeTabla(decisiones), 0)
	if err != nil {
		t.Fatalf("Evaluar: %v", err)
	}
	if informe.Total.PersonaMostrada != 1 {
		t.Fatalf("persona_mostrada = %d, quiero 1", informe.Total.PersonaMostrada)
	}
}

// TestEvaluarUmbralNoSeCumpleDejaElCumpleEnFalso es la compuerta que se lee al decidir la bandera.
func TestEvaluarUmbralNoSeCumpleDejaElCumpleEnFalso(t *testing.T) {
	t.Parallel()
	filas := []Fila{filaDePrueba("a", "compras"), filaDePrueba("b", "compras")}
	decisiones := map[string]DecisionDeOro{
		"a": {Categoria: "compras"},
		"b": {Categoria: "salud"},
	}
	informe, err := Evaluar("prueba", filas, decisorDeTabla(decisiones), 0.90)
	if err != nil {
		t.Fatalf("Evaluar: %v", err)
	}
	if informe.Cumple {
		t.Fatalf("precisión %.2f no puede cumplir el umbral 0.90", informe.Total.Precision)
	}
	if informe.Umbral != 0.90 {
		t.Fatalf("umbral = %v", informe.Umbral)
	}
}

// TestEvaluarAgrupaPorParteYSinVacios exige que las agrupaciones no inventen grupos vacíos.
func TestEvaluarAgrupaPorParteYSinVacios(t *testing.T) {
	t.Parallel()
	filas := []Fila{
		{ID: "a", Nombre: "UNO", Oro: "compras", Parte: "desarrollo", Estrato: "ocio"},
		{ID: "b", Nombre: "DOS", Oro: "compras", Parte: "prueba"},
	}
	decisiones := map[string]DecisionDeOro{"a": {Categoria: "compras"}, "b": {Categoria: "compras"}}
	informe, err := Evaluar("prueba", filas, decisorDeTabla(decisiones), 0)
	if err != nil {
		t.Fatalf("Evaluar: %v", err)
	}
	if len(informe.PorParte) != 2 {
		t.Fatalf("partes = %+v, quiero desarrollo y prueba", informe.PorParte)
	}
	if len(informe.PorEstrato) != 1 || informe.PorEstrato[0].Nombre != "ocio" {
		t.Fatalf("estratos = %+v, quiero solo ocio", informe.PorEstrato)
	}
}

// TestErroresOrdenaPorFrecuencia explica los fallos con los pares más repetidos.
func TestErroresOrdenaPorFrecuencia(t *testing.T) {
	t.Parallel()
	filas := []Fila{
		filaDePrueba("a", "otros_gastos"), filaDePrueba("b", "otros_gastos"),
		filaDePrueba("c", "compras"),
	}
	decisiones := map[string]DecisionDeOro{
		"a": {Categoria: "salud"}, "b": {Categoria: "salud"}, "c": {Categoria: "salud"},
	}
	informe, err := Evaluar("prueba", filas, decisorDeTabla(decisiones), 0)
	if err != nil {
		t.Fatalf("Evaluar: %v", err)
	}
	if len(informe.Errores) != 2 {
		t.Fatalf("errores = %+v, quiero dos pares", informe.Errores)
	}
	if informe.Errores[0].Oro != "otros_gastos" || informe.Errores[0].Veces != 2 {
		t.Fatalf("primer error = %+v, quiero otros_gastos dos veces", informe.Errores[0])
	}
}

// TestWilsonCoincideConLaTablaDelPreRegistro compara con la cifra ya publicada en SUPUESTOS.
func TestWilsonCoincideConLaTablaDelPreRegistro(t *testing.T) {
	t.Parallel()
	casos := []struct {
		correctas, total int
		bajo, alto       float64
	}{
		{105, 119, 0.812, 0.929},
		{34, 53, 0.507, 0.757},
		{0, 0, 0, 0},
		{10, 10, 0.722, 1},
		{0, 10, 0, 0.278},
	}
	for _, caso := range casos {
		bajo, alto := Wilson(caso.correctas, caso.total)
		if math.Abs(bajo-caso.bajo) > 1e-3 || math.Abs(alto-caso.alto) > 1e-3 {
			t.Fatalf("Wilson(%d,%d) = (%.3f, %.3f), quiero (%.3f, %.3f)",
				caso.correctas, caso.total, bajo, alto, caso.bajo, caso.alto)
		}
	}
}

// TestLeerHojaAceptaComillasYBOM comprueba que un nombre con coma no rompe la hoja.
func TestLeerHojaAceptaComillasYBOM(t *testing.T) {
	t.Parallel()
	crudo := "\ufeff" + `id_oro,nombre_qr,categoria,parece_persona
1,"TIENDA, LA 07",compras,no
2,PANADERIA NORTE 12,alimentos_mercado,si
3,SIN NOMBRE,no_se,
`
	cabecera, registros, err := leerFilas([]byte(crudo))
	if err != nil {
		t.Fatalf("leerFilas: %v", err)
	}
	indice := indiceDeColumnas(cabecera)
	if indice["id_oro"] != 0 || indice["categoria"] != 2 {
		t.Fatalf("índice = %v", indice)
	}
	if len(registros) != 3 {
		t.Fatalf("registros = %d, quiero 3", len(registros))
	}
	if nombre := campo(registros[0], indice, "nombre_qr"); nombre != "TIENDA, LA 07" {
		t.Fatalf("nombre = %q, quiero el de la coma", nombre)
	}
	if !esSi(campo(registros[1], indice, "parece_persona")) {
		t.Fatal("parece_persona si debía leerse como verdadero")
	}
	if esSi(campo(registros[0], indice, "parece_persona")) {
		t.Fatal("parece_persona no debía leerse como verdadero")
	}
}

// TestModoDeRechazaLoQueNoSeMide deja explícito que memoria, MCC y catálogo no entran acá.
func TestModoDeRechazaLoQueNoSeMide(t *testing.T) {
	t.Parallel()
	if _, err := modoDe("memoria"); err == nil {
		t.Fatal("el modo memoria no existe")
	}
	if _, err := modoDe(ModoPrimerCorte); err != nil {
		t.Fatalf("primer-corte debe ser válido: %v", err)
	}
}

// TestEscalonesDeCadaModo fija qué escalón mide cada modo.
func TestEscalonesDeCadaModo(t *testing.T) {
	t.Parallel()
	escalones, err := escalonesDe(ModoClasificador)
	if err != nil {
		t.Fatalf("escalonesDe: %v", err)
	}
	if !escalones.Contiene(6) || len(escalones) != 1 {
		t.Fatalf("escalones = %v, quiero solo el 6", escalones)
	}
	todos, err := escalonesDe(ModoTodos)
	if err != nil {
		t.Fatalf("escalonesDe: %v", err)
	}
	if len(todos) < 6 {
		t.Fatalf("el modo todos debe encender los seis escalones: %v", todos)
	}
	if _, err := escalonesDe("memoria"); err == nil || !strings.Contains(err.Error(), "inválido") {
		t.Fatalf("error = %v, quiero inválido", err)
	}
}
