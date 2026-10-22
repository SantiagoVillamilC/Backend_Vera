// Package audiopcm valida y normaliza los WAV PCM16 mono de 24 kHz del laboratorio.
package audiopcm

import (
	"bytes"
	"encoding/binary"
)

const desconocido = ^uint32(0)

type cabecera struct {
	datos    int
	tamano   uint32
	corregir bool
}

// Valido exige tamaños definitivos y un contenedor completo, con metadatos opcionales.
func Valido(b []byte) bool { _, ok := inspeccionar(b, false); return ok }

// Normalizar permite el marcador 0xffffffff de un WAV generado sin seek.
// Sólo debe llamarse tras recibir satisfactoriamente TODO el cuerpo HTTP/JSON.
// Una lectura con error/cancelación nunca debe normalizarse como audio completo.
// Conserva literalmente PCM/metadatos y devuelve una copia sólo al corregir tamaños.
func Normalizar(b []byte) ([]byte, bool) {
	c, ok := inspeccionar(b, true)
	if !ok {
		return nil, false
	}
	if !c.corregir {
		return b, true
	}
	longitud := len(b)
	if longitud < 44 || longitud > 4*1024*1024+4096 {
		return nil, false
	}
	tamanoTotal := uint32(longitud)
	tamanoRIFF := tamanoTotal - 8
	salida := bytes.Clone(b)
	binary.LittleEndian.PutUint32(salida[4:], tamanoRIFF)
	binary.LittleEndian.PutUint32(salida[c.datos+4:], c.tamano)
	return salida, true
}

func inspeccionar(b []byte, permitirMarcadores bool) (cabecera, bool) {
	if len(b) <= 44 || len(b) > 4*1024*1024+4096 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return cabecera{}, false
	}
	riff := binary.LittleEndian.Uint32(b[4:])
	marcador := riff == desconocido && permitirMarcadores
	if !marcador && int64(riff) != int64(len(b))-8 {
		return cabecera{}, false
	}
	c := cabecera{corregir: marcador}
	formato, datos := false, false
	for pos := 12; pos < len(b); {
		if len(b)-pos < 8 {
			return cabecera{}, false
		}
		inicio := pos + 8
		tamano := binary.LittleEndian.Uint32(b[pos+4:])
		tipo := string(b[pos : pos+4])
		if tamano == desconocido {
			if !marcador || tipo != "data" {
				return cabecera{}, false
			}
			restantes := len(b) - inicio
			if restantes <= 0 || restantes > 4*1024*1024 {
				return cabecera{}, false
			}
			tamano = uint32(restantes)
		}
		if int64(tamano) > int64(len(b)-inicio) {
			return cabecera{}, false
		}
		switch tipo {
		case "fmt ":
			if formato || tamano < 16 {
				return cabecera{}, false
			}
			f := b[inicio : inicio+int(tamano)]
			if binary.LittleEndian.Uint16(f) != 1 || binary.LittleEndian.Uint16(f[2:]) != 1 || binary.LittleEndian.Uint32(f[4:]) != 24000 || binary.LittleEndian.Uint32(f[8:]) != 48000 || binary.LittleEndian.Uint16(f[12:]) != 2 || binary.LittleEndian.Uint16(f[14:]) != 16 {
				return cabecera{}, false
			}
			formato = true
		case "data":
			if !formato || datos || tamano == 0 || tamano%2 != 0 || tamano > 4*1024*1024 {
				return cabecera{}, false
			}
			datos = true
			c.datos = pos
			c.tamano = tamano
		}
		pos = inicio + int(tamano) + int(tamano%2)
		if pos > len(b) {
			return cabecera{}, false
		}
	}
	return c, formato && datos
}
