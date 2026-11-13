package evaluacion

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

func TestVeinteTurnosConBloqueosConservanContextoYDatos(t *testing.T) {
	p := proveedorEvaluacionPrueba(func(_ context.Context, s copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		ultimo := s.Historial[len(s.Historial)-1]
		if ultimo.Rol == "usuario" {
			return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}}}}}, nil
		}
		return copilot.RespuestaIA{}, &copilot.ErrorBloqueo{Motivo: copilot.BloqueoSeguridad}
	})
	cfg := configConsolaPrueba()
	cfg.MaxSolicitudes = 80
	c, err := NuevaConsola(cfg, p, nil, time.Now, ConRecuperacion(), ConContinuidadConversacion())
	if err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		r, err := c.Preguntar(t.Context(), "¿Cuánto tengo?")
		if err != nil || !r.RespuestaDelBackend || r.MotivoRecuperacion != "ProviderSafety" || !strings.Contains(r.Texto, "120000.00 COP") {
			t.Fatalf("turno %d: %+v %v", i, r, err)
		}
		if len(c.memoria.historial) != (i+1)*2 {
			t.Fatal("historial parcial o repetido")
		}
	}
	usadas, _ := c.Consumo()
	if usadas != 40 || c.TurnosCompletados() != 20 {
		t.Fatal("consumo o turnos incorrectos", usadas, c.TurnosCompletados())
	}
	c.NuevaConversacion()
	if c.TurnosCompletados() != 20 {
		t.Fatal("reinicio del contador total")
	}
}

func TestRespaldoNoUsaLecturasDeUnTurnoAnteriorNiTextoBloqueado(t *testing.T) {
	n := 0
	p := proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		n++
		if n == 1 {
			return respuestaRecuperada(), nil
		}
		return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: "texto privado bloqueado: 999999.00 COP"}}}}, &copilot.ErrorBloqueo{Motivo: copilot.BloqueoSeguridad}
	})
	c, _ := NuevaConsola(configConsolaPrueba(), p, nil, time.Now, ConContinuidadConversacion())
	if _, err := c.Preguntar(t.Context(), "Hola"); err != nil {
		t.Fatal(err)
	}
	r, err := c.Preguntar(t.Context(), "Otra pregunta")
	if err != nil || !r.RespuestaDelBackend || strings.Contains(r.Texto, "999999") || strings.Contains(r.Texto, "privado") {
		t.Fatal("filtró respuesta bloqueada", r, err)
	}
}

func TestRespaldoDeConsumoUsaLosMontosDelBackend(t *testing.T) {
	historial := func(datos string) []copilot.TurnoIA {
		return []copilot.TurnoIA{{Rol: "herramienta", Partes: []copilot.ParteIA{{Resultado: &copilot.ResultadoIA{Nombre: "resumir_consumo_por_categoria", Datos: json.RawMessage(datos)}}}}}
	}
	texto := respuestaDeLecturas(historial(`{"datos":{"mes":"2026-09","alcance":"billetera","moneda":"COP","evaluable":true,"meses_completos":4,"meses_requeridos":3,"total":{"centavos":"7000000","pesos":"70000.00 COP"},"categorias":[{"categoria":"alimentos_mercado","nombre":"Alimentos y mercado","total":{"centavos":"5000000","pesos":"50000.00 COP"},"mediana":{"centavos":"4000000","pesos":"40000.00 COP"}},{"categoria":"transporte","nombre":"Transporte","total":{"centavos":"2000000","pesos":"20000.00 COP"},"mediana":null}],"hay_mas":false}}`))
	for _, esperado := range []string{"Gasto de 2026-09 por categoría", "Alimentos y mercado 50000.00 COP", "Transporte 20000.00 COP", "Total calculado por el backend: 70000.00 COP"} {
		if !strings.Contains(texto, esperado) {
			t.Fatalf("falta %q en: %s", esperado, texto)
		}
	}
	for _, prohibido := range []string{"40000.00", "null", "mediana"} {
		if strings.Contains(texto, prohibido) {
			t.Fatalf("presentó %q como gasto: %s", prohibido, texto)
		}
	}
	for nombre, caso := range map[string]string{
		"sin mes":            `{"datos":{"mes":"","total":{"centavos":"7000000","pesos":"70000.00 COP"},"categorias":[]}}`,
		"sin total":          `{"datos":{"mes":"2026-09","categorias":[]}}`,
		"categoria inválida": `{"datos":{"mes":"2026-09","total":{"centavos":"7000000","pesos":"70000.00 COP"},"categorias":[{"nombre":"Transporte","total":{}}]}}`,
	} {
		t.Run(nombre, func(t *testing.T) {
			if texto := respuestaDeLecturas(historial(caso)); texto != "" {
				t.Fatalf("respaldó datos incompletos: %s", texto)
			}
		})
	}
}

func TestContinuidadNoOcultaCuotaCredencialesNiCancelacion(t *testing.T) {
	for nombre, fallo := range map[string]error{"cuota": copilot.ErrLimiteIA, "clave": copilot.ErrCredencialesIA, "cancelacion": context.Canceled, "presupuesto": ErrCuotaEvaluacion} {
		t.Run(nombre, func(t *testing.T) {
			p := proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
				return copilot.RespuestaIA{}, fallo
			})
			c, _ := NuevaConsola(configConsolaPrueba(), p, nil, time.Now, ConContinuidadConversacion())
			_, err := c.Preguntar(t.Context(), "Hola")
			if !errors.Is(err, fallo) || len(c.memoria.historial) != 0 || c.turnos != 0 {
				t.Fatal("ocultó error o guardó datos ficticios", err)
			}
		})
	}
}

func TestTranscripcionInvalidaSeRepiteUnaVezSinReintentarBloqueos(t *testing.T) {
	audio, err := os.ReadFile("../testdata/audio/tono.wav")
	if err != nil {
		t.Fatal(err)
	}
	for nombre, fallo := range map[string]bool{"json_invalido": false, "bloqueo": true} {
		t.Run(nombre, func(t *testing.T) {
			n := 0
			p := proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
				n++
				if fallo {
					return copilot.RespuestaIA{}, &copilot.ErrorBloqueo{Motivo: copilot.BloqueoSeguridad}
				}
				texto := `{"transcripcion":"Hola Vera"}`
				if n == 1 {
					texto = `{"transcripcion":"Hola \u0019Vera"}`
				}
				return copilot.RespuestaIA{Turno: copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Texto: texto}}}}, nil
			})
			c, _ := NuevaConsola(configConsolaPrueba(), p, nil, time.Now, ConRecuperacion())
			texto, err := c.Transcribir(t.Context(), &copilot.AudioIA{MIME: "audio/wav", Datos: audio})
			if fallo {
				if n != 1 || !errors.Is(err, copilot.ErrBloqueoIA) {
					t.Fatal(n, err)
				}
			} else if n != 2 || err != nil || texto != "Hola Vera" {
				t.Fatal(n, texto, err)
			}
		})
	}
}

func TestMontoAisladoNoLlamaProveedorNiPrepara(t *testing.T) {
	p := proveedorEvaluacionPrueba(func(context.Context, copilot.SolicitudIA) (copilot.RespuestaIA, error) {
		t.Fatal("consultó al modelo para un importe ambiguo")
		return copilot.RespuestaIA{}, nil
	})
	c, _ := NuevaConsola(configConsolaPrueba(), p, nil, time.Now)
	r, err := c.Preguntar(t.Context(), "Pues cincuenta.")
	usadas, _ := c.Consumo()
	if err != nil || !r.RespuestaDelBackend || r.AccionPendiente != nil || usadas != 0 || !strings.Contains(r.Texto, "cincuenta mil pesos") {
		t.Fatal(r, err, usadas)
	}
}
