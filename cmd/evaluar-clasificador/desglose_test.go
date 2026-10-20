package main

import "testing"

// TestEvaluarDesglosaElLexico fija las tres causas de abstención y los pares que las producen.
func TestEvaluarDesglosaElLexico(t *testing.T) {
	t.Parallel()
	filas := []Fila{
		filaDePrueba("a", "salud"),
		filaDePrueba("b", "compras"),
		filaDePrueba("c", "otros_gastos"),
		filaDePrueba("d", "transporte"),
		filaDePrueba("e", "salud"),
		{ID: "f", Nombre: "SIN ETIQUETA", Oro: "no_se", Parte: "prueba"},
	}
	decisiones := map[string]DecisionDeOro{
		"a": {Categoria: "salud", Lexico: []Coincidencia{{Palabra: "DROGUERIA", Categoria: "salud"}}},
		"b": {Categoria: "salud", Lexico: []Coincidencia{{Palabra: "PANADERIA", Categoria: "alimentos_mercado"}}},
		"c": {Lexico: []Coincidencia{
			{Palabra: "CENTRAL", Categoria: "comisiones_costos_financieros"},
			{Palabra: "HOTEL", Categoria: "otros_gastos"},
		}},
		"d": {Lexico: []Coincidencia{
			{Palabra: "CLINICA", Categoria: "salud"},
			{Palabra: "ROPA", Categoria: "compras"},
		}},
		"e": {Lexico: []Coincidencia{}},
		"f": {Lexico: []Coincidencia{{Palabra: "TIENDA", Categoria: "compras"}}},
	}

	informe, err := Evaluar("lexico", filas, decisorDeTabla(decisiones), 0)
	if err != nil {
		t.Fatalf("Evaluar: %v", err)
	}
	desglose := informe.Lexico
	if desglose == nil {
		t.Fatal("el informe no trae el desglose del léxico")
	}
	if desglose.FilasConEtiqueta != 5 || desglose.UnaCategoria != 2 || desglose.Conflictos != 2 ||
		desglose.SinPalabra != 1 {
		t.Fatalf("conteo = %+v", desglose)
	}
	if desglose.UnaCategoriaCorrecta != 1 || desglose.ConflictosConAcierto != 1 {
		t.Fatalf("aciertos = %+v", desglose)
	}
	if len(desglose.ConflictosPorPalabras) != 2 {
		t.Fatalf("pares = %+v", desglose.ConflictosPorPalabras)
	}
	primero := desglose.ConflictosPorPalabras[0]
	if primero.Palabras != "CENTRAL=comisiones_costos_financieros | HOTEL=otros_gastos" ||
		primero.Veces != 1 || primero.Correctas != 1 {
		t.Fatalf("primer par = %+v", primero)
	}
}

// TestEvaluarSinLexicoNoLoSupone evita que un modo sin escalón 5 reporte un desglose inventado.
func TestEvaluarSinLexicoNoLoSupone(t *testing.T) {
	t.Parallel()
	filas := []Fila{filaDePrueba("a", "salud")}
	decisiones := map[string]DecisionDeOro{"a": {Categoria: "salud"}}
	informe, err := Evaluar("clasificador", filas, decisorDeTabla(decisiones), 0)
	if err != nil {
		t.Fatalf("Evaluar: %v", err)
	}
	if informe.Lexico != nil {
		t.Fatalf("desglose inesperado: %+v", informe.Lexico)
	}
}

// TestFiltrarParteDejaSoloLoPedidoYFallaCerrado evita medir una parte con nombre equivocado.
func TestFiltrarParteDejaSoloLoPedidoYFallaCerrado(t *testing.T) {
	t.Parallel()
	filas := []Fila{
		{ID: "a", Oro: "salud", Parte: "desarrollo"},
		{ID: "b", Oro: "salud", Parte: "prueba"},
		{ID: "c", Oro: "compras", Parte: "desarrollo"},
	}
	filtradas, err := filtrarParte(filas, "desarrollo")
	if err != nil {
		t.Fatalf("filtrarParte: %v", err)
	}
	if len(filtradas) != 2 || filtradas[0].ID != "a" || filtradas[1].ID != "c" {
		t.Fatalf("filtradas = %+v", filtradas)
	}
	if _, err := filtrarParte(filas, "inventada"); err == nil {
		t.Fatal("una parte desconocida debería fallar")
	}
}
