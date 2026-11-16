package destinolocal

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func listaDePrueba(t *testing.T, valor string) []netip.Addr {
	t.Helper()
	l, err := ParsearPermitidos(valor)
	if err != nil {
		t.Fatalf("lista %q: %v", valor, err)
	}
	return l
}

func TestAceptableConLoopbackPorDefecto(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1", "http://127.0.0.1:8766", "http://127.0.0.1:1", "http://127.0.0.1:65535", "HTTP://127.0.0.1:80"} {
		if !AceptableCon(u, nil) {
			t.Errorf("%q debe aceptarse sin lista", u)
		}
	}
}

func TestAceptableConIPPrivadaSoloSiEstaEnLaLista(t *testing.T) {
	lista := listaDePrueba(t, "172.18.0.1")
	for _, u := range []string{"http://172.18.0.1:8766", "http://172.18.0.1:11434", "http://127.0.0.1:8766"} {
		if !AceptableCon(u, lista) {
			t.Errorf("%q debe aceptarse con la lista", u)
		}
	}
	for _, u := range []string{"http://172.18.0.2:8766", "http://172.18.0.1", "http://10.0.0.5:8766", "http://192.168.1.10:8766"} {
		if AceptableCon(u, lista) {
			t.Errorf("%q no debe aceptarse: sin puerto o fuera de la lista exacta", u)
		}
	}
}

func TestAceptableConSinListaNingunaPrivada(t *testing.T) {
	for _, u := range []string{"http://172.18.0.1:8766", "http://10.0.0.5:8766", "http://192.168.1.10:8766"} {
		if AceptableCon(u, nil) || AceptableCon(u, []netip.Addr{}) {
			t.Errorf("%q no debe aceptarse sin lista", u)
		}
	}
}

func TestAceptableConRechazos(t *testing.T) {
	lista := listaDePrueba(t, "172.18.0.1,10.1.2.3,192.168.0.9")
	casos := map[string]string{
		"vacia":              "",
		"hostname":           "http://localhost:8766",
		"hostnameDocker":     "http://host.docker.internal:8766",
		"dominio":            "http://example.com:8766",
		"https":              "https://172.18.0.1:8766",
		"httpsLoopback":      "https://127.0.0.1:8766",
		"sinEsquema":         "172.18.0.1:8766",
		"ftp":                "ftp://127.0.0.1",
		"ipv6Loopback":       "http://[::1]:8766",
		"ipv6Mapeada":        "http://[::ffff:172.18.0.1]:8766",
		"ipv6Zona":           "http://[fe80::1%25eth0]:8766",
		"cero":               "http://0.0.0.0:8766",
		"publica":            "http://8.8.8.8:8766",
		"linkLocal":          "http://169.254.169.254:80",
		"loopbackDistinto":   "http://127.0.0.2:8766",
		"loopbackCeros":      "http://127.000.000.001:8766",
		"privadaCeros":       "http://172.018.0.1:8766",
		"punto":              "http://172.18.0.1.:8766",
		"enteroDecimal":      "http://2886729729:8766",
		"hexadecimal":        "http://0xac120001:8766",
		"borde172_15":        "http://172.15.0.1:8766",
		"borde172_32":        "http://172.32.0.1:8766",
		"borde192_169":       "http://192.169.0.1:8766",
		"userinfo":           "http://usuario:clave@127.0.0.1:8766",
		"userinfoPrivada":    "http://usuario@172.18.0.1:8766",
		"ruta":               "http://127.0.0.1:8766/sintetizar",
		"rutaRaiz":           "http://127.0.0.1:8766/",
		"consulta":           "http://127.0.0.1:8766?x=1",
		"consultaVacia":      "http://127.0.0.1:8766?",
		"fragmento":          "http://127.0.0.1:8766#x",
		"fragmentoVacio":     "http://127.0.0.1:8766#",
		"puertoCero":         "http://127.0.0.1:0",
		"puertoAlto":         "http://127.0.0.1:65536",
		"puertoAltoPrivada":  "http://172.18.0.1:99999",
		"puertoCeroPrivada":  "http://172.18.0.1:0",
		"puertoVacio":        "http://127.0.0.1:",
		"puertoVacioPrivada": "http://172.18.0.1:",
		"puertoNegativo":     "http://127.0.0.1:-1",
		"puertoLetras":       "http://127.0.0.1:abc",
		"espacios":           " http://127.0.0.1:8766",
		"saltoDeLinea":       "http://127.0.0.1:8766\n",
		"opaca":              "http:127.0.0.1:8766",
		"dobleHost":          "http://127.0.0.1:8766@172.18.0.1:8766",
		"dobleBarra":         "http://127.0.0.1:8766//",
		"cadenaFilosa":       "http://127.0.0.1:8766@evil.example",
		"privadaFueraLista":  "http://172.18.0.77:8766",
	}
	for nombre, u := range casos {
		if AceptableCon(u, lista) {
			t.Errorf("%s: %q no debe aceptarse", nombre, u)
		}
	}
}

func TestParsearPermitidos(t *testing.T) {
	l, err := ParsearPermitidos("")
	if err != nil || len(l) != 0 {
		t.Fatalf("vacía: %v %v", l, err)
	}
	l, err = ParsearPermitidos(" 172.18.0.1 , 10.0.0.5,192.168.1.1 ")
	if err != nil || len(l) != 3 || l[0].String() != "172.18.0.1" || l[2].String() != "192.168.1.1" {
		t.Fatalf("válida: %v %v", l, err)
	}
	l, err = ParsearPermitidos("172.18.0.1,172.18.0.1")
	if err != nil || len(l) != 1 {
		t.Fatalf("duplicados: %v %v", l, err)
	}
}

func TestParsearPermitidosRechazaEntradasInvalidas(t *testing.T) {
	for _, valor := range []string{
		"localhost", "host.docker.internal", "8.8.8.8", "127.0.0.1", "0.0.0.0", "169.254.1.1",
		"::1", "fe80::1", "::ffff:172.18.0.1", "172.18.0.0/16", "172.18.0.1:8766", "http://172.18.0.1",
		"172.018.0.1", "172.15.255.255", "172.32.0.0", "224.0.0.1", "255.255.255.255",
		"172.18.0.1,", ",172.18.0.1", "172.18.0.1,,10.0.0.1", "172.18.0.1;10.0.0.1", "172.18.0.1 10.0.0.1",
	} {
		if l, err := ParsearPermitidos(valor); err == nil {
			t.Errorf("%q debe rechazarse, dio %v", valor, l)
		}
	}
}

func TestParsearPermitidosAcotaElTamano(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 17; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		b.WriteString("10.0.0." + strconv.Itoa(i))
	}
	if _, err := ParsearPermitidos(b.String()); err == nil {
		t.Fatal("17 entradas deben rechazarse")
	}
	if _, err := ParsearPermitidos(strings.TrimSuffix(b.String(), ",10.0.0.17")); err != nil {
		t.Fatalf("16 entradas deben aceptarse: %v", err)
	}
}

func TestErrorDeListaNoEcoaElValor(t *testing.T) {
	const secreto = "clave-secreta-123"
	_, err := ParsearPermitidos("172.18.0.1," + secreto)
	if err == nil || strings.Contains(err.Error(), secreto) || !strings.Contains(err.Error(), VariableHostPermitido) {
		t.Fatalf("el error debe nombrar la variable y no el valor: %v", err)
	}
}

func TestEntornoSinVariableEsIdenticoAlComportamientoActual(t *testing.T) {
	t.Setenv(VariableHostPermitido, "")
	if !Aceptable("http://127.0.0.1:8766") {
		t.Fatal("loopback debe aceptarse sin variable")
	}
	for _, u := range []string{"http://172.18.0.1:8766", "http://localhost:8766", "http://example.com"} {
		if Aceptable(u) {
			t.Fatalf("%q no debe aceptarse sin variable", u)
		}
	}
	l, err := Permitidos()
	if err != nil || len(l) != 0 {
		t.Fatalf("sin variable: %v %v", l, err)
	}
}

func TestEntornoConVariable(t *testing.T) {
	t.Setenv(VariableHostPermitido, "172.18.0.1")
	if !Aceptable("http://172.18.0.1:8766") || !Aceptable("http://127.0.0.1:8766") {
		t.Fatal("la IP de la lista y el loopback deben aceptarse")
	}
	if Aceptable("http://172.18.0.2:8766") {
		t.Fatal("otra IP privada no debe aceptarse")
	}
}

func TestEntornoConListaInvalidaSeTrataComoVacia(t *testing.T) {
	t.Setenv(VariableHostPermitido, "172.18.0.1,8.8.8.8")
	if _, err := Permitidos(); err == nil {
		t.Fatal("la lista inválida debe reportar error")
	}
	if Aceptable("http://172.18.0.1:8766") || Aceptable("http://8.8.8.8:8766") {
		t.Fatal("una lista inválida no debe autorizar nada")
	}
	if !Aceptable("http://127.0.0.1:8766") {
		t.Fatal("el loopback sigue aceptándose")
	}
}
