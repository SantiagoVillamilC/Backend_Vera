package copilot

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
)

// ArgumentosHerramienta es un contrato interno, no un DTO HTTP. La identidad
// y la conversación nunca se toman de argumentos producidos por el modelo.
type ArgumentosHerramienta struct {
	Desde           string `json:"desde"`
	Hasta           string `json:"hasta"`
	Tipo            string `json:"tipo"`
	Limite          int    `json:"limite"`
	AgruparPor      string `json:"agrupar_por"`
	Celular         string `json:"celular"`
	Nombre          string `json:"nombre"`
	TransferenciaID string `json:"transferencia_id"`
	DestinatarioRef string `json:"destinatario_ref"`
	LlaveBreB       string `json:"llave_breb"`
	MontoPesos      string `json:"monto_pesos"`
	// Monto se deriva de MontoPesos con aritmética entera, jamás del JSON.
	Monto nucleo.Centavos `json:"-"`
}

func validarArgumentos(nombre string, esquema *openapi3.Schema, raw json.RawMessage) (ArgumentosHerramienta, map[string]any, error) {
	var args ArgumentosHerramienta
	campos, err := objetoEstricto(raw)
	if err != nil {
		return args, nil, err
	}
	if err := esquema.VisitJSON(campos, openapi3.EnableFormatValidation()); err != nil {
		return args, nil, ErrHerramientaValidacion
	}
	if numero, ok := campos["limite"].(json.Number); ok {
		// El esquema ya restringió este único número JSON a un entero entre 1 y 20.
		v, err := numero.Float64()
		if err != nil {
			return args, nil, ErrHerramientaValidacion
		}
		campos["limite"] = int(v)
	}
	b, err := json.Marshal(campos)
	if err != nil {
		return args, nil, ErrHerramientaValidacion
	}
	if err := json.Unmarshal(b, &args); err != nil {
		return args, nil, ErrHerramientaValidacion
	}
	if nombre == "listar_movimientos" || nombre == "resumir_movimientos" {
		if _, _, err := nucleo.RangoMovimientosColombia(args.Desde, args.Hasta); err != nil {
			return args, nil, ErrHerramientaValidacion
		}
	}
	if nombre == "listar_movimientos" && args.Limite == 0 {
		args.Limite = 20
	}
	if nombre == "consultar_transferencia" {
		id, err := uuid.Parse(args.TransferenciaID)
		if err != nil || id == uuid.Nil {
			return args, nil, ErrHerramientaValidacion
		}
	}
	if args.Celular != "" {
		celular, err := auth.NormalizarCelular(args.Celular)
		if err != nil {
			return args, nil, ErrHerramientaValidacion
		}
		args.Celular = celular
	}
	if nombre == "preparar_transferencia" {
		if args.LlaveBreB != "" {
			if _, err := llaveParaPreparacion(args.LlaveBreB); err != nil {
				return args, nil, ErrHerramientaValidacion
			}
		}
		monto, err := nucleo.ParsearPesosEnteros(args.MontoPesos)
		if err != nil {
			return args, nil, ErrHerramientaValidacion
		}
		args.Monto = monto
	}
	return args, campos, nil
}

// objetoEstricto rechaza JSON repetido, ambiguo, inválido o excesivo antes del esquema.
func objetoEstricto(raw json.RawMessage) (map[string]any, error) {
	if len(raw) > 4096 || !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, ErrHerramientaValidacion
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	inicio, err := d.Token()
	if err != nil || inicio != json.Delim('{') {
		return nil, ErrHerramientaValidacion
	}
	campos := make(map[string]any)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, ErrHerramientaValidacion
		}
		clave, ok := token.(string)
		if !ok {
			return nil, ErrHerramientaValidacion
		}
		if _, existe := campos[clave]; existe {
			return nil, ErrHerramientaValidacion
		}
		var valor any
		if err := d.Decode(&valor); err != nil {
			return nil, ErrHerramientaValidacion
		}
		campos[clave] = valor
	}
	return campos, nil
}
