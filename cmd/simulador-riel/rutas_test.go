package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// servidorDePrueba arma el simulador con su almacén temporal y devuelve el servidor de pruebas.
func servidorDePrueba(t *testing.T, perillas Perillas) (*httptest.Server, *Simulador) {
	t.Helper()

	simulador := NuevoSimulador(almacenDePrueba(t), NuevoControl(perillas), NuevoDirectorio(nil), "simulador-riel")
	servidor := httptest.NewServer(simulador.Rutas())
	t.Cleanup(servidor.Close)
	return servidor, simulador
}

// ordenJSON arma el cuerpo de una orden válida con el identificador dado.
func ordenJSON(id string) string {
	return `{"transferencia_id":"` + id + `","idempotency_key":"k-` + id + `","llave":"3001234567","monto_centavos":"1000000","moneda":"COP"}`
}

// enviar hace el POST de una orden y devuelve el status y el cuerpo, fallando si no hay respuesta.
// Cierra el body acá mismo (no lo expone) para que quien llama nunca tenga que cerrarlo.
func enviar(t *testing.T, cliente *http.Client, base, cuerpo string) (int, string) {
	t.Helper()

	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/transferencias", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("armar el envío: %v", err)
	}
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := cliente.Do(peticion)
	if err != nil {
		t.Fatalf("enviar la orden: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	datos, err := io.ReadAll(respuesta.Body)
	if err != nil {
		t.Fatalf("leer la respuesta: %v", err)
	}
	return respuesta.StatusCode, string(datos)
}

// desenlace es la parte del cuerpo que las pruebas examinan.
type desenlace struct {
	TransferenciaID string `json:"transferencia_id"`
	Resultado       string `json:"resultado"`
	Motivo          string `json:"motivo"`
	Reintento       bool   `json:"reintento"`
}

// leerDesenlace decodifica el cuerpo de una respuesta de operación.
func leerDesenlace(t *testing.T, cuerpo string) desenlace {
	t.Helper()

	var d desenlace
	if err := json.Unmarshal([]byte(cuerpo), &d); err != nil {
		t.Fatalf("cuerpo no es JSON: %v (%q)", err, cuerpo)
	}
	return d
}

func TestEnvioConfirmadoSeRegistraYSeConsulta(t *testing.T) {
	t.Parallel()

	servidor, _ := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna})
	id := uuid.NewString()
	status, cuerpo := enviar(t, servidor.Client(), servidor.URL, ordenJSON(id))
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (%s)", status, cuerpo)
	}
	if d := leerDesenlace(t, cuerpo); d.Resultado != estadoConfirmada || d.TransferenciaID != id {
		t.Fatalf("desenlace = %+v, quiero confirmada de %s", d, id)
	}

	peticionConsulta, err := http.NewRequestWithContext(t.Context(), http.MethodGet, servidor.URL+"/transferencias/"+id, nil)
	if err != nil {
		t.Fatalf("armar la consulta de estado: %v", err)
	}
	consulta, err := servidor.Client().Do(peticionConsulta)
	if err != nil {
		t.Fatalf("consultar estado: %v", err)
	}
	t.Cleanup(func() { _ = consulta.Body.Close() })
	datos, _ := io.ReadAll(consulta.Body)
	if consulta.StatusCode != http.StatusOK || leerDesenlace(t, string(datos)).Resultado != estadoConfirmada {
		t.Fatalf("consulta = %d %s, quiero 200 confirmada", consulta.StatusCode, datos)
	}
}

func TestPRechazoEsDeterministaEnLosExtremos(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		p      float64
		quiero string
	}{
		"cero siempre confirma": {p: 0, quiero: estadoConfirmada},
		"uno siempre rechaza":   {p: 1, quiero: estadoRechazada},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			servidor, _ := servidorDePrueba(t, Perillas{PRechazo: tc.p, ModoCaida: ModoCaidaNinguna})
			for range 20 {
				status, cuerpo := enviar(t, servidor.Client(), servidor.URL, ordenJSON(uuid.NewString()))
				if status != http.StatusOK {
					t.Fatalf("status = %d, quiero 200", status)
				}
				d := leerDesenlace(t, cuerpo)
				if d.Resultado != tc.quiero {
					t.Fatalf("resultado = %q, quiero %q con p_rechazo %v", d.Resultado, tc.quiero, tc.p)
				}
				if tc.quiero == estadoRechazada && d.Motivo != motivoFondosInsuficientes {
					t.Errorf("motivo = %q, quiero %q", d.Motivo, motivoFondosInsuficientes)
				}
			}
		})
	}
}

func TestLaLatenciaSeRespeta(t *testing.T) {
	t.Parallel()

	servidor, _ := servidorDePrueba(t, Perillas{Latencia: 100 * time.Millisecond, ModoCaida: ModoCaidaNinguna})
	inicio := time.Now()
	status, _ := enviar(t, servidor.Client(), servidor.URL, ordenJSON(uuid.NewString()))
	transcurrido := time.Since(inicio)
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200", status)
	}
	if transcurrido < 100*time.Millisecond {
		t.Errorf("transcurrido = %v, quiero al menos la latencia de 100ms", transcurrido)
	}
}

func TestModoConexionRechazadaDaErrorDeTransporteSinRegistrar(t *testing.T) {
	t.Parallel()

	servidor, simulador := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaConexionRechazada})
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPost, servidor.URL+"/transferencias",
		strings.NewReader(ordenJSON(uuid.NewString())))
	if err != nil {
		t.Fatalf("armar el envío: %v", err)
	}
	if respuesta, err := servidor.Client().Do(peticion); err == nil {
		_ = respuesta.Body.Close()
		t.Fatal("la conexión rechazada devolvió respuesta, quiero error de transporte")
	}
	operaciones, err := simulador.almacen.Operaciones(t.Context())
	if err != nil {
		t.Fatalf("Operaciones: %v", err)
	}
	if len(operaciones) != 0 {
		t.Errorf("operaciones = %d, quiero 0: una caída no registra nada", len(operaciones))
	}
}

func TestModoSilencioNoRespondeHastaVencerElPlazo(t *testing.T) {
	t.Parallel()

	servidor, simulador := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaSilencio, Latencia: 0})
	cliente := &http.Client{Timeout: 200 * time.Millisecond}
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPost, servidor.URL+"/transferencias",
		strings.NewReader(ordenJSON(uuid.NewString())))
	if err != nil {
		t.Fatalf("armar el envío: %v", err)
	}
	inicio := time.Now()
	if respuesta, err := cliente.Do(peticion); err == nil {
		_ = respuesta.Body.Close()
		t.Fatal("el silencio devolvió respuesta, quiero que el cliente venza su plazo")
	}
	if transcurrido := time.Since(inicio); transcurrido < 180*time.Millisecond {
		t.Errorf("transcurrido = %v, quiero que el silencio dure hasta el plazo del cliente", transcurrido)
	}
	operaciones, err := simulador.almacen.Operaciones(t.Context())
	if err != nil {
		t.Fatalf("Operaciones: %v", err)
	}
	if len(operaciones) != 0 {
		t.Errorf("operaciones = %d, quiero 0: el riel caído no procesa", len(operaciones))
	}
}

func TestRespuestaPerdidaProcesaSinResponder(t *testing.T) {
	t.Parallel()

	servidor, simulador := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna, PRespuestaPerdida: 1})
	id := uuid.NewString()
	cliente := &http.Client{Timeout: 200 * time.Millisecond}
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPost, servidor.URL+"/transferencias",
		strings.NewReader(ordenJSON(id)))
	if err != nil {
		t.Fatalf("armar el envío: %v", err)
	}
	if respuesta, err := cliente.Do(peticion); err == nil {
		_ = respuesta.Body.Close()
		t.Fatal("la respuesta perdida llegó al cliente, quiero que no llegue")
	}
	operacion, err := simulador.almacen.PorTransferencia(t.Context(), id)
	if err != nil {
		t.Fatalf("el simulador no registró la orden pese a perder la respuesta: %v", err)
	}
	if operacion.Estado != estadoConfirmada {
		t.Errorf("estado = %q, quiero confirmada: el riel procesó y solo perdió la respuesta", operacion.Estado)
	}
}

func TestControlCambiaLasPerillasEnCaliente(t *testing.T) {
	t.Parallel()

	servidor, _ := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna})
	nuevas := `{"p_rechazo":1,"latencia_ms":5,"variacion_ms":0,"modo_caida":"silencio","p_respuesta_perdida":0.5}`
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPut, servidor.URL+"/control", strings.NewReader(nuevas))
	if err != nil {
		t.Fatalf("armar el control: %v", err)
	}
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := servidor.Client().Do(peticion)
	if err != nil {
		t.Fatalf("cambiar el control: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quiero 200", respuesta.StatusCode)
	}
	var actuales perillasHTTP
	if err := json.NewDecoder(respuesta.Body).Decode(&actuales); err != nil {
		t.Fatalf("leer el control: %v", err)
	}
	if actuales.PRechazo != 1 || actuales.ModoCaida != string(ModoCaidaSilencio) || actuales.LatenciaMs != 5 {
		t.Errorf("perillas = %+v, quiero las recién fijadas", actuales)
	}

	peticionLectura, err := http.NewRequestWithContext(t.Context(), http.MethodGet, servidor.URL+"/control", nil)
	if err != nil {
		t.Fatalf("armar la lectura del control: %v", err)
	}
	lectura, err := servidor.Client().Do(peticionLectura)
	if err != nil {
		t.Fatalf("leer el control: %v", err)
	}
	defer func() { _ = lectura.Body.Close() }()
	var vigentes perillasHTTP
	if err := json.NewDecoder(lectura.Body).Decode(&vigentes); err != nil {
		t.Fatalf("leer el control: %v", err)
	}
	if vigentes.PRespuestaPerdida != 0.5 {
		t.Errorf("p_respuesta_perdida = %v, quiero 0.5 en caliente", vigentes.PRespuestaPerdida)
	}
}

func TestControlRechazaPerillasInvalidas(t *testing.T) {
	t.Parallel()

	servidor, _ := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna})
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPut, servidor.URL+"/control",
		strings.NewReader(`{"p_rechazo":2,"latencia_ms":0,"variacion_ms":0,"modo_caida":"ninguna","p_respuesta_perdida":0}`))
	if err != nil {
		t.Fatalf("armar el control: %v", err)
	}
	respuesta, err := servidor.Client().Do(peticion)
	if err != nil {
		t.Fatalf("cambiar el control: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, quiero 400", respuesta.StatusCode)
	}
}

func TestControlRegistraLosValoresDeLaCorrida(t *testing.T) {
	var buf bytes.Buffer
	anterior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(anterior) })

	servidor, _ := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna})
	cuerpo := `{"p_rechazo":0.25,"latencia_ms":10,"variacion_ms":0,"modo_caida":"conexion_rechazada","p_respuesta_perdida":0.75}`
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPut, servidor.URL+"/control", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("armar el control: %v", err)
	}
	respuesta, err := servidor.Client().Do(peticion)
	if err != nil {
		t.Fatalf("cambiar el control: %v", err)
	}
	t.Cleanup(func() { _ = respuesta.Body.Close() })

	salida := buf.String()
	for _, esperado := range []string{"p_rechazo=0.25", "modo_caida=conexion_rechazada", "p_respuesta_perdida=0.75", "latencia=10ms"} {
		if !strings.Contains(salida, esperado) {
			t.Errorf("el log de la corrida no registró %q: %s", esperado, salida)
		}
	}
}

func TestReenvioRespondeLoMismoSinDuplicar(t *testing.T) {
	t.Parallel()

	servidor, simulador := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna, PRechazo: 1})
	id := uuid.NewString()
	if _, cuerpo := enviar(t, servidor.Client(), servidor.URL, ordenJSON(id)); leerDesenlace(t, cuerpo).Resultado != estadoRechazada {
		t.Fatalf("primer envío = %s, quiero rechazada", cuerpo)
	}
	_, cuerpo := enviar(t, servidor.Client(), servidor.URL, ordenJSON(id))
	d := leerDesenlace(t, cuerpo)
	if d.Resultado != estadoRechazada || !d.Reintento {
		t.Errorf("reenvío = %+v, quiero rechazada con reintento", d)
	}
	operaciones, err := simulador.almacen.Operaciones(t.Context())
	if err != nil {
		t.Fatalf("Operaciones: %v", err)
	}
	if len(operaciones) != 1 {
		t.Errorf("operaciones = %d, quiero 1", len(operaciones))
	}
}

func TestReenvioConOtrosDatosDa409(t *testing.T) {
	t.Parallel()

	servidor, _ := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna})
	id := uuid.NewString()
	if status, cuerpo := enviar(t, servidor.Client(), servidor.URL, ordenJSON(id)); status != http.StatusOK {
		t.Fatalf("primer envío = %d, quiero 200 (%s)", status, cuerpo)
	}
	otra := `{"transferencia_id":"` + id + `","idempotency_key":"otra-clave","llave":"3001234567","monto_centavos":"1000000","moneda":"COP"}`
	status, cuerpo := enviar(t, servidor.Client(), servidor.URL, otra)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, quiero 409 (%s)", status, cuerpo)
	}
}

func TestOrdenInvalidaSeRechaza(t *testing.T) {
	t.Parallel()

	servidor, _ := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna})
	tests := map[string]string{
		"monto como número": `{"transferencia_id":"` + uuid.NewString() + `","idempotency_key":"k","llave":"300","monto_centavos":1000000,"moneda":"COP"}`,
		"sin idempotency":   `{"transferencia_id":"` + uuid.NewString() + `","llave":"300","monto_centavos":"1000000","moneda":"COP"}`,
		"monto cero":        `{"transferencia_id":"` + uuid.NewString() + `","idempotency_key":"k","llave":"300","monto_centavos":"0","moneda":"COP"}`,
	}
	for name, cuerpo := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			status, salida := enviar(t, servidor.Client(), servidor.URL, cuerpo)
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, quiero 400 (%s)", status, salida)
			}
		})
	}
}

func TestConsultaDesconocidaDa404(t *testing.T) {
	t.Parallel()

	servidor, _ := servidorDePrueba(t, Perillas{ModoCaida: ModoCaidaNinguna})
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodGet, servidor.URL+"/transferencias/"+uuid.NewString(), nil)
	if err != nil {
		t.Fatalf("armar la consulta de estado: %v", err)
	}
	respuesta, err := servidor.Client().Do(peticion)
	if err != nil {
		t.Fatalf("consultar estado: %v", err)
	}
	t.Cleanup(func() { _ = respuesta.Body.Close() })
	if respuesta.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, quiero 404", respuesta.StatusCode)
	}
}
