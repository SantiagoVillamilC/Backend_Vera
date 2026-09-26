package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// topeDePrueba es un tope corto para no esperar el de producción de 30s en las pruebas.
const topeDePrueba = 500 * time.Millisecond

// simuladorConPerillas levanta el simulador con las perillas dadas, un sorteo determinista y el tope de prueba.
func simuladorConPerillas(t *testing.T, p Perillas, sorteo func() float64) (*httptest.Server, *Simulador) {
	t.Helper()

	almacen, err := AbrirAlmacen(filepath.Join(t.TempDir(), "movimiento.db"))
	if err != nil {
		t.Fatalf("abrir almacén: %v", err)
	}
	t.Cleanup(func() { _ = almacen.Cerrar() })

	sim := NuevoSimulador(almacen, "simulador-custodia", NuevoControlPerillas(p))
	sim.sorteo = sorteo
	sim.topeEspera = topeDePrueba
	servidor := httptest.NewServer(sim.Rutas())
	t.Cleanup(servidor.Close)
	return servidor, sim
}

// simuladorConReadTimeout levanta el simulador con el ReadTimeout dado para probar el orden entre leer el cuerpo y dormir.
func simuladorConReadTimeout(t *testing.T, p Perillas, readTimeout time.Duration) (*httptest.Server, *Simulador) {
	t.Helper()

	almacen, err := AbrirAlmacen(filepath.Join(t.TempDir(), "movimiento.db"))
	if err != nil {
		t.Fatalf("abrir almacén: %v", err)
	}
	t.Cleanup(func() { _ = almacen.Cerrar() })

	sim := NuevoSimulador(almacen, "simulador-custodia", NuevoControlPerillas(p))
	sim.sorteo = func() float64 { return 0 }
	sim.topeEspera = topeDePrueba
	servidor := httptest.NewUnstartedServer(sim.Rutas())
	servidor.Config.ReadTimeout = readTimeout
	servidor.Start()
	t.Cleanup(servidor.Close)
	return servidor, sim
}

// notificarPorTramos manda cabeceras y cuerpo en dos escrituras separadas y devuelve la respuesta cruda.
func notificarPorTramos(t *testing.T, servidor *httptest.Server, referencia, monto string, espera time.Duration) string {
	t.Helper()

	conexion, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", strings.TrimPrefix(servidor.URL, "http://"))
	if err != nil {
		t.Fatalf("abrir conexión: %v", err)
	}
	defer func() { _ = conexion.Close() }()

	cuerpo := fmt.Sprintf(`{"referencia":%q,"monto_centavos":%q,"ocurrido_en":"2026-09-17T12:00:00Z"}`, referencia, monto)
	cabeceras := "POST /notificaciones/depositos HTTP/1.1\r\nHost: simulador\r\nContent-Type: application/json\r\nConnection: close\r\nContent-Length: " + strconv.Itoa(len(cuerpo)) + "\r\n\r\n"
	if _, err := conexion.Write([]byte(cabeceras)); err != nil {
		t.Fatalf("enviar cabeceras: %v", err)
	}
	time.Sleep(espera)
	if _, err := conexion.Write([]byte(cuerpo)); err != nil {
		t.Fatalf("enviar cuerpo: %v", err)
	}
	if err := conexion.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatalf("fijar plazo de lectura: %v", err)
	}
	datos, err := io.ReadAll(conexion)
	if err != nil {
		t.Fatalf("leer respuesta: %v", err)
	}
	return string(datos)
}

// esperarMovimientos espera hasta que el almacén tenga la cantidad de filas pedida.
func esperarMovimientos(t *testing.T, sim *Simulador, quiero int) {
	t.Helper()

	limite := time.Now().Add(2 * time.Second)
	for time.Now().Before(limite) {
		if filas := movimientosDe(t, sim); len(filas) == quiero {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("movimientos = %d, quiero %d", len(movimientosDe(t, sim)), quiero)
}

// HU-NF-09 Criterio 17.1: con las probabilidades en cero el simulador nunca falla, ni con el peor sorteo.
func TestHU_NF_09_Criterio17_ProbabilidadCeroNuncaFalla(t *testing.T) {
	servidor, sim := simuladorConPerillas(t, perillasFieles(), func() float64 { return 0.999999 })

	for i := range 20 {
		referencia := fmt.Sprintf("dep-%d", i)
		status, movimiento := notificar(t, servidor, "/notificaciones/depositos", referencia, "1000", "2026-09-17T12:00:00Z")
		if status != http.StatusOK {
			t.Fatalf("depósito %d: status = %d, quiero 200", i, status)
		}
		if movimiento.Reintento {
			t.Errorf("depósito %d llegó como reintento", i)
		}
	}
	if filas := movimientosDe(t, sim); len(filas) != 20 {
		t.Errorf("movimientos = %d, quiero 20", len(filas))
	}
	if status, _ := extraer(t, servidor, "2026-09-17T12:00:01Z"); status != http.StatusOK {
		t.Errorf("extracto: status = %d, quiero 200", status)
	}
}

// HU-NF-09 Criterio 17.1: con p_rechazo en 1 toda ruta de negocio rechaza, aun con el sorteo casi 1.
func TestHU_NF_09_Criterio17_RechazoUnoRechazaSiempre(t *testing.T) {
	p := perillasFieles()
	p.PRechazo = 1
	servidor, _ := simuladorConPerillas(t, p, func() float64 { return 0.999999 })

	cuerpo := `{"referencia":"dep-1","monto_centavos":"1000","ocurrido_en":"2026-09-17T12:00:00Z"}`
	for i := range 5 {
		status, datos := pedir(t, servidor, http.MethodPost, "/notificaciones/depositos", cuerpo)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("intento %d: status = %d, quiero 503 (%s)", i, status, datos)
		}
	}
	if status, _ := pedir(t, servidor, http.MethodGet, "/extracto?corte_en=2026-09-17T12:00:01Z", ""); status != http.StatusServiceUnavailable {
		t.Errorf("extracto: status = %d, quiero 503", status)
	}
	if status, _ := pedir(t, servidor, http.MethodGet, "/control/perillas", ""); status != http.StatusOK {
		t.Errorf("control: status = %d, quiero 200 para poder apagar la falla en caliente", status)
	}
}

// HU-NF-09 Criterio 17.1: la respuesta perdida registra el movimiento y no responde, así el cliente ve timeout.
func TestHU_NF_09_Criterio17_RespuestaPerdidaRegistraYNoResponde(t *testing.T) {
	p := perillasFieles()
	p.PRespuestaPerdida = 1
	servidor, sim := simuladorConPerillas(t, p, func() float64 { return 0.999999 })

	cliente := &http.Client{Timeout: 200 * time.Millisecond}
	cuerpo := `{"referencia":"dep-perdida","monto_centavos":"250000","ocurrido_en":"2026-09-17T12:00:00Z"}`
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPost, servidor.URL+"/notificaciones/depositos", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("armar petición: %v", err)
	}
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := cliente.Do(peticion)
	if err == nil {
		_ = respuesta.Body.Close()
		t.Fatal("el cliente debía vencer por timeout y el simulador respondió")
	}
	esperarMovimientos(t, sim, 1)

	status, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
	if status != http.StatusOK || corte.SaldoCentavos != "250000" {
		t.Errorf("extracto = %d %+v, quiero saldo 250000 porque el movimiento sí quedó", status, corte)
	}
}

// HU-NF-09 Criterio 17.1: la notificación perdida no registra, no mueve el saldo y responde aceptación sin id.
func TestHU_NF_09_Criterio17_NotificacionPerdidaDescartaDeVerdad(t *testing.T) {
	p := perillasFieles()
	p.PNotificacionPerdida = 1
	servidor, sim := simuladorConPerillas(t, p, func() float64 { return 0.999999 })

	status, datos := pedir(t, servidor, http.MethodPost, "/notificaciones/depositos",
		`{"referencia":"dep-perdida","monto_centavos":"250000","ocurrido_en":"2026-09-17T12:00:00Z"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 de aceptación (%s)", status, datos)
	}
	if !strings.Contains(string(datos), `"descartada":true`) {
		t.Errorf("la respuesta no marca la notificación descartada: %s", datos)
	}
	if strings.Contains(string(datos), `"id"`) {
		t.Errorf("la respuesta descartada no debe traer identificador de movimiento: %s", datos)
	}
	if filas := movimientosDe(t, sim); len(filas) != 0 {
		t.Errorf("movimientos = %d, quiero 0: el movimiento no debe quedar", len(filas))
	}
	status, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
	if status != http.StatusOK || corte.SaldoCentavos != "0" {
		t.Errorf("extracto = %d %+v, quiero saldo 0 porque la notificación se descartó", status, corte)
	}
}

// HU-NF-09 Criterio 17.2: el modo de caída deja el servicio sin atender y el control lo devuelve.
func TestHU_NF_09_Criterio17_ModoCaidaDejaSinAtenderYVuelve(t *testing.T) {
	tests := map[string]string{
		"rechaza":  modoRechaza,
		"silencio": modoSilencio,
	}

	for nombre, modo := range tests {
		t.Run(nombre, func(t *testing.T) {
			p := perillasFieles()
			p.ModoCaida = modo
			servidor, _ := simuladorConPerillas(t, p, func() float64 { return 0.5 })

			cliente := &http.Client{Timeout: 200 * time.Millisecond}
			peticion, err := http.NewRequestWithContext(t.Context(), http.MethodGet, servidor.URL+"/extracto?corte_en=2026-09-17T12:00:01Z", nil)
			if err != nil {
				t.Fatalf("armar petición: %v", err)
			}
			respuesta, err := cliente.Do(peticion)
			if err == nil {
				_ = respuesta.Body.Close()
				t.Fatal("con el modo de caída activo el cliente no debía recibir respuesta")
			}

			status, datos := pedir(t, servidor, http.MethodPut, "/control/perillas", `{"modo_caida":"ninguno"}`)
			if status != http.StatusOK {
				t.Fatalf("el control no pudo apagar la caída: status = %d (%s)", status, datos)
			}
			status, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
			if status != http.StatusOK {
				t.Fatalf("tras salir del modo de caída el extracto no atiende: status = %d", status)
			}
			if corte.SaldoCentavos != "0" {
				t.Errorf("saldo = %q, quiero 0", corte.SaldoCentavos)
			}
		})
	}
}

// HU-NF-09 Criterio 17.2: el control lee y cambia las seis perillas en caliente y rechaza valores inválidos.
func TestHU_NF_09_Criterio17_ControlCambiaPerillasEnCaliente(t *testing.T) {
	servidor, _ := simuladorConPerillas(t, perillasFieles(), func() float64 { return 0.5 })

	var vigentes Perillas
	status, datos := pedir(t, servidor, http.MethodGet, "/control/perillas", "")
	if status != http.StatusOK {
		t.Fatalf("GET control: status = %d", status)
	}
	if err := json.Unmarshal(datos, &vigentes); err != nil {
		t.Fatalf("leer perillas: %v (%s)", err, datos)
	}
	if vigentes != perillasFieles() {
		t.Errorf("perillas iniciales = %+v, quiero las fieles", vigentes)
	}

	status, datos = pedir(t, servidor, http.MethodPut, "/control/perillas", `{"p_rechazo":0.25,"modo_caida":"silencio"}`)
	if status != http.StatusOK {
		t.Fatalf("PUT control: status = %d (%s)", status, datos)
	}
	if err := json.Unmarshal(datos, &vigentes); err != nil {
		t.Fatalf("leer perillas resultantes: %v", err)
	}
	if vigentes.PRechazo != 0.25 || vigentes.ModoCaida != modoSilencio || vigentes.LatenciaMS != 0 {
		t.Errorf("perillas resultantes = %+v, quiero p_rechazo 0.25 y modo silencio sin tocar el resto", vigentes)
	}

	invalido := map[string]string{
		"probabilidad fuera de rango":  `{"p_rechazo":2}`,
		"probabilidad negativa":        `{"p_notificacion_perdida":-1}`,
		"modo desconocido":             `{"modo_caida":"explota"}`,
		"latencia negativa":            `{"latencia_ms":-3}`,
		"latencia fuera del tope":      `{"latencia_ms":999999999}`,
		"suma de latencias sobre tope": `{"latencia_ms":20000,"latencia_var_ms":20000}`,
		"campo desconocido":            `{"p_desconocida":1}`,
		"cuerpo no json":               `no-json`,
		"datos sobrantes tras el json": `{"latencia_ms":1}basura`,
		"segundo objeto json":          `{"latencia_ms":1}{"latencia_ms":2}`,
	}
	for nombre, cuerpo := range invalido {
		t.Run(nombre, func(t *testing.T) {
			status, datos := pedir(t, servidor, http.MethodPut, "/control/perillas", cuerpo)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, quiero 400 (%s)", status, datos)
			}
			_, actual := pedir(t, servidor, http.MethodGet, "/control/perillas", "")
			var p Perillas
			if err := json.Unmarshal(actual, &p); err != nil {
				t.Fatalf("leer perillas: %v", err)
			}
			if p.PRechazo != 0.25 || p.ModoCaida != modoSilencio {
				t.Errorf("el cambio inválido alteró la vigente: %+v", p)
			}
		})
	}

	status, datos = pedir(t, servidor, http.MethodPut, "/control/perillas", `{"latencia_ms":120,"latencia_var_ms":80}`)
	if status != http.StatusOK {
		t.Fatalf("latencia dentro del tope: status = %d (%s)", status, datos)
	}
	var conLatencia Perillas
	if err := json.Unmarshal(datos, &conLatencia); err != nil {
		t.Fatalf("leer perillas con latencia: %v", err)
	}
	if conLatencia.LatenciaMS != 120 || conLatencia.LatenciaVarMS != 80 {
		t.Errorf("latencia dentro del tope no se aceptó: %+v", conLatencia)
	}

	status, datos = pedir(t, servidor, http.MethodPut, "/control/perillas", `{"p_rechazo":1,"modo_caida":"ninguno"}`)
	if status != http.StatusOK {
		t.Fatalf("PUT control: status = %d (%s)", status, datos)
	}
	status, _ = pedir(t, servidor, http.MethodPost, "/notificaciones/depositos",
		`{"referencia":"dep-1","monto_centavos":"1000","ocurrido_en":"2026-09-17T12:00:00Z"}`)
	if status != http.StatusServiceUnavailable {
		t.Errorf("el cambio en caliente no surtió efecto: status = %d, quiero 503", status)
	}
}

// HU-NF-09 Criterio 17.1: la latencia base se suma antes de responder y la variación añade jitter.
func TestHU_NF_09_Criterio17_LatenciaConVariacion(t *testing.T) {
	p := perillasFieles()
	p.LatenciaMS = 120
	servidor, _ := simuladorConPerillas(t, p, func() float64 { return 0 })

	inicio := time.Now()
	if status, _ := extraer(t, servidor, "2026-09-17T12:00:01Z"); status != http.StatusOK {
		t.Fatalf("extracto: status = %d, quiero 200", status)
	}
	if transcurrido := time.Since(inicio); transcurrido < 100*time.Millisecond {
		t.Errorf("el extracto tardó %v, quiero al menos la latencia base de 120ms", transcurrido)
	}

	pJitter := perillasFieles()
	pJitter.LatenciaMS = 20
	pJitter.LatenciaVarMS = 200
	servidorJitter, _ := simuladorConPerillas(t, pJitter, func() float64 { return 0.5 })

	inicio = time.Now()
	if status, _ := extraer(t, servidorJitter, "2026-09-17T12:00:01Z"); status != http.StatusOK {
		t.Fatalf("extracto con jitter: status = %d, quiero 200", status)
	}
	if transcurrido := time.Since(inicio); transcurrido < 100*time.Millisecond || transcurrido > 400*time.Millisecond {
		t.Errorf("con jitter 0.5 sobre 200ms el extracto tardó %v, quiero ~120ms", transcurrido)
	}
}

// HU-NF-09 Criterio 17.1: las seis perillas se leen del entorno y un valor inválido detiene el arranque.
func TestCargarPerillasDesdeEntorno(t *testing.T) {
	tests := map[string]struct {
		override map[string]string
		want     Perillas
		wantErr  bool
	}{
		"sin variables queda fiel": {want: perillasFieles()},
		"todas explícitas": {
			override: map[string]string{
				"SIMULADOR_CUSTODIA_P_RECHAZO":              "0.1",
				"SIMULADOR_CUSTODIA_LATENCIA_MS":            "30",
				"SIMULADOR_CUSTODIA_LATENCIA_VAR_MS":        "10",
				"SIMULADOR_CUSTODIA_MODO_CAIDA":             "rechaza",
				"SIMULADOR_CUSTODIA_P_RESPUESTA_PERDIDA":    "0.2",
				"SIMULADOR_CUSTODIA_P_NOTIFICACION_PERDIDA": "0.3",
			},
			want: Perillas{PRechazo: 0.1, LatenciaMS: 30, LatenciaVarMS: 10, ModoCaida: modoRechaza, PRespuestaPerdida: 0.2, PNotificacionPerdida: 0.3},
		},
		"probabilidad fuera de rango": {override: map[string]string{"SIMULADOR_CUSTODIA_P_RECHAZO": "1.5"}, wantErr: true},
		"modo desconocido":            {override: map[string]string{"SIMULADOR_CUSTODIA_MODO_CAIDA": "explota"}, wantErr: true},
		"latencia negativa":           {override: map[string]string{"SIMULADOR_CUSTODIA_LATENCIA_MS": "-1"}, wantErr: true},
		"no numérico":                 {override: map[string]string{"SIMULADOR_CUSTODIA_P_RECHAZO": "mucho"}, wantErr: true},
		"latencia fuera del tope":     {override: map[string]string{"SIMULADOR_CUSTODIA_LATENCIA_MS": "999999999"}, wantErr: true},
		"variación fuera del tope":    {override: map[string]string{"SIMULADOR_CUSTODIA_LATENCIA_VAR_MS": "999999999"}, wantErr: true},
		"latencia desbordada":         {override: map[string]string{"SIMULADOR_CUSTODIA_LATENCIA_MS": "9223372036854775807"}, wantErr: true},
		"suma fuera del tope": {
			override: map[string]string{"SIMULADOR_CUSTODIA_LATENCIA_MS": "20000", "SIMULADOR_CUSTODIA_LATENCIA_VAR_MS": "20000"},
			wantErr:  true,
		},
		"latencia justo en el tope": {override: map[string]string{"SIMULADOR_CUSTODIA_LATENCIA_MS": fmt.Sprint(maximoLatenciaMS)}, want: Perillas{LatenciaMS: maximoLatenciaMS, ModoCaida: modoNinguno}},
	}

	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			for _, clave := range todasLasClaves {
				t.Setenv(clave, "")
			}
			for clave, valor := range tc.override {
				t.Setenv(clave, valor)
			}

			p, errs := cargarPerillas()
			if tc.wantErr {
				if len(errs) == 0 {
					t.Fatalf("cargarPerillas = %+v, quiero errores", p)
				}
				return
			}
			if len(errs) > 0 {
				t.Fatalf("cargarPerillas: %v", errs)
			}
			if p != tc.want {
				t.Errorf("perillas = %+v, quiero %+v", p, tc.want)
			}
		})
	}
}

// bufferSeguro es el registro de stderr que el proceso hijo escribe desde otra goroutine.
type bufferSeguro struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *bufferSeguro) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *bufferSeguro) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// arrancarSimuladorConLog arranca el proceso con las perillas dadas y captura su stderr estructurado.
func arrancarSimuladorConLog(t *testing.T, puerto, ruta string, perillas map[string]string) (*exec.Cmd, *bufferSeguro) {
	t.Helper()

	// t.Context() cubre la vida completa del subproceso: solo se cancela después de que el
	// cuerpo del test retorna (y con él, matarSimulador vía defer/Cleanup ya lo mató).
	cmd := exec.CommandContext(t.Context(), os.Args[0])
	cmd.Env = append(os.Environ(),
		envServidorDePrueba+"=1",
		"SIMULADOR_CUSTODIA_ADDR=127.0.0.1",
		"SIMULADOR_CUSTODIA_PORT="+puerto,
		"SIMULADOR_CUSTODIA_DB="+ruta,
	)
	for clave, valor := range perillas {
		cmd.Env = append(cmd.Env, clave+"="+valor)
	}
	registro := &bufferSeguro{}
	cmd.Stdout, cmd.Stderr = registro, registro
	if err := cmd.Start(); err != nil {
		t.Fatalf("arrancar simulador: %v", err)
	}
	esperarControl(t, cmd, puerto)
	return cmd, registro
}

// esperarControl sondea el endpoint de control, exento de las fallas, hasta que el proceso responde.
func esperarControl(t *testing.T, cmd *exec.Cmd, puerto string) {
	t.Helper()

	cliente := &http.Client{Timeout: 500 * time.Millisecond}
	limite := time.Now().Add(10 * time.Second)
	for time.Now().Before(limite) {
		peticion, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:"+puerto+"/control/perillas", nil)
		if err != nil {
			t.Fatalf("armar petición: %v", err)
		}
		respuesta, err := cliente.Do(peticion)
		if err == nil {
			_ = respuesta.Body.Close()
			if respuesta.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	matarSimulador(t, cmd)
	t.Fatal("el control no respondió en el plazo")
}

// esperarEnLog espera hasta que el registro contenga la aguja o vence el plazo.
func esperarEnLog(t *testing.T, registro *bufferSeguro, aguja string) {
	t.Helper()

	limite := time.Now().Add(5 * time.Second)
	for time.Now().Before(limite) {
		if strings.Contains(registro.String(), aguja) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("el log no registró %q:\n%s", aguja, registro.String())
}

// HU-NF-09 Criterio 17.3: las perillas de la corrida quedan en el log al arrancar y en cada cambio.
func TestHU_NF_09_Criterio17_3_LasPerillasQuedanEnElLog(t *testing.T) {
	puerto := puertoLibre(t)
	ruta := filepath.Join(t.TempDir(), "movimiento.db")
	cmd, registro := arrancarSimuladorConLog(t, puerto, ruta, map[string]string{
		"SIMULADOR_CUSTODIA_P_RECHAZO":              "0.25",
		"SIMULADOR_CUSTODIA_MODO_CAIDA":             "silencio",
		"SIMULADOR_CUSTODIA_LATENCIA_MS":            "30",
		"SIMULADOR_CUSTODIA_P_NOTIFICACION_PERDIDA": "0.5",
	})
	defer matarSimulador(t, cmd)

	esperarEnLog(t, registro, "perillas.p_rechazo=0.25")
	esperarEnLog(t, registro, "perillas.modo_caida=silencio")
	esperarEnLog(t, registro, "perillas.latencia_ms=30")
	esperarEnLog(t, registro, "perillas.p_notificacion_perdida=0.5")

	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPut, "http://127.0.0.1:"+puerto+"/control/perillas",
		strings.NewReader(`{"modo_caida":"rechaza","p_rechazo":1}`))
	if err != nil {
		t.Fatalf("armar petición: %v", err)
	}
	respuesta, err := http.DefaultClient.Do(peticion)
	if err != nil {
		t.Fatalf("cambiar perillas: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode != http.StatusOK {
		t.Fatalf("cambiar perillas: status = %d, quiero 200", respuesta.StatusCode)
	}

	esperarEnLog(t, registro, "perillas cambiadas por el control")
	esperarEnLog(t, registro, "perillas.modo_caida=rechaza")
	esperarEnLog(t, registro, "perillas.p_rechazo=1")
}

// H3: el control responde el detalle de validación en español, como el resto del simulador.
func TestHU_NF_09_Criterio17_ControlRespondeValidacionEnEspanol(t *testing.T) {
	servidor, _ := simuladorConPerillas(t, perillasFieles(), func() float64 { return 0.5 })

	status, datos := pedir(t, servidor, http.MethodPut, "/control/perillas", `{"latencia_ms":-3}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, quiero 400 (%s)", status, datos)
	}
	if !strings.Contains(string(datos), "latencia_ms") || !strings.Contains(string(datos), "debe") {
		t.Errorf("el detalle de validación no llegó en español: %s", datos)
	}
}

// H4: el error del tope de latencia nombra solo los campos que de verdad lo exceden.
func TestHU_NF_09_Criterio17_ErrorDeLatenciaNombraSoloElExcedido(t *testing.T) {
	servidor, _ := simuladorConPerillas(t, perillasFieles(), func() float64 { return 0.5 })

	casos := map[string]struct {
		cuerpo string
		quiere []string
		niega  []string
	}{
		"solo la base":      {cuerpo: `{"latencia_ms":999999999}`, quiere: []string{"latencia_ms=999999999"}, niega: []string{"latencia_var_ms"}},
		"solo la variación": {cuerpo: `{"latencia_var_ms":999999999}`, quiere: []string{"latencia_var_ms=999999999"}, niega: []string{"latencia_ms="}},
		"la suma":           {cuerpo: `{"latencia_ms":20000,"latencia_var_ms":20000}`, quiere: []string{"20000", "20000"}},
	}
	for nombre, tc := range casos {
		t.Run(nombre, func(t *testing.T) {
			status, datos := pedir(t, servidor, http.MethodPut, "/control/perillas", tc.cuerpo)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, quiero 400 (%s)", status, datos)
			}
			for _, quiere := range tc.quiere {
				if !strings.Contains(string(datos), quiere) {
					t.Errorf("el mensaje no nombra %q: %s", quiere, datos)
				}
			}
			for _, niega := range tc.niega {
				if strings.Contains(string(datos), niega) {
					t.Errorf("el mensaje nombra de más %q: %s", niega, datos)
				}
			}
		})
	}
}

// H1: al vencer el tope con p_respuesta_perdida el cliente no recibe un 200 vacío, sino la conexión cortada o un 504.
func TestHU_NF_09_Criterio17_RespuestaPerdidaNoDejaUn200Vacio(t *testing.T) {
	p := perillasFieles()
	p.PRespuestaPerdida = 1
	servidor, sim := simuladorConPerillas(t, p, func() float64 { return 0.999999 })

	cliente := &http.Client{Timeout: topeDePrueba + 2*time.Second}
	cuerpo := `{"referencia":"dep-tope","monto_centavos":"250000","ocurrido_en":"2026-09-17T12:00:00Z"}`
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPost, servidor.URL+"/notificaciones/depositos", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("armar petición: %v", err)
	}
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := cliente.Do(peticion)
	if err == nil {
		defer func() { _ = respuesta.Body.Close() }()
		datos, _ := io.ReadAll(respuesta.Body)
		if respuesta.StatusCode == http.StatusOK && len(datos) == 0 {
			t.Fatal("el cliente recibió un 200 vacío que leería como entrega exitosa")
		}
		if respuesta.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, quiero conexión cortada o 504", respuesta.StatusCode)
		}
	}
	esperarMovimientos(t, sim, 1)
}

// H1: al vencer el tope con modo_caida=silencio el lector no recibe un 200 vacío, sino la conexión cortada o un 504.
func TestHU_NF_09_Criterio17_SilencioNoDejaUn200Vacio(t *testing.T) {
	p := perillasFieles()
	p.ModoCaida = modoSilencio
	servidor, _ := simuladorConPerillas(t, p, func() float64 { return 0.5 })

	cliente := &http.Client{Timeout: topeDePrueba + 2*time.Second}
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodGet, servidor.URL+"/extracto?corte_en=2026-09-17T12:00:01Z", nil)
	if err != nil {
		t.Fatalf("armar petición: %v", err)
	}
	respuesta, err := cliente.Do(peticion)
	if err == nil {
		defer func() { _ = respuesta.Body.Close() }()
		datos, _ := io.ReadAll(respuesta.Body)
		if respuesta.StatusCode == http.StatusOK && len(datos) == 0 {
			t.Fatal("el cliente recibió un 200 vacío que leería como corte válido")
		}
		if respuesta.StatusCode != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, quiero conexión cortada o 504", respuesta.StatusCode)
		}
	}
}

// H1: con un cliente de plazo menor que el tope, vence el cliente por su propio timeout y no responde el servidor.
func TestHU_NF_09_Criterio17_ClienteConPlazoMenorVencePrimero(t *testing.T) {
	p := perillasFieles()
	p.PRespuestaPerdida = 1
	servidor, sim := simuladorConPerillas(t, p, func() float64 { return 0.999999 })
	sim.topeEspera = topeEsperaSilencio

	cliente := &http.Client{Timeout: 200 * time.Millisecond}
	cuerpo := `{"referencia":"dep-plazo","monto_centavos":"1000","ocurrido_en":"2026-09-17T12:00:00Z"}`
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPost, servidor.URL+"/notificaciones/depositos", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("armar petición: %v", err)
	}
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := cliente.Do(peticion)
	if err == nil {
		_ = respuesta.Body.Close()
		t.Fatal("el simulador respondió antes del plazo del cliente")
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) || !urlErr.Timeout() {
		t.Fatalf("el error no fue el timeout del cliente: %v", err)
	}
	esperarMovimientos(t, sim, 1)
}

// H1: con la latencia en el tope, cabeceras y cuerpo en tramos separados, el depósito se lee, registra y responde 200.
func TestHU_NF_09_Criterio17_LatenciaEnElTopeNoRompeLaLectura(t *testing.T) {
	p := perillasFieles()
	p.LatenciaMS = maximoLatenciaMS
	servidor, sim := simuladorConReadTimeout(t, p, 15*time.Second)

	respuesta := notificarPorTramos(t, servidor, "dep-latencia-tope", "250000", 100*time.Millisecond)
	if !strings.HasPrefix(respuesta, "HTTP/1.1 200 OK") {
		t.Fatalf("respuesta = %q, quiero 200 con la latencia en el tope y el cuerpo en dos tramos", respuesta)
	}
	esperarMovimientos(t, sim, 1)
}

// H1: aunque la latencia supere el ReadTimeout, se lee el cuerpo primero y el depósito responde 200.
func TestHU_NF_09_Criterio17_LatenciaNoPrecedeALaLectura(t *testing.T) {
	p := perillasFieles()
	p.LatenciaMS = 1000
	servidor, sim := simuladorConReadTimeout(t, p, 200*time.Millisecond)

	respuesta := notificarPorTramos(t, servidor, "dep-latencia-orden", "1000", 50*time.Millisecond)
	if !strings.HasPrefix(respuesta, "HTTP/1.1 200 OK") {
		t.Fatalf("respuesta = %q, quiero 200 porque la lectura precede a la latencia", respuesta)
	}
	esperarMovimientos(t, sim, 1)
}

// H2: el silencio del servidor no retiene la conexión más allá de su tope aunque el cliente no se rinda.
func TestHU_NF_09_Criterio17_SilencioNoRetieneMasAllaDelTope(t *testing.T) {
	p := perillasFieles()
	p.ModoCaida = modoSilencio
	servidor, sim := simuladorConPerillas(t, p, func() float64 { return 0.5 })

	conexion, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", strings.TrimPrefix(servidor.URL, "http://"))
	if err != nil {
		t.Fatalf("abrir conexión: %v", err)
	}
	defer func() { _ = conexion.Close() }()

	if _, err := conexion.Write([]byte("GET /extracto?corte_en=2026-09-17T12:00:01Z HTTP/1.1\r\nHost: simulador\r\n\r\n")); err != nil {
		t.Fatalf("enviar petición: %v", err)
	}

	inicio := time.Now()
	if err := conexion.SetReadDeadline(time.Now().Add(sim.topeEspera + 3*time.Second)); err != nil {
		t.Fatalf("fijar plazo de lectura: %v", err)
	}
	datos, err := io.ReadAll(conexion)
	if err != nil {
		t.Fatalf("el servidor no liberó la conexión: %v", err)
	}
	if len(datos) > 0 {
		t.Fatalf("el servidor respondió %q, quiero la conexión cortada sin cuerpo", datos)
	}
	if transcurrido := time.Since(inicio); transcurrido > sim.topeEspera+2*time.Second {
		t.Errorf("el servidor retuvo la conexión %v, quiero a lo sumo %v", transcurrido, sim.topeEspera)
	}
}

// H4: con el silencio activo, una conexión que no se rinde no impide el apagado ordenado.
func TestHU_NF_09_Criterio17_ApagadoOrdenadoNoEsperaAlSilencio(t *testing.T) {
	puerto := puertoLibre(t)
	ruta := filepath.Join(t.TempDir(), "movimiento.db")
	cmd, _ := arrancarSimuladorConLog(t, puerto, ruta, map[string]string{"SIMULADOR_CUSTODIA_MODO_CAIDA": "silencio"})

	conexion, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", "127.0.0.1:"+puerto)
	if err != nil {
		matarSimulador(t, cmd)
		t.Fatalf("abrir conexión: %v", err)
	}
	defer func() { _ = conexion.Close() }()
	if _, err := conexion.Write([]byte("GET /extracto?corte_en=2026-09-17T12:00:01Z HTTP/1.1\r\nHost: simulador\r\n\r\n")); err != nil {
		matarSimulador(t, cmd)
		t.Fatalf("enviar petición: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	inicio := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		matarSimulador(t, cmd)
		t.Fatalf("enviar señal de apagado: %v", err)
	}
	termino := make(chan error, 1)
	go func() { termino <- cmd.Wait() }()

	select {
	case <-termino:
	case <-time.After(3 * time.Second):
		matarSimulador(t, cmd)
		t.Fatalf("el apagado no terminó en 3s con una conexión en silencio")
	}
	if transcurrido := time.Since(inicio); transcurrido > 3*time.Second {
		t.Errorf("el apagado tardó %v, quiero menos de 3s", transcurrido)
	}
}

// H5: dos cambios concurrentes de campos distintos no se pierden ni se pisan.
func TestHU_NF_09_Criterio17_CambiosConcurrentesNoSePierden(t *testing.T) {
	servidor, _ := simuladorConPerillas(t, perillasFieles(), func() float64 { return 0.5 })

	const rondas = 40
	for i := range rondas {
		if status, datos := pedir(t, servidor, http.MethodPut, "/control/perillas", `{"p_rechazo":0,"modo_caida":"ninguno"}`); status != http.StatusOK {
			t.Fatalf("ronda %d: no se pudo reiniciar: status = %d (%s)", i, status, datos)
		}

		inicio := make(chan struct{})
		fallos := make(chan error, 2)
		var wg sync.WaitGroup
		for _, cuerpo := range []string{`{"p_rechazo":0.5}`, `{"modo_caida":"rechaza"}`} {
			wg.Add(1)
			go func(cuerpo string) {
				defer wg.Done()
				<-inicio
				peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPut, servidor.URL+"/control/perillas", strings.NewReader(cuerpo))
				if err != nil {
					fallos <- err
					return
				}
				peticion.Header.Set("Content-Type", "application/json")
				respuesta, err := servidor.Client().Do(peticion)
				if err != nil {
					fallos <- err
					return
				}
				defer func() { _ = respuesta.Body.Close() }()
				if respuesta.StatusCode != http.StatusOK {
					fallos <- fmt.Errorf("status %d", respuesta.StatusCode)
				}
			}(cuerpo)
		}
		close(inicio)
		wg.Wait()
		close(fallos)
		for fallo := range fallos {
			t.Fatalf("ronda %d: %v", i, fallo)
		}

		_, datos := pedir(t, servidor, http.MethodGet, "/control/perillas", "")
		var vigentes Perillas
		if err := json.Unmarshal(datos, &vigentes); err != nil {
			t.Fatalf("ronda %d: leer perillas: %v", i, err)
		}
		if vigentes.PRechazo != 0.5 || vigentes.ModoCaida != modoRechaza {
			t.Fatalf("ronda %d: un cambio concurrente se perdió: %+v", i, vigentes)
		}
	}
}
