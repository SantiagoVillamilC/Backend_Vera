// Package destinolocal decide si la URL de un servicio de modelos (voz, STT u Ollama) es un
// destino aceptable. Por defecto solo admite http en 127.0.0.1. Una IPv4 privada exacta se
// admite únicamente si el operador la lista en VERA_MODELOS_HOST_PERMITIDO, para el caso de
// la API dentro de un contenedor que alcanza modelos-control en el host por el gateway del
// puente de Docker.
package destinolocal

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// VariableHostPermitido es la variable de entorno con la lista de IPv4 privadas autorizadas.
const VariableHostPermitido = "VERA_MODELOS_HOST_PERMITIDO"

// maxPermitidos acota la lista: autorizar más de un puñado de hosts es una señal de error.
const maxPermitidos = 16

var loopback = netip.AddrFrom4([4]byte{127, 0, 0, 1})

// ErrPermitidos indica que VERA_MODELOS_HOST_PERMITIDO no es una lista válida. El detalle nunca
// repite el valor recibido.
var ErrPermitidos = errors.New(VariableHostPermitido + " debe ser una lista de IPv4 privadas literales (10.0.0.0/8, 172.16.0.0/12 o 192.168.0.0/16) separadas por comas")

// ParsearPermitidos valida la lista separada por comas. Los espacios alrededor de cada entrada
// se ignoran. Una entrada vacía, un hostname, IPv6, un rango CIDR, un puerto, una IP pública, un
// loopback o una IP con ceros a la izquierda invalida toda la lista. El valor vacío es una lista vacía.
func ParsearPermitidos(valor string) ([]netip.Addr, error) {
	if strings.TrimSpace(valor) == "" {
		return nil, nil
	}
	entradas := strings.Split(valor, ",")
	if len(entradas) > maxPermitidos {
		return nil, ErrPermitidos
	}
	lista := make([]netip.Addr, 0, len(entradas))
	vistas := map[netip.Addr]bool{}
	for i, e := range entradas {
		a, ok := ipv4Canonica(strings.TrimSpace(e))
		if !ok || !a.IsPrivate() {
			return nil, fmt.Errorf("%w (entrada %d)", ErrPermitidos, i+1)
		}
		if !vistas[a] {
			vistas[a] = true
			lista = append(lista, a)
		}
	}
	return lista, nil
}

// Permitidos lee y valida VERA_MODELOS_HOST_PERMITIDO del entorno del proceso.
func Permitidos() ([]netip.Addr, error) {
	return ParsearPermitidos(os.Getenv(VariableHostPermitido))
}

// Aceptable aplica AceptableCon con la lista del entorno. Una lista inválida cuenta como vacía,
// de modo que nunca autoriza nada: el arranque de la API la reporta aparte con Permitidos.
func Aceptable(crudo string) bool {
	lista, err := Permitidos()
	if err != nil {
		lista = nil
	}
	return AceptableCon(crudo, lista)
}

// AceptableCon acepta un origen http sin credenciales, ruta, consulta ni fragmento cuyo host sea
// exactamente 127.0.0.1 (puerto opcional) o exactamente una IPv4 privada de la lista (puerto
// obligatorio). Rechaza todo lo demás, incluidos nombres de host e IPv6.
func AceptableCon(crudo string, permitidos []netip.Addr) bool {
	u, err := url.Parse(crudo)
	if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.Path != "" ||
		u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(crudo, "?#") ||
		strings.HasSuffix(u.Host, ":") {
		return false
	}
	host, ok := ipv4Canonica(u.Hostname())
	if !ok || !puertoValido(u.Port()) {
		return false
	}
	if host == loopback {
		return true
	}
	if u.Port() == "" || !host.IsPrivate() {
		return false
	}
	for _, p := range permitidos {
		if p == host {
			return true
		}
	}
	return false
}

// ipv4Canonica exige una IPv4 literal en su forma canónica: sin ceros a la izquierda, sin
// mapeo IPv6 ni zona, y con el mismo texto que produce netip.
func ipv4Canonica(texto string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(texto)
	if err != nil || !a.Is4() || a.String() != texto {
		return netip.Addr{}, false
	}
	return a, true
}

// puertoValido admite la ausencia de puerto y exige 1 a 65535 si viene.
func puertoValido(puerto string) bool {
	if puerto == "" {
		return true
	}
	n, err := strconv.Atoi(puerto)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == puerto
}
