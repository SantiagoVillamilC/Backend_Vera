package nucleo

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Los cinco tipos de llave que admite Bre-B (HU-NF-10, criterio 2).
const (
	TipoLlaveDocumento    = "documento"
	TipoLlaveCelular      = "celular"
	TipoLlaveCorreo       = "correo"
	TipoLlaveAlfanumerico = "alfanumerico"
	TipoLlaveComercio     = "comercio"
)

// ErrLlaveInvalida reporta un tipo desconocido o un valor que no tiene la forma de su tipo.
var ErrLlaveInvalida = errors.New("nucleo: la llave de destino no es válida")

// ErrLlaveNoEncontrada reporta una llave que el directorio no reconoce; es un rechazo definitivo.
var ErrLlaveNoEncontrada = errors.New("nucleo: la llave no fue encontrada en el directorio")

// ErrDirectorioSinRespuesta reporta un directorio que no respondió o respondió mal; es temporal y reintentable.
var ErrDirectorioSinRespuesta = errors.New("nucleo: el directorio de llaves no respondió")

// ErrDirectorioNoDisponible reporta que falta el directorio configurado.
var ErrDirectorioNoDisponible = errors.New("nucleo: no hay directorio de llaves configurado")

// patronesDeLlave fija la forma de cada tipo; la historia nombra los tipos y no su forma exacta.
var patronesDeLlave = map[string]*regexp.Regexp{
	TipoLlaveDocumento:    regexp.MustCompile(`^[0-9]{6,10}$`),
	TipoLlaveCelular:      regexp.MustCompile(`^3[0-9]{9}$`),
	TipoLlaveCorreo:       regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`),
	TipoLlaveAlfanumerico: regexp.MustCompile(`^@[A-Za-z0-9]{1,64}$`),
	TipoLlaveComercio:     regexp.MustCompile(`^[A-Za-z0-9]{6,20}$`),
}

// Llave es la llave de destino ya normalizada con su tipo.
type Llave struct {
	Tipo  string
	Valor string
}

// ResolucionDirectorio es lo que el directorio devuelve: entidad y producto, nunca la cuenta.
type ResolucionDirectorio struct {
	Entidad  string
	Producto string
}

// DirectorioLlaves traduce una llave de Bre-B en la entidad y el producto del destinatario.
type DirectorioLlaves interface {
	Resolver(ctx context.Context, llave Llave) (ResolucionDirectorio, error)
}

// ParsearLlave valida el tipo contra los cinco admitidos y la forma, y devuelve la llave normalizada.
func ParsearLlave(tipo, valor string) (Llave, error) {
	tipo = strings.TrimSpace(tipo)
	valor = strings.TrimSpace(valor)
	patron, ok := patronesDeLlave[tipo]
	if !ok {
		return Llave{}, fmt.Errorf("%w: el tipo %q no es uno de los cinco de Bre-B", ErrLlaveInvalida, tipo)
	}
	if !patron.MatchString(valor) {
		return Llave{}, fmt.Errorf("%w: el valor no tiene la forma del tipo %q", ErrLlaveInvalida, tipo)
	}
	return Llave{Tipo: tipo, Valor: valor}, nil
}

// ResolverLlave valida la llave y consulta el directorio antes de abrir cualquier transacción.
func (s *Servicio) ResolverLlave(ctx context.Context, tipo, valor string) (ResolucionDirectorio, error) {
	llave, err := ParsearLlave(tipo, valor)
	if err != nil {
		return ResolucionDirectorio{}, err
	}
	if s.directorio == nil {
		return ResolucionDirectorio{}, ErrDirectorioNoDisponible
	}
	resolucion, err := s.directorio.Resolver(ctx, llave)
	if errors.Is(err, ErrLlaveNoEncontrada) {
		return ResolucionDirectorio{}, ErrLlaveNoEncontrada
	}
	if err != nil {
		if errors.Is(err, ErrDirectorioSinRespuesta) {
			return ResolucionDirectorio{}, err
		}
		return ResolucionDirectorio{}, fmt.Errorf("%w: %w", ErrDirectorioSinRespuesta, err)
	}
	resolucion.Entidad = strings.TrimSpace(resolucion.Entidad)
	resolucion.Producto = strings.TrimSpace(resolucion.Producto)
	if resolucion.Entidad == "" || resolucion.Producto == "" {
		return ResolucionDirectorio{}, fmt.Errorf("%w: la resolución llegó incompleta", ErrDirectorioSinRespuesta)
	}
	return resolucion, nil
}

// DirectorioNoDisponible es el relleno cuando main no configura el directorio.
type DirectorioNoDisponible struct{}

// Resolver siempre devuelve ErrDirectorioNoDisponible.
func (DirectorioNoDisponible) Resolver(context.Context, Llave) (ResolucionDirectorio, error) {
	return ResolucionDirectorio{}, ErrDirectorioNoDisponible
}

var _ DirectorioLlaves = DirectorioNoDisponible{}
