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
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelLlave, Valor: "L"}:             Categoria("inventada"),
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelMID, Valor: "M"}:               CategoriaCompras,
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelNombre, Valor: "COMERCIO-UNO"}: CategoriaSalud,
	}
	categoria, encontrada := Consultar(memoria, "u1", destino)
	if !encontrada || categoria != CategoriaCompras {
		t.Errorf("Consultar = %q, %v, want compras", categoria, encontrada)
	}
}

func TestProyectarAplanaLaMemoria(t *testing.T) {
	destino := Destino{Tipo: DestinoComercio, Llave: "L", MID: "M", Nombre: "Comercio Uno"}
	memoria := Memoria{
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelMID, Valor: "M"}:               CategoriaCompras,
		{Usuario: "u1", TipoDestino: DestinoComercio, Nivel: NivelNombre, Valor: "COMERCIO-UNO"}: CategoriaSalud,
	}
	plana := Proyectar(memoria, "u1", destino)
	if plana["M"] != CategoriaCompras || plana["COMERCIO-UNO"] != CategoriaSalud {
		t.Errorf("proyección = %v", plana)
	}
	if _, existe := plana["L"]; existe {
		t.Errorf("proyección incluye una llave sin corrección: %v", plana)
	}
}
