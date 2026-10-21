package inteligencia

import "testing"

func TestPersistirDestino(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		destino   Destino
		esperado  DestinoPersistido
		conClaves bool
	}{
		"comercio con llave, mid y nombre": {
			destino: Destino{Tipo: DestinoComercio, Llave: "C1", MID: "M1", Nombre: "Comercio Uno"},
			esperado: DestinoPersistido{
				Tipo: DestinoComercio, Llave: "C1", MID: "M1", NombreNormalizado: "COMERCIO-UNO",
			},
			conClaves: true,
		},
		"comercio solo con nombre": {
			destino:  Destino{Tipo: DestinoComercio, Nombre: "Tienda  Dos"},
			esperado: DestinoPersistido{Tipo: DestinoComercio, NombreNormalizado: "TIENDA-DOS"},
		},
		"persona solo con llave": {
			destino:   Destino{Tipo: DestinoPersona, Llave: "P1", Nombre: "Persona Uno"},
			esperado:  DestinoPersistido{Tipo: DestinoPersona, Llave: "P1"},
			conClaves: true,
		},
		"destino sin claves": {
			destino:  Destino{Tipo: DestinoComercio, Nombre: "..."},
			esperado: DestinoPersistido{Tipo: DestinoComercio},
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			persistido := PersistirDestino("u1", caso.destino)
			if persistido != caso.esperado {
				t.Fatalf("PersistirDestino = %+v, want %+v", persistido, caso.esperado)
			}
			_, ok := persistido.Destino()
			if ok != caso.conClaves && caso.esperado.Llave != "" {
				t.Errorf("Destino() = %v, want %v", ok, caso.conClaves)
			}
		})
	}
}

func TestDestinoDeLaCopia(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		persistido DestinoPersistido
		ok         bool
	}{
		"comercio completo": {
			persistido: DestinoPersistido{Tipo: DestinoComercio, MID: "M1", NombreNormalizado: "COMERCIO-UNO"},
			ok:         true,
		},
		"persona con llave": {
			persistido: DestinoPersistido{Tipo: DestinoPersona, Llave: "P1"},
			ok:         true,
		},
		"sin tipo":          {persistido: DestinoPersistido{Llave: "P1"}},
		"tipo desconocido":  {persistido: DestinoPersistido{Tipo: "otro", Llave: "P1"}},
		"sin ninguna clave": {persistido: DestinoPersistido{Tipo: DestinoComercio}},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			destino, ok := caso.persistido.Destino()
			if ok != caso.ok {
				t.Fatalf("ok = %v, want %v: %+v", ok, caso.ok, destino)
			}
		})
	}
}

func TestEsDestinoGenerico(t *testing.T) {
	t.Parallel()

	for _, texto := range []string{
		textoUsuarioDeLaBilletera, textoComercioExterno, textoMovimiento,
		textoBancoCustodio, textoGMF, textoComision,
	} {
		if !EsDestinoGenerico(texto) {
			t.Errorf("%q no se reconoce como genérico", texto)
		}
	}
	for _, texto := range []string{"Mercado Central", "Persona Receptora", ""} {
		if EsDestinoGenerico(texto) {
			t.Errorf("%q se reconoce como genérico", texto)
		}
	}
}
