package inteligencia

import (
	"errors"
	"testing"
)

func TestClavesDeComercioYPersona(t *testing.T) {
	comercio, err := ClavesDe("u1", Destino{Tipo: DestinoComercio, Llave: "L", MID: "M", Nombre: "Comercio  Uno"})
	if err != nil {
		t.Fatalf("ClavesDe comercio: %v", err)
	}
	if len(comercio) != 3 {
		t.Fatalf("claves de comercio = %d, want 3", len(comercio))
	}
	if comercio[0].Nivel != NivelLlave || comercio[1].Nivel != NivelMID || comercio[2].Nivel != NivelNombre {
		t.Errorf("orden de niveles = %v", []string{comercio[0].Nivel, comercio[1].Nivel, comercio[2].Nivel})
	}
	if comercio[2].Valor != "COMERCIO-UNO" {
		t.Errorf("nombre normalizado = %q", comercio[2].Valor)
	}

	persona, err := ClavesDe("u1", Destino{Tipo: DestinoPersona, Llave: "P", Nombre: "Persona Uno"})
	if err != nil {
		t.Fatalf("ClavesDe persona: %v", err)
	}
	if len(persona) != 1 || persona[0].Nivel != NivelLlave {
		t.Errorf("claves de persona = %+v, want solo la llave", persona)
	}
}

func TestClavesDeSinDestinoFalla(t *testing.T) {
	if _, err := ClavesDe("u1", Destino{Tipo: DestinoPersona}); !errors.Is(err, ErrDestinoSinClave) {
		t.Fatalf("error = %v, want ErrDestinoSinClave", err)
	}
}

func TestCorregirEscribeTodasLasClaves(t *testing.T) {
	destino := Destino{Tipo: DestinoComercio, Llave: "L", MID: "M", Nombre: "Comercio Uno"}
	memoria, err := Corregir(Memoria{}, "u1", destino, CategoriaCompras)
	if err != nil {
		t.Fatalf("Corregir: %v", err)
	}
	if len(memoria) != 3 {
		t.Fatalf("memoria = %d claves, want 3", len(memoria))
	}
	if categoria, encontrada := Consultar(memoria, "u1", destino); !encontrada || categoria != CategoriaCompras {
		t.Errorf("Consultar = %q, %v", categoria, encontrada)
	}
	if _, encontrada := Consultar(memoria, "u2", destino); encontrada {
		t.Error("la corrección se heredó a otro usuario")
	}
}

func TestCorregirRechazaCategoriaNoVisible(t *testing.T) {
	destino := Destino{Tipo: DestinoComercio, Llave: "L"}
	if _, err := Corregir(Memoria{}, "u1", destino, SinCategoria); !errors.Is(err, ErrCategoriaNoVisible) {
		t.Errorf("sin_categoria: error = %v, want ErrCategoriaNoVisible", err)
	}
	if _, err := Corregir(Memoria{}, "u1", destino, Categoria("inventada")); !errors.Is(err, ErrCategoriaNoVisible) {
		t.Errorf("inventada: error = %v, want ErrCategoriaNoVisible", err)
	}
}

func TestConsultarRespetaLaPrioridadYOmiteInvalidas(t *testing.T) {
	destino := Destino{Tipo: DestinoComercio, Llave: "L", MID: "M", Nombre: "Comercio Uno"}
	memoria := Memoria{
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelLlave, Valor: "L"}:             Correccion{Categoria: Categoria("inventada")},
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelMID, Valor: "M"}:               Correccion{Categoria: CategoriaCompras},
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelNombre, Valor: "COMERCIO-UNO"}: Correccion{Categoria: CategoriaSalud},
	}
	categoria, encontrada := Consultar(memoria, "u1", destino)
	if !encontrada || categoria != CategoriaCompras {
		t.Errorf("Consultar = %q, %v, want compras", categoria, encontrada)
	}
}

func TestProyectarAplanaLaMemoria(t *testing.T) {
	destino := Destino{Tipo: DestinoComercio, Llave: "L", MID: "M", Nombre: "Comercio Uno"}
	memoria := Memoria{
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelMID, Valor: "M"}:               Correccion{Categoria: CategoriaCompras},
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelNombre, Valor: "COMERCIO-UNO"}: Correccion{Categoria: CategoriaSalud},
	}
	plana := Proyectar(memoria, "u1", destino)
	if plana["M"] != CategoriaCompras || plana["COMERCIO-UNO"] != CategoriaSalud {
		t.Errorf("proyección = %v", plana)
	}
	if _, existe := plana["L"]; existe {
		t.Errorf("proyección incluye una llave sin corrección: %v", plana)
	}
}

func TestCorregirConservaElNombreYNombrarConservaLaCategoria(t *testing.T) {
	t.Parallel()

	destino := Destino{Tipo: DestinoComercio, Llave: "L", Nombre: "Comercio Uno"}
	conNombre, err := Nombrar(Memoria{}, "u1", destino, "Mercado Central")
	if err != nil {
		t.Fatalf("Nombrar: %v", err)
	}
	if nombre, encontrado := NombreDe(conNombre, "u1", destino); !encontrado || nombre != "Mercado Central" {
		t.Fatalf("NombreDe = %q, %v", nombre, encontrado)
	}

	conCategoria, err := Corregir(conNombre, "u1", destino, CategoriaCompras)
	if err != nil {
		t.Fatalf("Corregir: %v", err)
	}
	if nombre, _ := NombreDe(conCategoria, "u1", destino); nombre != "Mercado Central" {
		t.Errorf("Corregir borró el nombre: %q", nombre)
	}
	if categoria, _ := Consultar(conCategoria, "u1", destino); categoria != CategoriaCompras {
		t.Errorf("categoría = %q, want compras", categoria)
	}

	renombrada, err := Nombrar(conCategoria, "u1", destino, "Mercado Nuevo")
	if err != nil {
		t.Fatalf("Nombrar de nuevo: %v", err)
	}
	if categoria, _ := Consultar(renombrada, "u1", destino); categoria != CategoriaCompras {
		t.Errorf("Nombrar borró la categoría: %q", categoria)
	}
	if categoria, _ := Consultar(conCategoria, "u1", destino); categoria != CategoriaCompras {
		t.Error("Nombrar mutó la memoria de entrada")
	}
}

func TestNombreDeRespetaLaPrioridad(t *testing.T) {
	t.Parallel()

	destino := Destino{Tipo: DestinoComercio, Llave: "L", MID: "M", Nombre: "Comercio Uno"}
	memoria := Memoria{
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelLlave, Valor: "L"}:             Correccion{Nombre: "Nombre de la llave"},
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelNombre, Valor: "COMERCIO-UNO"}: Correccion{Nombre: "Nombre del nombre"},
	}
	if nombre, _ := NombreDe(memoria, "u1", destino); nombre != "Nombre de la llave" {
		t.Errorf("NombreDe = %q, want el de la llave", nombre)
	}
	if _, encontrado := NombreDe(memoria, "u2", destino); encontrado {
		t.Error("el nombre se heredó a otro usuario")
	}
}

func TestNombrarSoloComercioYRechazaVacio(t *testing.T) {
	t.Parallel()

	if _, err := Nombrar(Memoria{}, "u1", Destino{Tipo: DestinoPersona, Llave: "P"}, "Nombre"); !errors.Is(err, ErrNombreNoAplica) {
		t.Errorf("persona: error = %v, want ErrNombreNoAplica", err)
	}
	if _, err := Nombrar(Memoria{}, "u1", Destino{Tipo: DestinoComercio, Llave: "L"}, "   "); !errors.Is(err, ErrNombreVacio) {
		t.Errorf("vacío: error = %v, want ErrNombreVacio", err)
	}
}
