package httpserver

import (
	"net"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5/middleware"
)

// ClientIPConfianzaLocal fija la IP del cliente en el contexto de chi (la misma fuente que leen
// RateLimitExempting y logging.AccessLogger vía middleware.GetClientIP).
//
// Confía en la cabecera indicada solo si la dirección remota (r.RemoteAddr) es loopback o privada,
// es decir, si la petición llegó por un proxy local. En cualquier otro caso, o si el último valor de
// la cabecera no es una IP válida, ignora la cabecera y usa RemoteAddr.
//
// Brecha residual conocida (N-8, abierto hasta la respuesta S2 de Santiago): si el userland proxy de
// Docker reescribe el origen de un puerto publicado directamente a la IP privada del gateway del
// bridge, RemoteAddr parece privada y la cabecera se sigue confiando aunque la envíe un cliente externo.
func ClientIPConfianzaLocal(cabecera string) func(http.Handler) http.Handler {
	nombre := http.CanonicalHeaderKey(cabecera)
	return func(next http.Handler) http.Handler {
		confiado := middleware.ClientIPFromHeader(nombre)(next)
		remoto := middleware.ClientIPFromRemoteAddr(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if remotoLocal(r.RemoteAddr) && ultimoValorEsIP(r.Header.Values(nombre)) {
				confiado.ServeHTTP(w, r)
				return
			}
			remoto.ServeHTTP(w, r)
		})
	}
}

// remotoLocal informa si la dirección remota es loopback o privada (IPv4 o IPv6).
func remotoLocal(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	ip = ip.Unmap().WithZone("")
	return ip.IsLoopback() || ip.IsPrivate()
}

// ultimoValorEsIP replica la regla de chi: el último valor de la cabecera es el que cuenta.
func ultimoValorEsIP(valores []string) bool {
	if len(valores) == 0 {
		return false
	}
	_, err := netip.ParseAddr(valores[len(valores)-1])
	return err == nil
}
