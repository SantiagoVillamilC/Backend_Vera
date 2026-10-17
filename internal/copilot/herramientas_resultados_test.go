package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
)

func TestVistasMinimasDeCadaFuncion(t *testing.T) {
	fecha := time.Date(2026, 10, 17, 15, 0, 0, 0, time.UTC)
	casos := map[string]DatosHerramienta{
		"consultar_saldo":         SaldoHerramienta{Disponible: math.MaxInt64, Retenido: 1},
		"consultar_limites":       LimitesHerramienta{MaximoEnviable: 100, TopeOperacion: 200, CupoMensualRestante: 300, GMF: 1},
		"listar_movimientos":      MovimientosHerramienta{Movimientos: []MovimientoHerramienta{{Fecha: fecha, Motivo: "envio", Monto: -12345, Contraparte: "Laura Rodriguez"}}},
		"resumir_movimientos":     ResumenHerramienta{Entradas: 500, Salidas: 100, Neto: 400, Cantidad: 2, Grupos: []GrupoHerramienta{{Nombre: "envio", Cantidad: 1, Salidas: 100, Neto: -100}}},
		"buscar_destinatario":     DestinatariosHerramienta{Destinatarios: []DestinatarioHerramienta{{Nombre: "Laura Rodriguez", Celular: "+573001234567", Referencia: "referencia_opaca_123"}}},
		"consultar_transferencia": TransferenciaHerramienta{Estado: "confirmada", Monto: 12345, CreadaEn: fecha, ActualizadaEn: fecha},
		"preparar_transferencia":  AccionHerramienta{AccionID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Destinatario: DestinatarioHerramienta{Nombre: "Laura Rodriguez", Celular: "+573001234567"}, Monto: 5000000, ExpiraEn: fecha.Add(5 * time.Minute)},
	}
	for nombre, datos := range casos {
		t.Run(nombre, func(t *testing.T) {
			c := catalogoPrueba(t, map[string]ManejadorHerramienta{nombre: func(context.Context, identidad.Identidad, ArgumentosHerramienta) (DatosHerramienta, error) {
				return datos, nil
			}})
			r, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{ID: "c1", Nombre: nombre, Argumentos: json.RawMessage(argumentosValidos[nombre])})
			if err != nil || codigoResultado(t, r) != "" {
				t.Fatalf("%s %v", r.Datos, err)
			}
			for _, secreto := range []string{"Laura Rodriguez", "+573001234567", "usuario_id", "cuenta_origen", "sesion_id"} {
				if strings.Contains(string(r.Datos), secreto) {
					t.Fatalf("filtró %s: %s", secreto, r.Datos)
				}
			}
			if nombre == "consultar_saldo" && (!bytes.Contains(r.Datos, []byte(`"centavos":"9223372036854775807"`)) || !bytes.Contains(r.Datos, []byte(`"pesos":"92233720368547758.07 COP"`))) {
				t.Fatal("precisión perdida")
			}
			if nombre == "listar_movimientos" && (!bytes.Contains(r.Datos, []byte(`"centavos":"-12345"`)) || !bytes.Contains(r.Datos, []byte(`2026-10-17T10:00:00-05:00`))) {
				t.Fatal("signo o zona incorrecta")
			}
			if nombre == "preparar_transferencia" && !bytes.Contains(r.Datos, []byte(`"requiere_confirmacion":true`)) {
				t.Fatal("falta confirmación")
			}
		})
	}
}

func TestResultadosAcotados(t *testing.T) {
	fecha := time.Date(2026, 10, 17, 15, 0, 0, 0, time.UTC)
	movimientos := make([]MovimientoHerramienta, 21)
	grupos := make([]GrupoHerramienta, 21)
	destinatarios := make([]DestinatarioHerramienta, 21)
	for i := range 21 {
		movimientos[i] = MovimientoHerramienta{Fecha: fecha, Motivo: "deposito", Monto: 100}
		grupos[i] = GrupoHerramienta{Nombre: "Laura Rodriguez", Cantidad: 1}
		destinatarios[i] = DestinatarioHerramienta{Nombre: "Laura Rodriguez", Celular: "3001234567", Referencia: "referencia_opaca_123"}
	}
	for _, caso := range []struct {
		nombre, campo string
		args          ArgumentosHerramienta
		datos         DatosHerramienta
		limite        int
	}{
		{"listar_movimientos", "movimientos", ArgumentosHerramienta{Limite: 2}, MovimientosHerramienta{Movimientos: movimientos}, 2},
		{"resumir_movimientos", "grupos", ArgumentosHerramienta{AgruparPor: "contraparte"}, ResumenHerramienta{Grupos: grupos}, 20},
		{"buscar_destinatario", "destinatarios", ArgumentosHerramienta{}, DestinatariosHerramienta{Destinatarios: destinatarios}, 20},
	} {
		b, err := serializarDatos(caso.nombre, caso.args, caso.datos)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Datos map[string]any `json:"datos"`
		}
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		if len(v.Datos[caso.campo].([]any)) != caso.limite || v.Datos["hay_mas"] != true {
			t.Fatalf("límite incorrecto: %s", b)
		}
	}
	for i := range destinatarios {
		destinatarios[i].Referencia = strings.Repeat("a", 1024)
	}
	if _, err := serializarDatos("buscar_destinatario", ArgumentosHerramienta{}, DestinatariosHerramienta{Destinatarios: destinatarios}); err == nil {
		t.Fatal("aceptó resultado mayor a 16 KiB")
	}
	b, err := serializarDatos("listar_movimientos", ArgumentosHerramienta{Limite: 20}, MovimientosHerramienta{})
	if err != nil || !bytes.Contains(b, []byte(`"movimientos":[]`)) {
		t.Fatalf("lista vacía no es array: %s %v", b, err)
	}
}

func TestFalloCerradoYLogsSaneados(t *testing.T) {
	for nombre, caso := range map[string]struct {
		datos  DatosHerramienta
		err    error
		codigo string
	}{
		"errorPrivado":      {nil, errors.New("clave-privada consulta SQL celular 3001234567"), "FunctionUnavailable"},
		"errorEnvuelto":     {nil, fmt.Errorf("detalle secreto: %w", ErrHerramientaLimite), "TooManyRequests"},
		"codigoDesconocido": {nil, ErrorHerramienta("secreto"), "FunctionUnavailable"},
		"vistaEquivocada":   {SaldoHerramienta{}, nil, "InvalidFunctionResult"},
		"nil":               {nil, nil, "InvalidFunctionResult"},
	} {
		t.Run(nombre, func(t *testing.T) {
			var logs bytes.Buffer
			c, err := NuevoCatalogo(map[string]ManejadorHerramienta{"buscar_destinatario": func(context.Context, identidad.Identidad, ArgumentosHerramienta) (DatosHerramienta, error) {
				return caso.datos, caso.err
			}}, slog.New(logging.WithRequestID(slog.NewJSONHandler(&logs, nil))))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(contextoHerramienta(t), middleware.RequestIDKey, "request-prueba")
			r, err := c.Ejecutar(ctx, LlamadaIA{Nombre: "buscar_destinatario", Argumentos: json.RawMessage(`{"celular":"3001234567"}`)})
			if err != nil || codigoResultado(t, r) != caso.codigo {
				t.Fatalf("%s %v", r.Datos, err)
			}
			for _, secreto := range []string{"3001234567", "clave-privada", "secreto", "SQL", "usuario-autenticado", "sesion-privada"} {
				if strings.Contains(logs.String()+string(r.Datos), secreto) {
					t.Fatalf("filtró %s", secreto)
				}
			}
			if !strings.Contains(logs.String(), "request-prueba") || !strings.Contains(logs.String(), `"celular":true`) {
				t.Fatalf("traza incompleta: %s", logs.String())
			}
		})
	}
}

type lectorTransferenciaPrueba struct {
	tr      nucleo.Transferencia
	err     error
	usuario string
	id      uuid.UUID
}

func (l *lectorTransferenciaPrueba) ConsultarTransferencia(_ context.Context, usuario string, id uuid.UUID) (nucleo.Transferencia, error) {
	l.usuario = usuario
	l.id = id
	return l.tr, l.err
}

func TestConsultaTransferenciaPropia(t *testing.T) {
	fecha := time.Date(2026, 10, 17, 15, 0, 0, 0, time.UTC)
	for nombre, caso := range map[string]struct {
		propietario string
		err         error
		codigo      string
	}{
		"propia":   {"usuario-autenticado", nil, ""},
		"ajena":    {"otro", nil, "NotFound"},
		"noExiste": {"", nucleo.ErrTransferenciaNoEncontrada, "NotFound"},
		"error":    {"", errors.New("dsn privado"), "FunctionUnavailable"},
	} {
		t.Run(nombre, func(t *testing.T) {
			lector := &lectorTransferenciaPrueba{tr: nucleo.Transferencia{UsuarioID: caso.propietario, CuentaOrigen: "cuenta-privada", LlaveDestino: "llave-privada", Monto: 12345, Estado: "confirmada", CreadaEn: fecha, ActualizadaEn: fecha}, err: caso.err}
			c := catalogoPrueba(t, map[string]ManejadorHerramienta{"consultar_transferencia": ConsultaTransferenciaIA(lector)})
			r, err := c.Ejecutar(contextoHerramienta(t), LlamadaIA{Nombre: "consultar_transferencia", Argumentos: json.RawMessage(argumentosValidos["consultar_transferencia"])})
			if err != nil || codigoResultado(t, r) != caso.codigo || lector.usuario != "usuario-autenticado" || lector.id.String() != "11111111-1111-4111-8111-111111111111" {
				t.Fatalf("%s %v", r.Datos, err)
			}
			if strings.Contains(string(r.Datos), "privad") {
				t.Fatal("filtró campos internos")
			}
		})
	}
}
