package custodiosim

import (
	"context"
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

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// TestLectorExtractoContraElSimuladorReal levanta el servicio, lo notifica y trae el corte con el cliente.
func TestLectorExtractoContraElSimuladorReal(t *testing.T) {
	if testing.Short() {
		t.Skip("construye el simulador y lo levanta como proceso")
	}
	bin := construirSimulador(t)
	puerto := puertoLibre(t)
	base := "http://127.0.0.1:" + puerto

	cmd := exec.CommandContext(t.Context(), bin)
	cmd.Env = append(os.Environ(),
		"SIMULADOR_CUSTODIA_ADDR=127.0.0.1",
		"SIMULADOR_CUSTODIA_PORT="+puerto,
		"SIMULADOR_CUSTODIA_DB="+filepath.Join(t.TempDir(), "movimiento.db"),
	)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("arrancar el simulador: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	esperarExtracto(t, base)
	ocurrido := time.Now().UTC().Add(-2 * time.Minute)
	notificarDeposito(t, base, "dep-e2e", "123456", ocurrido.Format(time.RFC3339Nano))
	corte := time.Now().UTC().Add(-time.Minute)

	extracto, err := NuevoLectorExtracto(base+"/extracto", nil).Extracto(context.Background(), corte)
	if err != nil {
		t.Fatalf("Extracto contra el servicio real: %v", err)
	}
	if !extracto.CorteEn.Equal(corte.Truncate(time.Nanosecond)) {
		t.Errorf("CorteEn = %v, quiero el corte pedido %v", extracto.CorteEn, corte)
	}
	if extracto.Saldo != nucleo.Centavos(123456) {
		t.Errorf("Saldo = %d, quiero 123456 (lo notificado)", extracto.Saldo)
	}
	if extracto.Origen == "" {
		t.Error("Origen llegó vacío")
	}
}

// construirSimulador compila el binario real del simulador desde el monorepo de simuladores
// o desde SIMULADOR_CUSTODIA_BIN; se salta la prueba si no encuentra la fuente.
func construirSimulador(t *testing.T) string {
	t.Helper()

	if bin := os.Getenv("SIMULADOR_CUSTODIA_BIN"); bin != "" {
		return bin
	}
	salida, err := exec.CommandContext(t.Context(), "go", "env", "GOMOD").Output()
	if err != nil {
		t.Skipf("sin toolchain de Go para construir el simulador: %v", err)
	}
	raiz := filepath.Dir(strings.TrimSpace(string(salida)))
	fuente := filepath.Join(raiz, "servicios", "simuladores", "cmd", "simulador-custodia")
	if _, err := os.Stat(filepath.Join(fuente, "main.go")); err != nil {
		t.Skip("el simulador de custodia vive en servicios/simuladores; define SIMULADOR_CUSTODIA_BIN para correr esta prueba")
	}
	bin := filepath.Join(t.TempDir(), "simulador-custodia")
	construir := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".")
	construir.Dir = fuente
	if compilado, err := construir.CombinedOutput(); err != nil {
		t.Fatalf("construir el simulador: %v\n%s", err, compilado)
	}
	return bin
}

// puertoLibre pide al sistema un puerto libre y lo devuelve como texto.
func puertoLibre(t *testing.T) string {
	t.Helper()

	escucha, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
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

// esperarExtracto sondea el servicio hasta que responda o venza el plazo.
func esperarExtracto(t *testing.T, base string) {
	t.Helper()

	limite := time.Now().Add(10 * time.Second)
	for time.Now().Before(limite) {
		peticion, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/extracto?corte_en="+time.Now().UTC().Format(time.RFC3339Nano), nil)
		if err == nil {
			respuesta, errPeticion := http.DefaultClient.Do(peticion)
			if errPeticion == nil {
				_ = respuesta.Body.Close()
				if respuesta.StatusCode == http.StatusOK {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("el simulador no respondió en el plazo")
}

// notificarDeposito manda una notificación válida al servicio real.
func notificarDeposito(t *testing.T, base, referencia, monto, ocurrido string) {
	t.Helper()

	cuerpo := fmt.Sprintf(`{"referencia":%q,"monto_centavos":%q,"ocurrido_en":%q}`, referencia, monto, ocurrido)
	peticion, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/notificaciones/depositos", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("armar la notificación: %v", err)
	}
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := http.DefaultClient.Do(peticion)
	if err != nil {
		t.Fatalf("notificar: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	datos, _ := io.ReadAll(respuesta.Body)
	if respuesta.StatusCode != http.StatusOK {
		t.Fatalf("notificar: status = %d, quiero 200 (%s)", respuesta.StatusCode, datos)
	}
}
