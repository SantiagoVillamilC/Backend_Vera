package copilot

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"testing"
)

func clipPrueba(t *testing.T, nombre string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/audio/" + nombre)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func wavPrueba(segundos int) []byte {
	b := make([]byte, 44+8000*segundos)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:20], 16)
	binary.LittleEndian.PutUint16(b[20:22], 1)
	binary.LittleEndian.PutUint16(b[22:24], 1)
	binary.LittleEndian.PutUint32(b[24:28], 8000)
	binary.LittleEndian.PutUint32(b[28:32], 8000)
	binary.LittleEndian.PutUint16(b[32:34], 1)
	binary.LittleEndian.PutUint16(b[34:36], 8)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:44], uint32(len(b)-44))
	return b
}
func TestValidarAudio(t *testing.T) {
	for _, nombre := range []string{"tono.wav", "silencio.wav", "tono.m4a", "limite.m4a"} {
		t.Run(nombre, func(t *testing.T) {
			formato := "wav"
			if strings.HasSuffix(nombre, "m4a") {
				formato = "m4a"
			}
			a, err := ValidarAudio(base64.StdEncoding.EncodeToString(clipPrueba(t, nombre)), formato)
			if err != nil || len(a.Datos) == 0 {
				t.Fatalf("clip válido rechazado: %v", err)
			}
		})
	}
	if _, err := ValidarAudio(base64.StdEncoding.EncodeToString(wavPrueba(60)), "wav"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ nombre, formato, encoded, regla string }{
		{"base64", "wav", "%%%", "audio_base64"},
		{"vacio", "wav", "", "audio_vacio"},
		{"formato", "mp3", "YWJj", "audio_formato"},
		{"mismatch", "wav", base64.StdEncoding.EncodeToString(clipPrueba(t, "tono.m4a")), "audio_contenedor"},
		{"truncado", "m4a", base64.StdEncoding.EncodeToString(clipPrueba(t, "tono.m4a")[:400]), "audio_contenedor"},
		{"grande", "wav", base64.StdEncoding.EncodeToString(make([]byte, MaxAudioBytes+1)), "audio_tamano"},
		{"wavLargo", "wav", base64.StdEncoding.EncodeToString(wavPrueba(61)), "audio_duracion"},
		{"m4aLargo", "m4a", base64.StdEncoding.EncodeToString(clipPrueba(t, "largo.m4a")), "audio_duracion"},
		{"url", "wav", "https://audio.invalid/archivo.wav", "audio_base64"},
		{"salto", "wav", "YWJj\n", "audio_base64"},
	} {
		t.Run(tc.nombre, func(t *testing.T) {
			_, err := ValidarAudio(tc.encoded, tc.formato)
			var audio *ErrorAudio
			if !errors.As(err, &audio) || audio.Regla != tc.regla {
				t.Fatalf("regla: %v", err)
			}
		})
	}
}

func FuzzAudioSinPanico(f *testing.F) {
	f.Add([]byte("RIFF"))
	f.Add(wavPrueba(1))
	for _, nombre := range []string{"tono.m4a", "tono.wav"} {
		b, err := os.ReadFile("testdata/audio/" + nombre)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > MaxAudioBytes {
			t.Skip()
		}
		_ = validarWAV(b)
		_ = validarM4A(b)
	})
}
