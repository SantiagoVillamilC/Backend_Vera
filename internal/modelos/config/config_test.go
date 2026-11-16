package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func entorno(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestValoresPorDefectoSeguros(t *testing.T) {
	c, err := Cargar(entorno(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.GPUs) != 2 || c.GPUs[0] != "NVIDIA RTX A5000" || c.GPUs[1] != "NVIDIA RTX A4000" {
		t.Fatalf("gpus %v", c.GPUs)
	}
	if c.TarifaTopeUSDH != 0.35 || c.Inactividad != 10*time.Minute || c.SesionMaxima != 4*time.Hour {
		t.Fatalf("tope=%v inact=%v sesion=%v", c.TarifaTopeUSDH, c.Inactividad, c.SesionMaxima)
	}
	if c.Control != "127.0.0.1:8790" || c.EscuchaVoz != "127.0.0.1:8766" || c.EscuchaSTT != "127.0.0.1:8767" || c.EscuchaOllama != "127.0.0.1:11434" {
		t.Fatalf("escuchas %+v", c)
	}
	if !strings.HasPrefix(c.RunPodURL, "https://api.runpod.io/v2") || c.NombrePod != "vera-modelos" {
		t.Fatalf("url=%s nombre=%s", c.RunPodURL, c.NombrePod)
	}
}

func TestSobrescribeDesdeElEntorno(t *testing.T) {
	c, err := Cargar(entorno(map[string]string{
		"MODELOS_GPUS": "NVIDIA RTX A4000, NVIDIA RTX A5000 ", "MODELOS_TARIFA_TOPE_USD_H": "0.30", "MODELOS_INACTIVIDAD": "15m",
		"MODELOS_SESION_MAXIMA": "2h", "MODELOS_DIR_ESTADO": "/tmp/x", "MODELOS_PUERTO_CONTROL": "127.0.0.1:9000"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.GPUs[0] != "NVIDIA RTX A4000" || c.GPUs[1] != "NVIDIA RTX A5000" || c.TarifaTopeUSDH != 0.30 || c.Inactividad != 15*time.Minute ||
		c.SesionMaxima != 2*time.Hour || c.DirEstado != "/tmp/x" || c.Control != "127.0.0.1:9000" {
		t.Fatalf("%+v", c)
	}
}

func TestRechazaConfiguracionPeligrosaYJuntaLosErrores(t *testing.T) {
	_, err := Cargar(entorno(map[string]string{
		"MODELOS_PUERTO_CONTROL": "0.0.0.0:8790", "MODELOS_ESCUCHA_VOZ": "8.8.8.8:8766", "MODELOS_TARIFA_TOPE_USD_H": "-1",
		"MODELOS_GPUS": "NVIDIA; rm -rf /", "MODELOS_INACTIVIDAD": "10 minutos", "MODELOS_SESION_MAXIMA": "1s",
		"MODELOS_RUNPOD_URL": "http://api.runpod.io/v2", "MODELOS_IMAGEN": "-malo"}))
	if err == nil {
		t.Fatal("esperaba error")
	}
	for _, fragmento := range []string{"MODELOS_PUERTO_CONTROL", "MODELOS_ESCUCHA_VOZ", "MODELOS_TARIFA_TOPE_USD_H", "MODELOS_GPUS", "MODELOS_INACTIVIDAD",
		"MODELOS_SESION_MAXIMA", "MODELOS_RUNPOD_URL", "MODELOS_IMAGEN"} {
		if !strings.Contains(err.Error(), fragmento) {
			t.Errorf("el error no menciona %s: %v", fragmento, err)
		}
	}
}

func TestRechazaPuertosRepetidos(t *testing.T) {
	_, err := Cargar(entorno(map[string]string{"MODELOS_ESCUCHA_VOZ": "127.0.0.1:8767"}))
	if err == nil || !strings.Contains(err.Error(), "repetid") {
		t.Fatalf("err=%v", err)
	}
}

func escribir(t *testing.T, modo os.FileMode, contenido string) string {
	t.Helper()
	ruta := filepath.Join(t.TempDir(), "secreto.env")
	if err := os.WriteFile(ruta, []byte(contenido), modo); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ruta, modo); err != nil {
		t.Fatal(err)
	}
	return ruta
}

func TestLeerClaveRunPodDeArchivoPrivado(t *testing.T) {
	ruta := escribir(t, 0o600, "# comentario\nexport RUNPOD_API_KEY=\"rpa_ABC123456789\"\nOTRA=1\n")
	clave, err := LeerClaveRunPod(ruta, entorno(nil))
	if err != nil || clave != "rpa_ABC123456789" {
		t.Fatalf("clave=%q err=%v", clave, err)
	}
}

func TestLeerSecretoRechazaPermisosAbiertos(t *testing.T) {
	ruta := escribir(t, 0o644, "RUNPOD_API_KEY=rpa_ABC123456789\n")
	if _, err := LeerClaveRunPod(ruta, entorno(nil)); err == nil || strings.Contains(err.Error(), "rpa_ABC") {
		t.Fatalf("err=%v", err)
	}
}

func TestLeerClaveRunPodSinArchivoUsaElEntornoSoloSiLaRutaEstaVacia(t *testing.T) {
	clave, err := LeerClaveRunPod("", entorno(map[string]string{"RUNPOD_API_KEY": "rpa_DESDE_ENTORNO"}))
	if err != nil || clave != "rpa_DESDE_ENTORNO" {
		t.Fatalf("clave=%q err=%v", clave, err)
	}
	if _, err := LeerClaveRunPod(filepath.Join(t.TempDir(), "no-existe"), entorno(map[string]string{"RUNPOD_API_KEY": "x"})); err == nil {
		t.Fatal("un archivo configurado que no existe debe fallar")
	}
}

func TestLeerTokensYTokenDeControl(t *testing.T) {
	stt, voz := strings.Repeat("s", 32), strings.Repeat("v", 40)
	ruta := escribir(t, 0o600, "VERA_STT_TOKEN="+stt+"\nVERA_VOZ_TOKEN='"+voz+"'\n")
	gotSTT, gotVoz, err := LeerTokensModelos(ruta)
	if err != nil || gotSTT != stt || gotVoz != voz {
		t.Fatalf("%q %q %v", gotSTT, gotVoz, err)
	}
	corto := escribir(t, 0o600, "VERA_STT_TOKEN=corto\nVERA_VOZ_TOKEN="+voz+"\n")
	if _, _, err := LeerTokensModelos(corto); err == nil {
		t.Fatal("un token de menos de 32 caracteres debe rechazarse")
	}
	token := escribir(t, 0o600, strings.Repeat("c", 40)+"\n")
	if got, err := LeerTokenControl(token); err != nil || got != strings.Repeat("c", 40) {
		t.Fatalf("%q %v", got, err)
	}
}

func TestResumenNoLeeSecretosNiLosImprime(t *testing.T) {
	c, err := Cargar(entorno(map[string]string{"MODELOS_ARCHIVO_CLAVE_RUNPOD": "/ruta/que/no/existe/runpod.env"}))
	if err != nil {
		t.Fatal(err)
	}
	salida := c.Resumen()
	var m map[string]any
	if err := json.Unmarshal([]byte(salida), &m); err != nil {
		t.Fatalf("el resumen no es json: %v", err)
	}
	if !strings.Contains(salida, "/ruta/que/no/existe/runpod.env") || !strings.Contains(salida, "vera-modelos") {
		t.Fatalf("resumen %s", salida)
	}
	for _, prohibido := range []string{"rpa_", "TOKEN=", "Bearer"} {
		if strings.Contains(salida, prohibido) {
			t.Fatalf("el resumen contiene %q", prohibido)
		}
	}
}

func TestLosProxysAdmitenLoopbackOUnaIPv4Privada(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "127.0.0.2", "10.0.0.5", "10.255.255.254", "172.16.0.1", "172.17.0.1", "172.31.255.254", "192.168.0.1", "192.168.255.254"} {
		for _, variable := range []string{"MODELOS_ESCUCHA_VOZ", "MODELOS_ESCUCHA_STT", "MODELOS_ESCUCHA_OLLAMA"} {
			c, err := Cargar(entorno(map[string]string{variable: ip + ":9001"}))
			if err != nil {
				t.Errorf("%s=%s debio aceptarse: %v", variable, ip, err)
				continue
			}
			if got := map[string]string{"MODELOS_ESCUCHA_VOZ": c.EscuchaVoz, "MODELOS_ESCUCHA_STT": c.EscuchaSTT, "MODELOS_ESCUCHA_OLLAMA": c.EscuchaOllama}[variable]; got != ip+":9001" {
				t.Errorf("%s: got %q", variable, got)
			}
		}
	}
}

func TestLosProxysRechazanTodoLoQueNoSeaLoopbackOPrivadaIPv4(t *testing.T) {
	rechazadas := map[string]string{
		"todas las interfaces IPv4": "0.0.0.0:8766", "todas las interfaces IPv6": "[::]:8766", "hostname local": "localhost:8766", "hostname": "vm.interna:8766",
		"publica": "8.8.8.8:8766", "publica cercana a 172.16": "172.15.255.255:8766", "publica cercana a 172.31": "172.32.0.1:8766",
		"publica cercana a 192.168": "192.169.0.1:8766", "publica cercana a 10": "11.0.0.1:8766", "link-local": "169.254.169.254:8766",
		"CGNAT": "100.64.0.1:8766", "IPv6 privada": "[fd00::1]:8766", "IPv4 mapeada en IPv6": "[::ffff:10.0.0.1]:8766", "sin puerto": "10.0.0.5",
		"puerto fuera de rango": "10.0.0.5:70000", "multicast": "224.0.0.1:8766", "broadcast": "255.255.255.255:8766",
	}
	for nombre, valor := range rechazadas {
		for _, variable := range []string{"MODELOS_ESCUCHA_VOZ", "MODELOS_ESCUCHA_STT", "MODELOS_ESCUCHA_OLLAMA"} {
			if _, err := Cargar(entorno(map[string]string{variable: valor})); err == nil || !strings.Contains(err.Error(), variable) {
				t.Errorf("%s (%s=%s) debio rechazarse, err=%v", nombre, variable, valor, err)
			}
		}
	}
}

func TestLaAPIDeControlYLosPuertosInternosSiguenSiendoSoloLoopback(t *testing.T) {
	for _, variable := range []string{"MODELOS_PUERTO_CONTROL", "MODELOS_INTERNO_VOZ", "MODELOS_INTERNO_STT", "MODELOS_INTERNO_OLLAMA"} {
		for _, privada := range []string{"10.0.0.5:9100", "172.17.0.1:9100", "192.168.1.5:9100"} {
			if _, err := Cargar(entorno(map[string]string{variable: privada})); err == nil || !strings.Contains(err.Error(), variable) {
				t.Errorf("%s=%s debio rechazarse, err=%v", variable, privada, err)
			}
		}
		if _, err := Cargar(entorno(map[string]string{variable: "127.0.0.1:9100"})); err != nil {
			t.Errorf("%s con loopback debio aceptarse: %v", variable, err)
		}
	}
}

func TestSinVariablesLosProxysEscuchanSoloEnLoopback(t *testing.T) {
	c, err := Cargar(entorno(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{c.EscuchaVoz, c.EscuchaSTT, c.EscuchaOllama, c.Control} {
		if !strings.HasPrefix(dir, "127.0.0.1:") {
			t.Errorf("el valor por defecto %s no es loopback", dir)
		}
	}
}

func TestRechazaDireccionDeProxyIgualAOtraPorOtraIP(t *testing.T) {
	_, err := Cargar(entorno(map[string]string{"MODELOS_ESCUCHA_VOZ": "172.17.0.1:8767", "MODELOS_ESCUCHA_STT": "172.17.0.1:8767"}))
	if err == nil || !strings.Contains(err.Error(), "repetid") {
		t.Fatalf("err=%v", err)
	}
}
