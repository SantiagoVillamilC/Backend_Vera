package inteligencia

import "strings"

// DestinoPersistido es la forma en la que la copia guarda el destino de un movimiento.
type DestinoPersistido struct {
	Tipo              TipoDestino
	Llave             string
	MID               string
	NombreNormalizado string
}

// PersistirDestino traduce un destino a sus columnas, sin inventar claves.
func PersistirDestino(usuario string, destino Destino) DestinoPersistido {
	persistido := DestinoPersistido{Tipo: destino.Tipo, MID: destino.MID}
	claves, err := ClavesDe(usuario, destino)
	if err != nil {
		return persistido
	}
	for _, clave := range claves {
		switch clave.Nivel {
		case NivelLlave:
			persistido.Llave = clave.Valor
		case NivelMID:
			persistido.MID = clave.Valor
		case NivelNombre:
			persistido.NombreNormalizado = clave.Valor
		}
	}
	return persistido
}

// Destino reconstruye el destino que puede corregirse desde las columnas de la copia.
func (p DestinoPersistido) Destino() (Destino, bool) {
	if p.Tipo != DestinoComercio && p.Tipo != DestinoPersona {
		return Destino{}, false
	}
	destino := Destino{Tipo: p.Tipo, Llave: p.Llave, MID: p.MID}
	if p.Tipo == DestinoComercio {
		destino.Nombre = p.NombreNormalizado
	}
	if _, err := ClavesDe("destino", destino); err != nil {
		return Destino{}, false
	}
	return destino, true
}

// EsDestinoGenerico reporta si el texto es una etiqueta compartida por muchos destinos distintos.
func EsDestinoGenerico(texto string) bool {
	switch strings.TrimSpace(texto) {
	case textoUsuarioDeLaBilletera, textoComercioExterno, textoMovimiento,
		textoBancoCustodio, textoGMF, textoComision:
		return true
	}
	return false
}
