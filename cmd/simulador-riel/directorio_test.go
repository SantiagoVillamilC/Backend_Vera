package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// servidorConDirectorio levanta el simulador con un catálogo de llaves dado.
func servidorConDirectorio(t *testing.T, entradas map[string]EntradaDirectorio) *httptest.Server {
	t.Helper()

	perillas := Perillas{ModoCaida: ModoCaidaNinguna}
	simulador := NuevoSimulador(almacenDePrueba(t), NuevoControl(perillas), NuevoDirectorio(entradas), "simulador-riel")
	servidor := httptest.NewServer(simulador.Rutas())
	t.Cleanup(servidor.Close)
	return servidor
}

// HU-NF-10, criterio 4: el directorio simulado devuelve entidad y producto y nunca una cuenta.
func TestHU_NF_10_Criterio4_DirectorioDevuelveEntidadYProducto(t *testing.T) {
	t.Parallel()

	servidor := servidorConDirectorio(t, map[string]EntradaDirectorio{
		"celular:3001234567": {Entidad: "Banco Vecino", Producto: "Cuenta de ahorros"},
	})
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodGet, servidor.URL+"/resolver?tipo=celular&valor=3001234567", nil)
	if err != nil {
		t.Fatalf("armar la petición: %v", err)
	}
	respuesta, err := servidor.Client().Do(peticion)
	if err != nil {
		t.Fatalf("pedir la resolución: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quiero 200", respuesta.StatusCode)
	}
	datos, err := io.ReadAll(respuesta.Body)
	if err != nil {
		t.Fatalf("leer la respuesta: %v", err)
	}
	var cuerpo map[string]any
	if err := json.Unmarshal(datos, &cuerpo); err != nil {
		t.Fatalf("la respuesta no es JSON: %v", err)
	}
	if cuerpo["entidad"] != "Banco Vecino" || cuerpo["producto"] != "Cuenta de ahorros" {
		t.Errorf("cuerpo = %v, quiero la entidad y el producto", cuerpo)
	}
	if _, hayCuenta := cuerpo["numero_cuenta"]; hayCuenta {
		t.Errorf("la respuesta trae numero_cuenta: %v", cuerpo)
	}
	if len(cuerpo) != 2 {
		t.Errorf("la respuesta trae %d campos, quiero solo entidad y producto: %v", len(cuerpo), cuerpo)
	}
}

// HU-NF-10, criterio 5.1: una llave no registrada responde 404.
func TestHU_NF_10_Criterio5_1_DirectorioLlaveInexistenteEsCuatroCeroCuatro(t *testing.T) {
	t.Parallel()

	servidor := servidorConDirectorio(t, map[string]EntradaDirectorio{
		"celular:3001234567": {Entidad: "Banco Vecino", Producto: "Cuenta de ahorros"},
	})
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodGet, servidor.URL+"/resolver?tipo=celular&valor=3009999999", nil)
	if err != nil {
		t.Fatalf("armar la petición: %v", err)
	}
	respuesta, err := servidor.Client().Do(peticion)
	if err != nil {
		t.Fatalf("pedir la resolución: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, quiero 404", respuesta.StatusCode)
	}
}

// HU-NF-10, criterio 2: una petición sin tipo o sin valor se rechaza con 400.
func TestHU_NF_10_Criterio2_DirectorioSinParametrosEsCuatroCientos(t *testing.T) {
	t.Parallel()

	servidor := servidorConDirectorio(t, nil)
	for _, ruta := range []string{"/resolver", "/resolver?tipo=celular", "/resolver?valor=3001234567"} {
		peticion, err := http.NewRequestWithContext(t.Context(), http.MethodGet, servidor.URL+ruta, nil)
		if err != nil {
			t.Fatalf("armar la petición %q: %v", ruta, err)
		}
		respuesta, err := servidor.Client().Do(peticion)
		if err != nil {
			t.Fatalf("pedir %q: %v", ruta, err)
		}
		_ = respuesta.Body.Close()
		if respuesta.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, quiero 400", ruta, respuesta.StatusCode)
		}
	}
}

// La config del catálogo valida las claves y las entradas incompletas.
func TestLeerDirectorioDelEntorno(t *testing.T) {
	t.Run("vacío es catálogo vacío", func(t *testing.T) {
		t.Setenv("SIMULADOR_RIEL_DIRECTORIO", "")
		entradas, errs := leerDirectorioDelEntorno()
		if len(errs) != 0 || len(entradas) != 0 {
			t.Fatalf("entradas = %v, errs = %v, quiero vacío sin errores", entradas, errs)
		}
	})

	t.Run("entrada válida", func(t *testing.T) {
		t.Setenv("SIMULADOR_RIEL_DIRECTORIO", `{"celular:3001234567":{"entidad":"Banco Vecino","producto":"Cuenta de ahorros"}}`)
		entradas, errs := leerDirectorioDelEntorno()
		if len(errs) != 0 || len(entradas) != 1 {
			t.Fatalf("entradas = %v, errs = %v, quiero una entrada sin errores", entradas, errs)
		}
	})

	t.Run("entrada incompleta", func(t *testing.T) {
		t.Setenv("SIMULADOR_RIEL_DIRECTORIO", `{"sin-separador":{"entidad":"","producto":""}}`)
		if _, errs := leerDirectorioDelEntorno(); len(errs) == 0 {
			t.Fatal("quiero errores para la clave sin tipo:valor y la entrada vacía")
		}
	})

	t.Run("json inválido", func(t *testing.T) {
		t.Setenv("SIMULADOR_RIEL_DIRECTORIO", `{`)
		if _, errs := leerDirectorioDelEntorno(); len(errs) == 0 {
			t.Fatal("quiero error para un JSON inválido")
		}
	})
}
