package inteligencia

// Categoria es el código de una etiqueta del catálogo cerrado.
type Categoria string

// EntradaCatalogo es una fila del catálogo con su nombre visible, su grupo y su orden.
type EntradaCatalogo struct {
	Codigo Categoria
	Nombre string
	Grupo  string
	Orden  int16
}

// Los 14 códigos visibles y la fila de los movimientos sin categoría, firmados en el punto 349 bis.
const (
	CategoriaSalario               Categoria = "salario"
	CategoriaTransferenciaRecibida Categoria = "transferencia_recibida"
	CategoriaReembolso             Categoria = "reembolso"
	CategoriaOtrosIngresos         Categoria = "otros_ingresos"
	CategoriaAlimentosMercado      Categoria = "alimentos_mercado"
	CategoriaRestaurantes          Categoria = "restaurantes_domicilios"
	CategoriaTransporte            Categoria = "transporte"
	CategoriaViviendaServicios     Categoria = "vivienda_servicios"
	CategoriaSalud                 Categoria = "salud"
	CategoriaEducacion             Categoria = "educacion"
	CategoriaEntretenimiento       Categoria = "entretenimiento"
	CategoriaCompras               Categoria = "compras"
	CategoriaComisiones            Categoria = "comisiones_costos_financieros"
	CategoriaOtrosGastos           Categoria = "otros_gastos"
	SinCategoria                   Categoria = "sin_categoria"
)

var catalogoFijo = [...]EntradaCatalogo{
	{Codigo: CategoriaSalario, Nombre: "Salario", Grupo: "ingreso", Orden: 1},
	{Codigo: CategoriaTransferenciaRecibida, Nombre: "Transferencia recibida", Grupo: "ingreso", Orden: 2},
	{Codigo: CategoriaReembolso, Nombre: "Reembolso", Grupo: "ingreso", Orden: 3},
	{Codigo: CategoriaOtrosIngresos, Nombre: "Otros ingresos", Grupo: "ingreso", Orden: 4},
	{Codigo: CategoriaAlimentosMercado, Nombre: "Alimentos y mercado", Grupo: "gasto", Orden: 5},
	{Codigo: CategoriaRestaurantes, Nombre: "Restaurantes y domicilios", Grupo: "gasto", Orden: 6},
	{Codigo: CategoriaTransporte, Nombre: "Transporte", Grupo: "gasto", Orden: 7},
	{Codigo: CategoriaViviendaServicios, Nombre: "Vivienda y servicios", Grupo: "gasto", Orden: 8},
	{Codigo: CategoriaSalud, Nombre: "Salud", Grupo: "gasto", Orden: 9},
	{Codigo: CategoriaEducacion, Nombre: "Educación", Grupo: "gasto", Orden: 10},
	{Codigo: CategoriaEntretenimiento, Nombre: "Entretenimiento", Grupo: "gasto", Orden: 11},
	{Codigo: CategoriaCompras, Nombre: "Compras", Grupo: "gasto", Orden: 12},
	{Codigo: CategoriaComisiones, Nombre: "Comisiones y costos financieros", Grupo: "gasto", Orden: 13},
	{Codigo: CategoriaOtrosGastos, Nombre: "Otros gastos", Grupo: "gasto", Orden: 14},
	{Codigo: SinCategoria, Nombre: "Sin categoría", Grupo: "ninguno", Orden: 15},
}

var catalogoPorCodigo = func() map[Categoria]EntradaCatalogo {
	indice := make(map[Categoria]EntradaCatalogo, len(catalogoFijo))
	for _, entrada := range catalogoFijo {
		indice[entrada.Codigo] = entrada
	}
	return indice
}()

// CatalogoFijo devuelve una copia del catálogo firmado en el punto 349 bis.
func CatalogoFijo() []EntradaCatalogo {
	copia := make([]EntradaCatalogo, len(catalogoFijo))
	copy(copia, catalogoFijo[:])
	return copia
}

// BuscarCategoria devuelve la entrada del código y si existe.
func BuscarCategoria(codigo Categoria) (EntradaCatalogo, bool) {
	entrada, existe := catalogoPorCodigo[codigo]
	return entrada, existe
}

// EsCategoria informa si el código pertenece al catálogo cerrado, incluida sin_categoria.
func EsCategoria(codigo Categoria) bool {
	_, existe := catalogoPorCodigo[codigo]
	return existe
}

// EsCategoriaVisible informa si el código se puede ofrecer al usuario.
func EsCategoriaVisible(codigo Categoria) bool {
	entrada, existe := catalogoPorCodigo[codigo]
	return existe && entrada.Grupo != "ninguno"
}
