package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/logging"
	"github.com/go-chi/chi/v5/middleware"
)

const respuestaTexto = `{"candidates":[{"content":{"role":"model","parts":[{"text":"Hola"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":4}}`

func solicitudTexto() copilot.SolicitudIA {
	return copilot.SolicitudIA{Instrucciones: "instrucciones privadas", Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: "texto privado"}}}}}
}

func proveedorPrueba(t *testing.T, handler http.HandlerFunc) *Proveedor {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := nuevo(t.Context(), Config{APIKey: "clave-secreta-prueba", Modelo: "modelo-prueba", Timeout: time.Second, Temperatura: 0.2}, slog.New(slog.NewJSONHandler(io.Discard, nil)), srv.Client(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p.esperar = func(context.Context, time.Duration) error { return nil }
	return p
}

func TestTraduccionFuncionesAudioYResultados(t *testing.T) {
	peticiones := make(chan map[string]any, 2)
	p := proveedorPrueba(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/modelo-prueba:generateContent" {
			t.Errorf("ruta inesperada: %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "clave-secreta-prueba" {
			t.Error("falta la credencial")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		peticiones <- body
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"razonamiento privado","thought":true},{"text":"Consultaré"},{"functionCall":{"id":"c1","name":"consultar","args":{"monto":"9223372036854775807"}},"thoughtSignature":"AQID"},{"functionCall":{"id":"c2","name":"limites","args":{}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":21,"candidatesTokenCount":9}}`)
	})
	s := solicitudTexto()
	s.Funciones = []copilot.FuncionIA{{Nombre: "consultar", Descripcion: "Consulta segura", Parametros: json.RawMessage(`{"type":"object","properties":{"monto":{"type":"string","description":"Centavos","pattern":"^[0-9]+$"},"tope":{"type":"integer","minimum":1,"maximum":20}},"required":["monto"],"additionalProperties":false}`)}}
	s.Historial[0].Partes = append(s.Historial[0].Partes, copilot.ParteIA{Audio: &copilot.AudioIA{MIME: "audio/wav", Datos: []byte{1, 2}}})
	res, err := p.Generar(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	if res.TokensEntrada != 21 || res.TokensSalida != 9 || len(res.Turno.Partes) != 3 {
		t.Fatalf("respuesta incorrecta: %+v", res)
	}
	if res.Turno.Partes[1].Llamada.ID != "c1" || res.Turno.Partes[2].Llamada.ID != "c2" || !bytes.Equal(res.Turno.Partes[1].Firma, []byte{1, 2, 3}) {
		t.Fatal("se perdieron llamadas o firma")
	}
	if !strings.Contains(string(res.Turno.Partes[1].Llamada.Argumentos), `"9223372036854775807"`) {
		t.Fatal("se alteró el monto string")
	}
	primera := <-peticiones
	tools := primera["tools"].([]any)
	declaracion := tools[0].(map[string]any)["functionDeclarations"].([]any)[0].(map[string]any)
	var esquema any
	if err := json.Unmarshal(s.Funciones[0].Parametros, &esquema); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(declaracion["parametersJsonSchema"], esquema) || declaracion["description"] != "Consulta segura" {
		t.Fatal("se perdió información del esquema")
	}
	modo := primera["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"]
	if modo != "AUTO" {
		t.Fatalf("modo = %v", modo)
	}
	generacion := primera["generationConfig"].(map[string]any)
	if generacion["maxOutputTokens"] != float64(2048) || len(primera["safetySettings"].([]any)) != 4 {
		t.Fatal("faltan límites o seguridad")
	}
	s.Historial = append(s.Historial, res.Turno, copilot.TurnoIA{Rol: "herramienta", Partes: []copilot.ParteIA{
		{Resultado: &copilot.ResultadoIA{ID: "c1", Nombre: "consultar", Datos: json.RawMessage(`{"saldo":"9223372036854775807"}`)}},
		{Resultado: &copilot.ResultadoIA{ID: "c2", Nombre: "limites", Datos: json.RawMessage(`{"error":"no disponible"}`)}},
	}})
	if _, err := p.Generar(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	segunda := <-peticiones
	contents := segunda["contents"].([]any)
	parts := contents[1].(map[string]any)["parts"].([]any)
	if parts[1].(map[string]any)["thoughtSignature"] != "AQID" {
		t.Fatal("no se devolvió firma al modelo")
	}
	results := contents[2].(map[string]any)["parts"].([]any)
	if results[0].(map[string]any)["functionResponse"].(map[string]any)["id"] != "c1" {
		t.Fatal("no se correlacionó el resultado")
	}
	userParts := contents[0].(map[string]any)["parts"].([]any)
	if userParts[1].(map[string]any)["inlineData"].(map[string]any)["data"] != "AQI=" {
		t.Fatal("audio modificado")
	}
}

func TestErroresYReintentos(t *testing.T) {
	for _, caso := range []struct {
		nombre   string
		codigo   int
		cuerpo   string
		esperado error
		intentos int
	}{
		{"clave", 401, "", copilot.ErrCredencialesIA, 1},
		{"permiso", 403, "", copilot.ErrCredencialesIA, 1},
		{"clave400", 400, `{"error":{"code":400,"details":[{"reason":"API_KEY_INVALID"}]}}`, copilot.ErrCredencialesIA, 1},
		{"solicitud", 400, "", copilot.ErrSolicitudIA, 1},
		{"modelo", 404, "", copilot.ErrSolicitudIA, 1},
		{"limite", 429, "", copilot.ErrLimiteIA, 3},
		{"timeout", 408, "", copilot.ErrTimeoutIA, 3},
		{"servidor", 503, "", copilot.ErrNoDisponibleIA, 3},
		{"seguridad", 200, `{"promptFeedback":{"blockReason":"SAFETY"}}`, copilot.ErrBloqueoIA, 1},
		{"bloqueoCandidato", 200, `{"candidates":[{"finishReason":"SAFETY"}]}`, copilot.ErrBloqueoIA, 1},
		{"vacia", 200, `{}`, copilot.ErrRespuestaIA, 1},
		{"truncada", 200, `{"candidates":[{"finishReason":"MAX_TOKENS"}]}`, copilot.ErrRespuestaIA, 1},
		{"continuacion", 200, `{"candidates":[{"finishReason":"CONTINUATION","continuationToken":"AQID"}]}`, copilot.ErrRespuestaIA, 1},
	} {
		t.Run(caso.nombre, func(t *testing.T) {
			var intentos atomic.Int32
			p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
				intentos.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(caso.codigo)
				cuerpo := caso.cuerpo
				if cuerpo == "" {
					cuerpo = fmt.Sprintf(`{"error":{"code":%d,"message":"secreto del servidor"}}`, caso.codigo)
				}
				fmt.Fprint(w, cuerpo)
			})
			var esperas []time.Duration
			p.esperar = func(_ context.Context, d time.Duration) error { esperas = append(esperas, d); return nil }
			_, err := p.Generar(t.Context(), solicitudTexto())
			if !errors.Is(err, caso.esperado) || int(intentos.Load()) != caso.intentos {
				t.Fatalf("error=%v, intentos=%d", err, intentos.Load())
			}
			if strings.Contains(err.Error(), "secreto") {
				t.Fatal("error filtró datos del proveedor")
			}
			if caso.intentos == 3 {
				if !errors.Is(err, copilot.ErrNoDisponibleIA) {
					t.Fatal("falta categoría de indisponibilidad")
				}
				if len(esperas) != 2 || esperas[0] < 200*time.Millisecond || esperas[0] >= 300*time.Millisecond || esperas[1] < 400*time.Millisecond || esperas[1] >= 600*time.Millisecond {
					t.Fatalf("backoff: %v", esperas)
				}
			}
		})
	}
}

func TestRecuperaTrasDosFallos(t *testing.T) {
	var n atomic.Int32
	p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if n.Add(1) < 3 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"code":503}}`)
			return
		}
		fmt.Fprint(w, respuestaTexto)
	})
	r, err := p.Generar(t.Context(), solicitudTexto())
	if err != nil || n.Load() != 3 || r.Turno.Partes[0].Texto != "Hola" {
		t.Fatalf("respuesta=%+v, err=%v", r, err)
	}
}

func TestTimeoutYCancelacion(t *testing.T) {
	var n atomic.Int32
	terminado := make(chan struct{})
	defer close(terminado)
	p := proveedorPrueba(t, func(_ http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		n.Add(1)
		select {
		case <-r.Context().Done():
		case <-terminado:
		}
	})
	p.cfg.Timeout = 20 * time.Millisecond
	_, err := p.Generar(t.Context(), solicitudTexto())
	if !errors.Is(err, copilot.ErrTimeoutIA) || n.Load() != 3 {
		t.Fatalf("timeout: %v (%d intentos)", err, n.Load())
	}
	n.Store(0)
	p.cfg.Timeout = time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err = p.Generar(ctx, solicitudTexto())
	if !errors.Is(err, context.DeadlineExceeded) || n.Load() != 1 {
		t.Fatalf("plazo padre: %v (%d intentos)", err, n.Load())
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	_, err = p.Generar(ctx, solicitudTexto())
	if !errors.Is(err, context.Canceled) || n.Load() != 1 {
		t.Fatal("se llamó al proveedor con contexto cancelado")
	}
}

func TestCancelacionDuranteBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var n atomic.Int32
	p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) { n.Add(1); w.WriteHeader(503) })
	p.esperar = func(ctx context.Context, d time.Duration) error { cancel(); return esperar(ctx, d) }
	_, err := p.Generar(ctx, solicitudTexto())
	if !errors.Is(err, context.Canceled) || n.Load() != 1 {
		t.Fatalf("err=%v llamadas=%d", err, n.Load())
	}
}

func TestLogsSinContenido(t *testing.T) {
	var logs bytes.Buffer
	p := proveedorPrueba(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, respuestaTexto) })
	p.logger = slog.New(logging.WithRequestID(slog.NewJSONHandler(&logs, nil)))
	ctx := context.WithValue(t.Context(), middleware.RequestIDKey, "solicitud-prueba")
	if _, err := p.Generar(ctx, solicitudTexto()); err != nil {
		t.Fatal(err)
	}
	for _, secreto := range []string{"clave-secreta-prueba", "texto privado", "instrucciones privadas", "Hola"} {
		if strings.Contains(logs.String(), secreto) {
			t.Fatalf("log contiene %q", secreto)
		}
	}
	for _, campo := range []string{"solicitud-prueba", "tokens_entrada", "tokens_salida", "duracion", "modelo", "resultado"} {
		if !strings.Contains(logs.String(), campo) {
			t.Fatalf("falta %q", campo)
		}
	}
}

// TestGeminiManual es opt-in: sólo datos sintéticos y nunca corre en la suite normal.
func TestGeminiManual(t *testing.T) {
	if os.Getenv("VERA_PRUEBA_REAL") != "1" {
		t.Skip("requiere VERA_PRUEBA_REAL=1 y credenciales locales")
	}
	p, err := Nuevo(t.Context(), Config{APIKey: os.Getenv("GEMINI_API_KEY"), Modelo: os.Getenv("VERA_MODELO"), Timeout: 10 * time.Second, Temperatura: 0.2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	r, err := p.Generar(ctx, copilot.SolicitudIA{Instrucciones: "Responde un saludo breve en español.", Historial: []copilot.TurnoIA{{Rol: "usuario", Partes: []copilot.ParteIA{{Texto: "Hola, prueba sintética de conexión."}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Turno.Partes) == 0 {
		t.Fatal("sin respuesta")
	}
	t.Logf("conexión verificada: entrada=%d salida=%d", r.TokensEntrada, r.TokensSalida)
}
