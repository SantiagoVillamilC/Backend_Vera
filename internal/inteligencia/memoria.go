package inteligencia

import (
	"errors"
	"fmt"
)

// Los tres niveles de la clave de memoria, en orden de prioridad.
const (
	NivelLlave  = "llave"
	NivelMID    = "mid"
	NivelNombre = "nombre"
)

// ErrDestinoSinClave reporta un destino sin llave, MID ni nombre que corregir.
var ErrDestinoSinClave = errors.New("inteligencia: el destino no tiene clave que corregir")

// ErrCategoriaNoVisible reporta un intento de asignar una categoría que no se ofrece al usuario.
var ErrCategoriaNoVisible = errors.New("inteligencia: la categoría no es visible en el catálogo")

// ClaveCorreccion es la clave tipada de la memoria privada.
type ClaveCorreccion struct {
	Usuario     string
	TipoDestino TipoDestino
	Nivel       string
	Valor       string
}

// Memoria es el conjunto de correcciones privadas.
type Memoria map[ClaveCorreccion]Categoria

// Destino es lo que se conoce de la contraparte para consultar y corregir.
type Destino struct {
	Tipo   TipoDestino
	Llave  string
	MID    string
	Nombre string
}

// ClavesDe devuelve las claves aplicables al destino, en orden de prioridad y sin claves vacías.
func ClavesDe(usuario string, destino Destino) ([]ClaveCorreccion, error) {
	var claves []ClaveCorreccion
	if destino.Llave != "" {
		claves = append(claves, ClaveCorreccion{usuario, destino.Tipo, NivelLlave, destino.Llave})
	}
	if destino.MID != "" {
		claves = append(claves, ClaveCorreccion{usuario, destino.Tipo, NivelMID, destino.MID})
	}
	if destino.Tipo == DestinoComercio && destino.Nombre != "" {
		if normalizado := NormalizarNombre(destino.Nombre); normalizado != "" {
			claves = append(claves, ClaveCorreccion{usuario, destino.Tipo, NivelNombre, normalizado})
		}
	}
	if len(claves) == 0 {
		return nil, ErrDestinoSinClave
	}
	return claves, nil
}

// Consultar devuelve la primera categoría válida de las claves del destino.
func Consultar(memoria Memoria, usuario string, destino Destino) (Categoria, bool) {
	claves, err := ClavesDe(usuario, destino)
	if err != nil {
		return "", false
	}
	for _, clave := range claves {
		categoria, existe := memoria[clave]
		if existe && EsCategoriaVisible(categoria) {
			return categoria, true
		}
	}
	return "", false
}

// Corregir devuelve una memoria nueva con la corrección aplicada a todas las claves del destino.
func Corregir(memoria Memoria, usuario string, destino Destino, categoria Categoria) (Memoria, error) {
	if !EsCategoriaVisible(categoria) {
		return nil, fmt.Errorf("%w: %q", ErrCategoriaNoVisible, categoria)
	}
	claves, err := ClavesDe(usuario, destino)
	if err != nil {
		return nil, err
	}
	nueva := make(Memoria, len(memoria)+len(claves))
	for clave, valor := range memoria {
		nueva[clave] = valor
	}
	for _, clave := range claves {
		nueva[clave] = categoria
	}
	return nueva, nil
}

// Proyectar aplana la memoria del destino para la cascada. Si dos claves comparten valor, gana la
// de mayor prioridad.
func Proyectar(memoria Memoria, usuario string, destino Destino) map[string]Categoria {
	claves, err := ClavesDe(usuario, destino)
	if err != nil {
		return map[string]Categoria{}
	}
	plana := make(map[string]Categoria, len(claves))
	for _, clave := range claves {
		categoria, existe := memoria[clave]
		if !existe || !EsCategoriaVisible(categoria) {
			continue
		}
		if _, ya := plana[clave.Valor]; !ya {
			plana[clave.Valor] = categoria
		}
	}
	return plana
}
