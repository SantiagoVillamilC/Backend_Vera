package copilot

import (
	"encoding/base64"
	"encoding/binary"
)

// MaxAudioBytes limita el clip decodificado a 700 KiB, sin archivos temporales.
const MaxAudioBytes = 700 * 1024

// ErrorAudio identifica la regla incumplida sin incluir bytes del clip.
type ErrorAudio struct{ Regla string }

func (e *ErrorAudio) Error() string { return "audio inválido: " + e.Regla }

// ValidarAudio exige base64 canónico y duración verificable en el contenedor declarado.
// Admite WAV PCM/float y M4A AAC no fragmentado, los formatos de grabación de la app.
func ValidarAudio(encoded, formato string) (*AudioIA, error) {
	if formato != "m4a" && formato != "wav" {
		return nil, &ErrorAudio{"audio_formato"}
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxAudioBytes) {
		return nil, &ErrorAudio{"audio_tamano"}
	}
	b, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.StdEncoding.EncodeToString(b) != encoded {
		clear(b)
		return nil, &ErrorAudio{"audio_base64"}
	}
	if len(b) == 0 {
		return nil, &ErrorAudio{"audio_vacio"}
	}
	if len(b) > MaxAudioBytes {
		clear(b)
		return nil, &ErrorAudio{"audio_tamano"}
	}
	mime := "audio/wav"
	if formato == "wav" {
		err = validarWAV(b)
	} else {
		mime = "audio/m4a"
		err = validarM4A(b)
	}
	if err != nil {
		clear(b)
		return nil, err
	}
	return &AudioIA{MIME: mime, Datos: b}, nil
}

func audioInvalido() error { return &ErrorAudio{"audio_contenedor"} }
func validarWAV(b []byte) error {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(b[4:8]))+8 != uint64(len(b)) {
		return audioInvalido()
	}
	var block, rate uint64
	var size uint64
	fmt, data := false, false
	for pos := uint64(12); pos < uint64(len(b)); {
		if uint64(len(b))-pos < 8 {
			return audioInvalido()
		}
		n := uint64(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		end := pos + 8 + n
		if end > uint64(len(b)) {
			return audioInvalido()
		}
		v := b[pos+8 : end]
		switch string(b[pos : pos+4]) {
		case "fmt ":
			if fmt || len(v) < 16 {
				return audioInvalido()
			}
			fmt = true
			codec := binary.LittleEndian.Uint16(v[:2])
			channels := uint64(binary.LittleEndian.Uint16(v[2:4]))
			sampleRate := uint64(binary.LittleEndian.Uint32(v[4:8]))
			bits := uint64(binary.LittleEndian.Uint16(v[14:16]))
			block = uint64(binary.LittleEndian.Uint16(v[12:14]))
			rate = uint64(binary.LittleEndian.Uint32(v[8:12]))
			if (codec != 1 && codec != 3) || channels < 1 || channels > 2 || sampleRate < 8000 || sampleRate > 192000 || bits == 0 || bits%8 != 0 || bits > 64 || block != channels*bits/8 || rate != sampleRate*block {
				return audioInvalido()
			}
			if codec == 3 && bits != 32 && bits != 64 {
				return audioInvalido()
			}
		case "data":
			if data {
				return audioInvalido()
			}
			data = true
			size = n
		}
		end += n % 2
		if end > uint64(len(b)) {
			return audioInvalido()
		}
		pos = end
	}
	if !fmt || !data || size == 0 || block == 0 || size%block != 0 {
		return audioInvalido()
	}
	if size > rate*60 {
		return &ErrorAudio{"audio_duracion"}
	}
	return nil
}

type cajaAudio struct {
	tipo  string
	datos []byte
}

func cajasAudio(b []byte) ([]cajaAudio, error) {
	var out []cajaAudio
	for len(b) > 0 {
		if len(b) < 8 {
			return nil, audioInvalido()
		}
		n := uint64(binary.BigEndian.Uint32(b[:4]))
		h := uint64(8)
		switch n {
		case 1:
			if len(b) < 16 {
				return nil, audioInvalido()
			}
			n = binary.BigEndian.Uint64(b[8:16])
			h = 16
		case 0:
			n = uint64(len(b))
		}
		if n < h || n > uint64(len(b)) {
			return nil, audioInvalido()
		}
		out = append(out, cajaAudio{string(b[4:8]), b[h:n]})
		b = b[n:]
	}
	return out, nil
}
func cajaUnica(c []cajaAudio, tipo string) ([]byte, error) {
	var found []byte
	for _, v := range c {
		if v.tipo == tipo {
			if found != nil {
				return nil, audioInvalido()
			}
			found = v.datos
		}
	}
	if found == nil {
		return nil, audioInvalido()
	}
	return found, nil
}
func hijosAudio(c []cajaAudio, tipo string) ([]cajaAudio, error) {
	v, err := cajaUnica(c, tipo)
	if err != nil {
		return nil, err
	}
	return cajasAudio(v)
}
func relojAudio(b []byte, limite uint64) (uint64, uint64, error) {
	if len(b) < 20 {
		return 0, 0, audioInvalido()
	}
	var scale, duration uint64
	switch b[0] {
	case 0:
		scale = uint64(binary.BigEndian.Uint32(b[12:16]))
		duration = uint64(binary.BigEndian.Uint32(b[16:20]))
	case 1:
		if len(b) < 32 {
			return 0, 0, audioInvalido()
		}
		scale = uint64(binary.BigEndian.Uint32(b[20:24]))
		duration = binary.BigEndian.Uint64(b[24:32])
	default:
		return 0, 0, audioInvalido()
	}
	if scale == 0 || duration == 0 {
		return 0, 0, audioInvalido()
	}
	if duration > limite*scale {
		return 0, 0, &ErrorAudio{"audio_duracion"}
	}
	return scale, duration, nil
}
func validarM4A(b []byte) error {
	c, err := cajasAudio(b)
	if err != nil {
		return err
	}
	ftyp, err := cajaUnica(c, "ftyp")
	if err != nil || len(ftyp) < 8 || len(ftyp)%4 != 0 {
		return audioInvalido()
	}
	brand := false
	for i := 0; i < len(ftyp); i += 4 {
		if i == 4 {
			continue
		}
		switch string(ftyp[i : i+4]) {
		case "M4A ", "isom", "mp41", "mp42":
			brand = true
		}
	}
	if !brand {
		return audioInvalido()
	}
	mdat, err := cajaUnica(c, "mdat")
	if err != nil || len(mdat) == 0 {
		return audioInvalido()
	}
	moov, err := hijosAudio(c, "moov")
	if err != nil {
		return err
	}
	mvhd, err := cajaUnica(moov, "mvhd")
	if err != nil {
		return err
	}
	movieScale, movieDuration, err := relojAudio(mvhd, 60)
	if err != nil {
		return err
	}
	trak, err := hijosAudio(moov, "trak")
	if err != nil {
		return err
	}
	mdia, err := hijosAudio(trak, "mdia")
	if err != nil {
		return err
	}
	hdlr, err := cajaUnica(mdia, "hdlr")
	if err != nil || len(hdlr) < 12 || string(hdlr[8:12]) != "soun" {
		return audioInvalido()
	}
	mdhd, err := cajaUnica(mdia, "mdhd")
	if err != nil {
		return err
	}
	mediaScale, duration, err := relojAudio(mdhd, 61)
	if err != nil {
		return err
	}
	minf, err := hijosAudio(mdia, "minf")
	if err != nil {
		return err
	}
	stbl, err := hijosAudio(minf, "stbl")
	if err != nil {
		return err
	}
	stsd, err := cajaUnica(stbl, "stsd")
	if err != nil || len(stsd) < 8 || binary.BigEndian.Uint32(stsd[4:8]) != 1 {
		return audioInvalido()
	}
	entries, err := cajasAudio(stsd[8:])
	if err != nil || len(entries) != 1 || entries[0].tipo != "mp4a" || len(entries[0].datos) < 28 {
		return audioInvalido()
	}
	sampleRate := uint64(binary.BigEndian.Uint32(entries[0].datos[24:28]) >> 16)
	if sampleRate < 8000 || sampleRate > 192000 {
		return audioInvalido()
	}
	// AAC añade hasta dos tramas de priming/relleno. El límite público se mide
	// en la presentación (mvhd), no en esas muestras extra del medio (mdhd).
	paddingMS := (2048*1000+sampleRate-1)/sampleRate + (1000+movieScale-1)/movieScale
	mediaMS := (duration*1000 + mediaScale - 1) / mediaScale
	movieMS := (movieDuration*1000 + movieScale - 1) / movieScale
	if mediaMS > movieMS+paddingMS {
		return &ErrorAudio{"audio_duracion"}
	}
	stts, err := cajaUnica(stbl, "stts")
	if err != nil || len(stts) < 8 {
		return audioInvalido()
	}
	count := uint64(binary.BigEndian.Uint32(stts[4:8]))
	if count == 0 || count*8 != uint64(len(stts))-8 {
		return audioInvalido()
	}
	var ticks, samples uint64
	for pos := 8; pos < len(stts); pos += 8 {
		n := uint64(binary.BigEndian.Uint32(stts[pos : pos+4]))
		delta := uint64(binary.BigEndian.Uint32(stts[pos+4 : pos+8]))
		if n == 0 || delta == 0 || n > duration/delta {
			return audioInvalido()
		}
		samples += n
		ticks += n * delta
		if ticks > duration {
			return audioInvalido()
		}
	}
	if ticks != duration {
		return audioInvalido()
	}
	stsz, err := cajaUnica(stbl, "stsz")
	if err != nil || len(stsz) < 12 || uint64(binary.BigEndian.Uint32(stsz[8:12])) != samples {
		return audioInvalido()
	}
	n := uint64(binary.BigEndian.Uint32(stsz[4:8]))
	var size uint64
	if n > 0 {
		if len(stsz) != 12 || samples > uint64(len(mdat))/n {
			return audioInvalido()
		}
		size = n * samples
	} else {
		if samples*4 != uint64(len(stsz))-12 {
			return audioInvalido()
		}
		for pos := 12; pos < len(stsz); pos += 4 {
			size += uint64(binary.BigEndian.Uint32(stsz[pos : pos+4]))
			if size > uint64(len(mdat)) {
				return audioInvalido()
			}
		}
	}
	if size == 0 || size != uint64(len(mdat)) {
		return audioInvalido()
	}
	for _, box := range c {
		if box.tipo == "moof" {
			return audioInvalido()
		}
	}
	return nil
}
