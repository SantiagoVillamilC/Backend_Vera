package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
)

func catalogoPrueba(t *testing.T, manejadores map[string]ManejadorHerramienta) *Catalogo {
	t.Helper()
	c, err := NuevoCatalogo(manejadores, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func contextoHerramienta(t *testing.T) context.Context {
	t.Helper()
	return identidad.ConIdentidad(t.Context(), identidad.Identidad{UsuarioID: "usuario-autenticado", ParteID: "parte-autenticada", SesionID: "sesion-privada"})
}

func TestCatalogoCerrado(t *testing.T) {
	c := catalogoPrueba(t, nil)
	declaraciones := c.Declaraciones()
	if len(declaraciones) != 7 {
		t.Fatalf("funciones=%d", len(declaraciones))
	}
	for _, f := range declaraciones {
		var esquema map[string]any
		if err := json.Unmarshal(f.Parametros, &esquema); err != nil {
			t.Fatal(err)
		}
		if esquema["additionalProperties"] != false {
			t.Fatalf("%s permite extras", f.Nombre)
		}
		for _, prohibido := range []string{"usuario_id", "parte_id", "sesion_id", "cuenta_origen", "contexto"} {
			if _, ok := esquema["properties"].(map[string]any)[prohibido]; ok {
				t.Fatalf("%s expone %s", f.Nombre, prohibido)
			}
		}
		clase, ok := c.Clase(f.Nombre)
		if !ok || (clase != Lectura && clase != Preparacion) {
			t.Fatalf("clase inválida: %v", clase)
		}
		if (f.Nombre == "preparar_transferencia") != (clase == Preparacion) {
			t.Fatal("clasificación incorrecta")
		}
	}
	if _, err := NuevoCatalogo(map[string]ManejadorHerramienta{"transferir": func(context.Context, identidad.Identidad, ArgumentosHerramienta) (DatosHerramienta, error) {
		return nil, nil
	}}, nil); err == nil {
		t.Fatal("aceptó función ajena")
	}
	declaraciones[0].Parametros[0] = 'x'
	if !json.Valid(c.Declaraciones()[0].Parametros) {
		t.Fatal("el llamador puede modificar el catálogo")
	}
	r, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{ID: "llamada", Nombre: "transferir", Argumentos: json.RawMessage(`{}`)})
	if err != nil || r.ID != "llamada" || codigoResultado(t, r) != "UnknownFunction" {
		t.Fatalf("resultado=%+v err=%v", r, err)
	}
}

func codigoResultado(t *testing.T, r ResultadoIA) string {
	t.Helper()
	var v struct {
		Error *struct {
			Codigo string `json:"codigo"`
		} `json:"error"`
	}
	if err := json.Unmarshal(r.Datos, &v); err != nil {
		t.Fatal(err)
	}
	if v.Error == nil {
		return ""
	}
	return v.Error.Codigo
}

func TestCatalogoIdentidadYCancelacion(t *testing.T) {
	var llamadas int
	c := catalogoPrueba(t, map[string]ManejadorHerramienta{"consultar_saldo": func(_ context.Context, id identidad.Identidad, _ ArgumentosHerramienta) (DatosHerramienta, error) {
		llamadas++
		if id.UsuarioID != "usuario-autenticado" || id.ParteID != "parte-autenticada" {
			t.Fatal("identidad incorrecta")
		}
		return SaldoHerramienta{Disponible: 12345, Retenido: 0}, nil
	}})
	llamada := LlamadaIA{Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}
	r, err := c.Ejecutar(t.Context(), llamada)
	if err != nil || codigoResultado(t, r) != "IdentityRequired" || llamadas != 0 {
		t.Fatal("ejecutó sin identidad")
	}
	r, err = c.Ejecutar(contextoHerramienta(t), llamada)
	if err != nil || codigoResultado(t, r) != "" || llamadas != 1 {
		t.Fatalf("resultado=%+v err=%v", r, err)
	}
	ctx, cancel := context.WithCancel(contextoHerramienta(t))
	cancel()
	_, err = c.Ejecutar(ctx, llamada)
	if !errors.Is(err, context.Canceled) || llamadas != 1 {
		t.Fatal("ejecutó con contexto cancelado")
	}
}

func TestCatalogoRechazaLlaveAmbiguaAntesDelManejador(t *testing.T) {
	llamadas := 0
	c := catalogoPrueba(t, map[string]ManejadorHerramienta{"preparar_transferencia": func(context.Context, identidad.Identidad, ArgumentosHerramienta) (DatosHerramienta, error) {
		llamadas++
		return nil, nil
	}})
	r, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{Nombre: "preparar_transferencia", Argumentos: json.RawMessage(`{"llave_breb":"1234567890","monto_pesos":"50000"}`)})
	if err != nil || codigoResultado(t, r) != "ValidationError" || llamadas != 0 {
		t.Fatalf("llave ambigua llegó al manejador: llamadas=%d resultado=%s err=%v", llamadas, r.Datos, err)
	}
}

func TestCatalogoMantieneLlavesInequivocas(t *testing.T) {
	for _, llave := range []string{"@laura", "laura@example.com", "+573001234567", "celular:3001234567", "documento:1234567", "comercio:ABC12345"} {
		t.Run(llave, func(t *testing.T) {
			llamadas := 0
			c := catalogoPrueba(t, map[string]ManejadorHerramienta{"preparar_transferencia": func(_ context.Context, _ identidad.Identidad, args ArgumentosHerramienta) (DatosHerramienta, error) {
				llamadas++
				if args.Monto != 5000000 {
					t.Fatalf("monto alterado: %d", args.Monto)
				}
				return nil, ErrHerramientaNoDisponible
			}})
			args, err := json.Marshal(map[string]string{"llave_breb": llave, "monto_pesos": "50000"})
			if err != nil {
				t.Fatal(err)
			}
			r, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{Nombre: "preparar_transferencia", Argumentos: args})
			if err != nil || codigoResultado(t, r) != "FunctionUnavailable" || llamadas != 1 {
				t.Fatalf("llave válida rechazada: %s %v", r.Datos, err)
			}
		})
	}
}
