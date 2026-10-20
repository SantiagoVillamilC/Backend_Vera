// Package database abre y configura las conexiones a Postgres del servicio.
package database

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrDSNNoDesechable reporta una cadena de conexión que no apunta a la base de medición.
var ErrDSNNoDesechable = errors.New("database: el DSN no apunta a la base desechable")

// DSNLocalDeMedicion exige loopback, el puerto esperado y un prefijo de base concreto. Es la guarda
// que separa los comandos de medición de una base real.
func DSNLocalDeMedicion(raw, puerto, prefijoDeBase string, permitirRemoto bool) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("%w: el DSN está vacío", ErrDSNNoDesechable)
	}
	partes, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDSNNoDesechable, err)
	}
	host := partes.Hostname()
	local := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if !local && !permitirRemoto {
		return fmt.Errorf("%w: el host %q no es loopback", ErrDSNNoDesechable, host)
	}
	if partes.Port() != puerto {
		return fmt.Errorf("%w: el puerto %q no es %s", ErrDSNNoDesechable, partes.Port(), puerto)
	}
	base := strings.TrimPrefix(partes.Path, "/")
	if !strings.HasPrefix(base, prefijoDeBase) {
		return fmt.Errorf("%w: el nombre de la base %q debe empezar por %q",
			ErrDSNNoDesechable, base, prefijoDeBase)
	}
	return nil
}
