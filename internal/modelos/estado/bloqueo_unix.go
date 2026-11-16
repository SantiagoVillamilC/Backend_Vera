//go:build unix

package estado

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// BloquearServicio toma un bloqueo exclusivo no bloqueante sobre un archivo del directorio de estado.
// Lo mantiene el proceso principal mientras vive. Devuelve ok=false si otro proceso ya lo tiene, y
// así el reconcile sabe si puede escribir el estado sin pisar al servicio.
func (a Almacen) BloquearServicio() (liberar func(), ok bool, err error) {
	f, err := os.OpenFile(filepath.Join(a.dir, "servicio.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("abrir bloqueo: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil { //nolint:gosec // el descriptor cabe en int
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("bloquear: %w", err)
	}
	return func() { _ = f.Close() }, true, nil
}
