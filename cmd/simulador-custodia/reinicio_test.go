package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// envServidorDePrueba marca al proceso hijo que debe quedarse sirviendo el simulador.
const envServidorDePrueba = "SIMULADOR_CUSTODIA_SERVIDOR_DE_PRUEBA"

// TestMain convierte al binario de pruebas en el servidor cuando el padre lo pide.
func TestMain(m *testing.M) {
	if os.Getenv(envServidorDePrueba) == "1" {
		if err := run(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

// HU-NF-09 Criterio 10: con el proceso muerto y vuelto a levantar, el saldo sigue igual.
func TestHU_NF_09_Criterio10_ElSaldoSobreviveAlReinicio(t *testing.T) {
	puerto := puertoLibre(t)
	ruta := filepath.Join(t.TempDir(), "movimiento.db")

	primer := arrancarSimulador(t, puerto, ruta)
	notificarEnProceso(t, puerto, "dep-1", "250000")
	antes := saldoEnProceso(t, puerto)
	matarSimulador(t, primer)

	segundo := arrancarSimulador(t, puerto, ruta)
	defer matarSimulador(t, segundo)
	despues := saldoEnProceso(t, puerto)

	if antes != "250000" {
		t.Errorf("saldo antes del reinicio = %q, quiero 250000", antes)
	}
	if despues != antes {
		t.Errorf("saldo tras el reinicio = %q, quiero %q (el saldo sobrevive)", despues, antes)
	}
}

// puertoLibre pide al sistema un puerto libre y lo devuelve como texto.
func puertoLibre(t *testing.T) string {
	t.Helper()

	escucha, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("buscar puerto libre: %v", err)
	}
	defer func() { _ = escucha.Close() }()
	_, puerto, err := net.SplitHostPort(escucha.Addr().String())
	if err != nil {
		t.Fatalf("leer el puerto: %v", err)
	}
	return puerto
}

// arrancarSimulador ejecuta el binario de pruebas como servidor y espera a que responda.
func arrancarSimulador(t *testing.T, puerto, ruta string) *exec.Cmd {
	t.Helper()

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		envServidorDePrueba+"=1",
		"SIMULADOR_CUSTODIA_ADDR=127.0.0.1",
		"SIMULADOR_CUSTODIA_PORT="+puerto,
		"SIMULADOR_CUSTODIA_DB="+ruta,
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("arrancar simulador: %v", err)
	}

	esperarServidor(t, cmd, puerto)
	return cmd
}

// esperarServidor sondea el extracto hasta que el proceso responde o vence el plazo.
func esperarServidor(t *testing.T, cmd *exec.Cmd, puerto string) {
	t.Helper()

	limite := time.Now().Add(10 * time.Second)
	for time.Now().Before(limite) {
		if _, status, err := pedirExtracto(puerto); err == nil && status == http.StatusOK {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	matarSimulador(t, cmd)
	t.Fatal("el simulador no respondió en el plazo")
}

// matarSimulador mata el proceso de verdad, sin apagado ordenado.
func matarSimulador(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// notificarEnProceso manda una notificación al proceso y exige un 200.
func notificarEnProceso(t *testing.T, puerto, referencia, monto string) {
	t.Helper()

	cuerpo := fmt.Sprintf(`{"referencia":%q,"monto_centavos":%q,"ocurrido_en":%q}`,
		referencia, monto, time.Now().UTC().Format(time.RFC3339Nano))
	respuesta, err := http.Post("http://127.0.0.1:"+puerto+"/notificaciones/depositos",
		"application/json", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("notificar: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode != http.StatusOK {
		t.Fatalf("notificar: status = %d, quiero 200", respuesta.StatusCode)
	}
}

// saldoEnProceso pide el extracto al proceso y devuelve el saldo como cadena.
func saldoEnProceso(t *testing.T, puerto string) string {
	t.Helper()

	cuerpo, status, err := pedirExtracto(puerto)
	if err != nil {
		t.Fatalf("pedir extracto: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("pedir extracto: status = %d, quiero 200", status)
	}
	inicio := strings.Index(cuerpo, `"saldo_centavos":"`)
	if inicio < 0 {
		t.Fatalf("el extracto no trae saldo_centavos: %s", cuerpo)
	}
	resto := cuerpo[inicio+len(`"saldo_centavos":"`):]
	return resto[:strings.Index(resto, `"`)]
}

// pedirExtracto consulta el extracto al instante actual.
func pedirExtracto(puerto string) (string, int, error) {
	corte := time.Now().UTC().Format(time.RFC3339Nano)
	respuesta, err := http.Get("http://127.0.0.1:" + puerto + "/extracto?corte_en=" + corte)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = respuesta.Body.Close() }()
	datos, err := io.ReadAll(respuesta.Body)
	if err != nil {
		return "", 0, err
	}
	return string(datos), respuesta.StatusCode, nil
}
