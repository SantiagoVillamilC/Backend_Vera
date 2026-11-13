package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDirectorioResuelveElComercioPorLlave(t *testing.T) {
	t.Parallel()

	directorio := NuevoDirectorio(map[string]EntradaDirectorio{
		"celular:3001234567": {Entidad: "Banco Vecino", Producto: "Cuenta de ahorros"},
		"comercio:3001234567": {
			Entidad:        "Banco Vecino",
			Producto:       "Recaudo",
			NombreComercio: "Tienda del barrio",
			MCC:            "5411",
		},
	})
	entrada, ok := directorio.PorLlave("3001234567")
	if !ok {
		t.Fatal("no resolvió la llave registrada")
	}
	if entrada.NombreComercio != "Tienda del barrio" || entrada.MCC != "5411" {
		t.Fatalf("entrada = %+v, quiero el comercio con MCC 5411", entrada)
	}
}

func TestDirectorioRechazaMCCQueNoTieneCuatroDigitos(t *testing.T) {
	t.Setenv("SIMULADOR_RIEL_DIRECTORIO", `{"comercio:3001234567":{"entidad":"Banco","producto":"Recaudo","mcc":"541"}}`)
	if _, fallos := leerDirectorioDelEntorno(); len(fallos) == 0 {
		t.Fatal("aceptó un MCC de tres dígitos")
	}
}

func TestEnvioConfirmadoIncluyeElComercio(t *testing.T) {
	t.Parallel()

	servidor := servidorConDirectorio(t, map[string]EntradaDirectorio{
		"comercio:3001234567": {
			Entidad:        "Banco Vecino",
			Producto:       "Recaudo",
			NombreComercio: "Tienda del barrio",
			MCC:            "5411",
		},
	})
	status, cuerpo := enviar(t, servidor.Client(), servidor.URL, ordenJSON("id-comercio"))
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (%s)", status, cuerpo)
	}
	var respuesta struct {
		ComercioNombre string `json:"comercio_nombre"`
		MCC            string `json:"mcc"`
	}
	if err := json.Unmarshal([]byte(cuerpo), &respuesta); err != nil {
		t.Fatalf("cuerpo no es JSON: %v (%q)", err, cuerpo)
	}
	if respuesta.ComercioNombre != "Tienda del barrio" || respuesta.MCC != "5411" {
		t.Fatalf("respuesta = %+v, quiero comercio y MCC", respuesta)
	}
}
