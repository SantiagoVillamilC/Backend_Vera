package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

var argumentosValidos = map[string]string{
	"consultar_saldo":         `{}`,
	"consultar_limites":       `{}`,
	"listar_movimientos":      `{"desde":"2026-10-01","hasta":"2026-10-17","tipo":"envio","limite":20}`,
	"resumir_movimientos":     `{"desde":"2026-10-01","hasta":"2026-10-17","agrupar_por":"motivo"}`,
	"buscar_destinatario":     `{"nombre":"Laura"}`,
	"consultar_transferencia": `{"transferencia_id":"11111111-1111-4111-8111-111111111111"}`,
	"preparar_transferencia":  `{"destinatario_ref":"referencia_opaca_123","monto_pesos":"50000"}`,
}

func TestValidacionDeCadaFuncion(t *testing.T) {
	for nombre, raw := range argumentosValidos {
		t.Run(nombre, func(t *testing.T) {
			var llamadas int
			c := catalogoPrueba(t, map[string]ManejadorHerramienta{nombre: func(_ context.Context, _ identidad.Identidad, args ArgumentosHerramienta) (DatosHerramienta, error) {
				llamadas++
				if nombre == "preparar_transferencia" && args.Monto != 5000000 {
					t.Fatal("conversión a centavos incorrecta")
				}
				return nil, ErrHerramientaNoDisponible
			}})
			_, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{Nombre: nombre, Argumentos: json.RawMessage(raw)})
			if err != nil || llamadas != 1 {
				t.Fatalf("no despachó argumentos válidos: %v", err)
			}
			invalidos := []string{`null`, `[]`, `1`, `"texto"`, `{} {}`, `{"desconocido":1}`, strings.Repeat(" ", 4097), "{\"x\":\"\xff\"}"}
			for _, campo := range []string{"usuario_id", "parte_id", "sesion_id", "cuenta_origen", "monto", "contexto"} {
				coma := ","
				if raw == `{}` {
					coma = ""
				}
				invalidos = append(invalidos, strings.TrimSuffix(raw, "}")+coma+fmt.Sprintf(`"%s":"otro"}`, campo))
			}
			for _, invalido := range invalidos {
				r, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{Nombre: nombre, Argumentos: json.RawMessage(invalido)})
				if err != nil || codigoResultado(t, r) != "ValidationError" || llamadas != 1 {
					t.Fatalf("aceptó %q: %s %v", invalido, r.Datos, err)
				}
			}
		})
	}
}

func TestRestriccionesDeArgumentos(t *testing.T) {
	c := catalogoPrueba(t, nil)
	casos := map[string][]string{
		"listar_movimientos": {
			`{}`, `{"desde":"2026-02-30","hasta":"2026-03-01"}`, `{"desde":"2026-10-18","hasta":"2026-10-17"}`,
			`{"desde":"2026-10-01","hasta":"2026-10-17","limite":21}`, `{"desde":"2026-10-01","hasta":"2026-10-17","limite":0}`,
			`{"desde":"2026-10-01","hasta":"2026-10-17","limite":1.5}`, `{"desde":"2026-10-01","hasta":"2026-10-17","limite":"20"}`,
			`{"desde":"2026-10-01","hasta":"2026-10-17","tipo":"ajeno"}`,
		},
		"resumir_movimientos":     {`{}`, `{"desde":"2026-10-01","hasta":"2026-10-17","agrupar_por":"usuario"}`, `{"desde":"2026-10-01","hasta":"2026-10-17"}`},
		"buscar_destinatario":     {`{}`, `{"nombre":" "}`, `{"nombre":null}`, `{"nombre":"Laura","celular":"3001234567"}`, `{"celular":"+++++++"}`, `{"celular":"1234567"}`, `{"nombre":"Laura","nombre":"Pedro"}`, `{"nombre":"Laura","\u006eombre":"Pedro"}`},
		"consultar_transferencia": {`{}`, `{"transferencia_id":"x"}`, `{"transferencia_id":"00000000-0000-0000-0000-000000000000"}`},
		"preparar_transferencia": {
			`{}`, `{"monto_pesos":"1"}`, `{"celular":"3001234567"}`,
			`{"celular":"3001234567","destinatario_ref":"referencia_opaca_123","monto_pesos":"1"}`,
			`{"destinatario_ref":"corta","monto_pesos":"1"}`, `{"destinatario_ref":"referencia_opaca_123","monto_pesos":50000}`,
			`{"destinatario_ref":"referencia_opaca_123","monto_pesos":"0"}`, `{"destinatario_ref":"referencia_opaca_123","monto_pesos":"1.5"}`,
			`{"destinatario_ref":"referencia_opaca_123","monto_pesos":"-1"}`, `{"destinatario_ref":"referencia_opaca_123","monto_pesos":"1e3"}`,
			`{"destinatario_ref":"referencia_opaca_123","monto_pesos":"92233720368547759"}`, `{"llave_breb":"   ","monto_pesos":"1"}`,
		},
	}
	for nombre, invalidos := range casos {
		for i, raw := range invalidos {
			t.Run(fmt.Sprintf("%s/%d", nombre, i), func(t *testing.T) {
				r, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{Nombre: nombre, Argumentos: json.RawMessage(raw)})
				if err != nil || codigoResultado(t, r) != "ValidationError" {
					t.Fatalf("aceptó %s: %s %v", raw, r.Datos, err)
				}
			})
		}
	}
}

func TestNormalizacionYBordes(t *testing.T) {
	c := catalogoPrueba(t, nil)
	for _, caso := range []struct {
		nombre, raw string
		limite      int
		monto       int64
		celular     string
	}{
		{"listar_movimientos", `{"desde":"2024-02-29","hasta":"2024-02-29"}`, 20, 0, ""},
		{"listar_movimientos", `{"desde":"2024-02-29","hasta":"2024-02-29","limite":1.0}`, 1, 0, ""},
		{"buscar_destinatario", `{"celular":"300 123 4567"}`, 0, 0, "+573001234567"},
		{"preparar_transferencia", `{"llave_breb":"@destino","monto_pesos":"92233720368547758"}`, 0, 9223372036854775800, ""},
		{"preparar_transferencia", `{"celular":"3001234567","monto_pesos":"1"}`, 0, 100, "+573001234567"},
	} {
		args, _, err := validarArgumentos(caso.nombre, c.funciones[caso.nombre].esquema, json.RawMessage(caso.raw))
		if err != nil || args.Limite != caso.limite || args.Monto.Int64() != caso.monto || args.Celular != caso.celular {
			t.Fatalf("args=%+v err=%v", args, err)
		}
	}
}

func TestCatalogoConcurrente(t *testing.T) {
	c := catalogoPrueba(t, nil)
	ctx := contextoHerramienta(t)
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() {
			for nombre, raw := range argumentosValidos {
				r, err := c.Ejecutar(ctx, LlamadaIA{Nombre: nombre, Argumentos: json.RawMessage(raw)})
				if err != nil || codigoResultado(t, r) != "FunctionUnavailable" {
					t.Errorf("%s %v", r.Datos, err)
				}
			}
		})
	}
	wg.Wait()
}
