package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
)

const respuestaTexto = `{"model":"gemma4-e4b-local","message":{"role":"assistant","content":"Hola"},"done":true,"done_reason":"stop","prompt_eval_count":12,"eval_count":4}`

func proveedorPrueba(t *testing.T, handler http.HandlerFunc) *Proveedor {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := Nuevo(Config{URL: srv.URL, Modelo: "gemma4-e4b-local", Timeout: 2 * time.Second, Temperatura: 0.2}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	p.esperar = func(context.Context, time.Duration) error { return nil }
	return p
}

func solicitudTexto(partes ...copilot.ParteIA) copilot.SolicitudIA {
	if len(partes) == 0 {
		partes = []copilot.ParteIA{{Texto: "texto privado"}}
	}
	return copilot.SolicitudIA{Instrucciones: "instrucciones privadas", Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: partes}}}
}

// declaracionesEntrega las herramientas reales del catálogo, sin copiarlas en
// la prueba: si el contrato cambia, esta prueba cambia con él.
func declaraciones(t *testing.T) []copilot.FuncionIA {
	t.Helper()
	catalogo, err := copilot.NuevoCatalogo(nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return catalogo.Declaraciones()
}

func TestPeticionCompletaYHerramientasExactas(t *testing.T) {
	capturado := make(chan []byte, 1)
	p := proveedorPrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/chat" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("contrato HTTP inesperado: %s %s %q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		cuerpo, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		capturado <- cuerpo
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write([]byte(respuestaTexto))
	})
	funciones := declaraciones(t)
	if len(funciones) < 7 {
		t.Fatalf("el catálogo declara %d herramientas, se esperaban al menos siete", len(funciones))
	}
	s := copilot.SolicitudIA{
		Instrucciones: "instrucciones privadas",
		Funciones:     funciones,
		Historial: []copilot.TurnoIA{
			{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: "texto privado"}}},
			{Rol: "asistente", Partes: []copilot.ParteIA{
				{Texto: "Consultaré"},
				{Llamada: &copilot.LlamadaIA{ID: "call_1", Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}},
				{Llamada: &copilot.LlamadaIA{ID: "call_2", Nombre: "resumir_consumo_por_categoria", Argumentos: json.RawMessage(`{"mes":"2026-11"}`)}},
			}},
			{Rol: "herramienta", Partes: []copilot.ParteIA{{
				Resultado: &copilot.ResultadoIA{ID: "call_1", Nombre: "consultar_saldo", Datos: json.RawMessage(`{"saldo":{"disponible":"1000"}}`)},
			}}},
		},
	}
	res, err := p.Generar(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if res.TokensEntrada != 12 || res.TokensSalida != 4 || res.Turno.Rol != "asistente" || len(res.Turno.Partes) != 1 || res.Turno.Partes[0].Texto != "Hola" {
		t.Fatalf("respuesta inesperada: %+v", res)
	}
	cuerpo := <-capturado
	if bytes.Contains(cuerpo, []byte("think")) {
		t.Fatal("la petición no debe mencionar think")
	}
	var peticion map[string]any
	if err := json.Unmarshal(cuerpo, &peticion); err != nil {
		t.Fatal(err)
	}
	if peticion["model"] != "gemma4-e4b-local" || peticion["stream"] != false || peticion["keep_alive"] != "30m" {
		t.Fatalf("parámetros del modelo: %v %v %v", peticion["model"], peticion["stream"], peticion["keep_alive"])
	}
	opciones := peticion["options"].(map[string]any)
	if opciones["num_ctx"] != float64(8192) || opciones["num_predict"] != float64(2048) || opciones["temperature"] != 0.2 {
		t.Fatalf("opciones: %v", opciones)
	}
	mensajes := peticion["messages"].([]any)
	if len(mensajes) != 4 {
		t.Fatalf("mensajes: %d", len(mensajes))
	}
	for i, rol := range []string{"system", "user", "assistant", "tool"}[:4] {
		if mensajes[i].(map[string]any)["role"] != rol {
			t.Fatalf("mensaje %d: %v", i, mensajes[i])
		}
	}
	asistente := mensajes[2].(map[string]any)
	if asistente["content"] != "Consultaré" || len(asistente["tool_calls"].([]any)) != 2 {
		t.Fatalf("turno del asistente: %v", asistente)
	}
	herramienta := mensajes[3].(map[string]any)
	if herramienta["tool_name"] != "consultar_saldo" || herramienta["content"] != `{"saldo":{"disponible":"1000"}}` {
		t.Fatalf("resultado de herramienta: %v", herramienta)
	}
	tools := peticion["tools"].([]any)
	if len(tools) != len(funciones) {
		t.Fatalf("herramientas enviadas: %d de %d", len(tools), len(funciones))
	}
	for i, f := range funciones {
		// Los parámetros viajan byte a byte: ni reordenados ni redondeados.
		if !bytes.Contains(cuerpo, f.Parametros) {
			t.Fatalf("esquema de %s alterado en la petición", f.Nombre)
		}
		declaracion := tools[i].(map[string]any)
		if declaracion["type"] != "function" {
			t.Fatalf("tipo de herramienta: %v", declaracion["type"])
		}
		funcion := declaracion["function"].(map[string]any)
		if funcion["name"] != f.Nombre || funcion["description"] != f.Descripcion {
			t.Fatalf("declaración %d alterada: %v", i, funcion)
		}
	}
}

func TestRespuestasConUnaYDosLlamadas(t *testing.T) {
	respuestas := []string{
		`{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"consultar_saldo","arguments":{}}}]},"done":true,"done_reason":"stop","prompt_eval_count":10,"eval_count":2}`,
		`{"message":{"role":"assistant","content":"Voy","tool_calls":[{"id":"a","function":{"name":"consultar_limites","arguments":{}}},{"id":"b","function":{"name":"consultar_saldo","arguments":{"tope":3,"nota":"x"}}}]},"done":true,"done_reason":"stop","prompt_eval_count":11,"eval_count":3}`,
	}
	var atendidas int
	p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
		cuerpo := respuestas[atendidas]
		atendidas++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cuerpo))
	})
	primera, err := p.Generar(t.Context(), solicitudTexto())
	if err != nil {
		t.Fatal(err)
	}
	if len(primera.Turno.Partes) != 1 || primera.Turno.Partes[0].Llamada == nil {
		t.Fatalf("primera respuesta: %+v", primera)
	}
	if l := primera.Turno.Partes[0].Llamada; l.Nombre != "consultar_saldo" || l.ID != "" || string(l.Argumentos) != `{}` {
		t.Fatalf("llamada sin argumentos: %+v", l)
	}
	segunda, err := p.Generar(t.Context(), solicitudTexto())
	if err != nil {
		t.Fatal(err)
	}
	if len(segunda.Turno.Partes) != 3 {
		t.Fatalf("partes de la segunda respuesta: %d", len(segunda.Turno.Partes))
	}
	if segunda.Turno.Partes[0].Texto != "Voy" {
		t.Fatalf("texto previo a las llamadas: %+v", segunda.Turno.Partes[0])
	}
	if l := segunda.Turno.Partes[2].Llamada; l == nil || l.ID != "b" || l.Nombre != "consultar_saldo" || string(l.Argumentos) != `{"tope":3,"nota":"x"}` {
		t.Fatalf("segunda llamada: %+v", l)
	}
}

func TestRespuestaIncompleta(t *testing.T) {
	for _, cuerpo := range []string{
		`{"message":{"role":"assistant","content":"cortado"},"done":false,"done_reason":"stop"}`,
		`{"message":{"role":"assistant","content":"cortado"},"done":true,"done_reason":"length"}`,
		`{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}`,
		`{"message":{"role":"assistant","content":""},"done":true,"done_reason":""}`,
		`{"done":true,"done_reason":"stop"}`,
		`{`,
	} {
		p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(cuerpo))
		})
		if _, err := p.Generar(t.Context(), solicitudTexto()); !errors.Is(err, copilot.ErrRespuestaIA) {
			t.Fatalf("%s devolvió %v", cuerpo, err)
		}
	}
}

func TestImportesFueraDelRangoExacto(t *testing.T) {
	for _, caso := range []struct {
		argumentos string
		valido     bool
	}{
		{`{"monto":"9223372036854775807"}`, true},
		{`{"monto":4503599627370496}`, true},
		{`{"monto":9007199254740992}`, false},
		{`{"monto":9007199254740993}`, false},
		{`{"monto":9223372036854775807}`, false},
		{`{"monto":1.5}`, true},
		{`{"monto":[1,{"centavos":9007199254740993}]}`, false},
		{`{"monto":"1e400"}`, true},
		{`{"monto":1e400}`, false},
	} {
		cuerpo := `{"message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"preparar_transferencia","arguments":` + caso.argumentos + `}}]},"done":true,"done_reason":"stop"}`
		p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(cuerpo))
		})
		res, err := p.Generar(t.Context(), solicitudTexto())
		if caso.valido && err != nil {
			t.Fatalf("%s rechazado: %v", caso.argumentos, err)
		}
		if caso.valido {
			if string(res.Turno.Partes[0].Llamada.Argumentos) != caso.argumentos {
				t.Fatalf("argumentos alterados: %s", res.Turno.Partes[0].Llamada.Argumentos)
			}
			continue
		}
		if !errors.Is(err, copilot.ErrRespuestaIA) {
			t.Fatalf("%s debía rechazarse, devolvió %v", caso.argumentos, err)
		}
	}
}

func TestTraduccionRechazaSolicitudesInvalidas(t *testing.T) {
	base := func() copilot.SolicitudIA { return solicitudTexto() }
	for _, caso := range []struct {
		nombre string
		cambia func(*copilot.SolicitudIA)
	}{
		{"soloTranscripcion", func(s *copilot.SolicitudIA) { s.SoloTranscripcion = true }},
		{"sinHistorial", func(s *copilot.SolicitudIA) { s.Historial = nil }},
		{"rolDesconocido", func(s *copilot.SolicitudIA) { s.Historial[0].Rol = "sistema" }},
		{"audioEnUsuario", func(s *copilot.SolicitudIA) {
			s.Historial[0].Partes = []copilot.ParteIA{{Audio: &copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1}}}}
		}},
		{"dosRepresentaciones", func(s *copilot.SolicitudIA) {
			s.Historial[0].Partes = []copilot.ParteIA{{Texto: "hola", Llamada: &copilot.LlamadaIA{Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{}`)}}}
		}},
		{"turnoVacio", func(s *copilot.SolicitudIA) { s.Historial[0].Partes = nil }},
		{"resultadoEnUsuario", func(s *copilot.SolicitudIA) {
			s.Historial[0].Partes = []copilot.ParteIA{{Resultado: &copilot.ResultadoIA{Nombre: "consultar_saldo", Datos: json.RawMessage(`{}`)}}}
		}},
		{"llamadaSinNombre", func(s *copilot.SolicitudIA) {
			s.Historial = append(s.Historial, copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{Argumentos: json.RawMessage(`{}`)}}}})
		}},
		{"resultadoSinNombre", func(s *copilot.SolicitudIA) {
			s.Historial = append(s.Historial, copilot.TurnoIA{Rol: "herramienta", Partes: []copilot.ParteIA{{Resultado: &copilot.ResultadoIA{Datos: json.RawMessage(`{}`)}}}})
		}},
		{"argumentosNoObjeto", func(s *copilot.SolicitudIA) {
			s.Historial = append(s.Historial, copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{Nombre: "consultar_saldo", Argumentos: json.RawMessage(`[]`)}}}})
		}},
		{"argumentosFueraDeRango", func(s *copilot.SolicitudIA) {
			s.Historial = append(s.Historial, copilot.TurnoIA{Rol: "asistente", Partes: []copilot.ParteIA{{Llamada: &copilot.LlamadaIA{Nombre: "consultar_saldo", Argumentos: json.RawMessage(`{"centavos":9007199254740993}`)}}}})
		}},
		{"esquemaSinTipoObjeto", func(s *copilot.SolicitudIA) {
			s.Funciones = []copilot.FuncionIA{{Nombre: "consultar_saldo", Parametros: json.RawMessage(`{"type":"string"}`)}}
		}},
		{"esquemaInvalido", func(s *copilot.SolicitudIA) {
			s.Funciones = []copilot.FuncionIA{{Nombre: "consultar_saldo", Parametros: json.RawMessage(`{`)}}
		}},
		{"herramientaSinNombre", func(s *copilot.SolicitudIA) {
			s.Funciones = []copilot.FuncionIA{{Parametros: json.RawMessage(`{"type":"object"}`)}}
		}},
		{"herramientaDuplicada", func(s *copilot.SolicitudIA) {
			f := copilot.FuncionIA{Nombre: "consultar_saldo", Parametros: json.RawMessage(`{"type":"object"}`)}
			s.Funciones = []copilot.FuncionIA{f, f}
		}},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			s := base()
			caso.cambia(&s)
			p := proveedorPrueba(t, func(http.ResponseWriter, *http.Request) { t.Error("no debía llamarse a la red") })
			if _, err := p.Generar(t.Context(), s); !errors.Is(err, copilot.ErrSolicitudIA) {
				t.Fatalf("se esperaba ErrSolicitudIA, llegó %v", err)
			}
		})
	}
}
