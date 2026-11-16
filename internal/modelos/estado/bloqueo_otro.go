//go:build !unix

package estado

import "errors"

// BloquearServicio no está disponible fuera de sistemas Unix: modelos-control corre en la VM Debian.
func (a Almacen) BloquearServicio() (liberar func(), ok bool, err error) {
	return nil, false, errors.New("el bloqueo del servicio requiere un sistema Unix")
}
