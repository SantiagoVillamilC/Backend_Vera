package audiopcm

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func muestraWAV() []byte {
	b := make([]byte, 82)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], 74)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 24000)
	binary.LittleEndian.PutUint32(b[28:], 48000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "LIST")
	binary.LittleEndian.PutUint32(b[40:], 26)
	copy(b[44:], "INFOISFT")
	copy(b[70:], "data")
	binary.LittleEndian.PutUint32(b[74:], 4)
	copy(b[78:], []byte{0, 128, 255, 127})
	return b
}

func TestNormalizarMarcadoresDeProveedorConservaPCMYMetadatos(t *testing.T) {
	entrada := muestraWAV()
	binary.LittleEndian.PutUint32(entrada[4:], desconocido)
	binary.LittleEndian.PutUint32(entrada[74:], desconocido)
	original := bytes.Clone(entrada)
	if Valido(entrada) {
		t.Fatal("aceptó marcador como tamaño definitivo")
	}
	b, ok := Normalizar(entrada)
	if !ok || !Valido(b) || !bytes.Equal(b, muestraWAV()) || !bytes.Equal(entrada, original) {
		t.Fatal("alteró PCM, metadatos o entrada")
	}
	if b, ok := Normalizar(muestraWAV()); !ok || !Valido(b) {
		t.Fatal("rechazó WAV completo")
	}
}

func TestNormalizarRechazaTruncamientoFormatoYSizesContradictorios(t *testing.T) {
	for nombre, cambiar := range map[string]func([]byte) []byte{
		"corte":      func(b []byte) []byte { return b[:80] },
		"frecuencia": func(b []byte) []byte { binary.LittleEndian.PutUint32(b[24:], 16000); return b },
		"data impar": func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[4:], desconocido)
			binary.LittleEndian.PutUint32(b[74:], desconocido)
			return b[:81]
		},
		"data desconocida con riff conocido": func(b []byte) []byte { binary.LittleEndian.PutUint32(b[74:], desconocido); return b },
		"metadatos desconocidos": func(b []byte) []byte {
			binary.LittleEndian.PutUint32(b[4:], desconocido)
			binary.LittleEndian.PutUint32(b[40:], desconocido)
			return b
		},
	} {
		t.Run(nombre, func(t *testing.T) {
			if _, ok := Normalizar(cambiar(muestraWAV())); ok {
				t.Fatal("aceptó WAV inválido")
			}
		})
	}
}
