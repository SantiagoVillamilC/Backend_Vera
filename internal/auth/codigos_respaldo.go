package auth

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

const (
	// cantidadCodigosRespaldo es cuantos codigos se emiten al activar la cuenta.
	cantidadCodigosRespaldo = 10
	// bytesCodigoRespaldo es la entropia en bytes de cada codigo.
	bytesCodigoRespaldo = 10
	// longitudGrupoRespaldo es el tamano de cada grupo legible del codigo.
	longitudGrupoRespaldo = 4
	// separadorCodigoRespaldo separa los grupos del codigo presentado.
	separadorCodigoRespaldo = "-"
)

// codificacionRespaldo es base32 sin relleno: 10 bytes rinden 16 caracteres legibles.
var codificacionRespaldo = base32.StdEncoding.WithPadding(base32.NoPadding)

// generarCodigosRespaldo produce la cantidad pedida de codigos independientes.
func generarCodigosRespaldo(cantidad int) ([]string, error) {
	codigos := make([]string, 0, cantidad)
	for range cantidad {
		codigo, err := generarCodigoRespaldo()
		if err != nil {
			return nil, err
		}
		codigos = append(codigos, codigo)
	}
	return codigos, nil
}

// generarCodigoRespaldo produce un codigo agrupado a partir de bytes aleatorios criptograficos.
func generarCodigoRespaldo() (string, error) {
	bruto := make([]byte, bytesCodigoRespaldo)
	if _, err := rand.Read(bruto); err != nil {
		return "", fmt.Errorf("auth: generar el codigo de respaldo: %w", err)
	}
	return agruparCodigoRespaldo(codificacionRespaldo.EncodeToString(bruto)), nil
}

// agruparCodigoRespaldo parte el codigo crudo en grupos separados para su lectura.
func agruparCodigoRespaldo(crudo string) string {
	grupos := make([]string, 0, len(crudo)/longitudGrupoRespaldo+1)
	for len(crudo) > longitudGrupoRespaldo {
		grupos = append(grupos, crudo[:longitudGrupoRespaldo])
		crudo = crudo[longitudGrupoRespaldo:]
	}
	grupos = append(grupos, crudo)
	return strings.Join(grupos, separadorCodigoRespaldo)
}

// normalizarCodigoRespaldo deja el codigo en la forma canonica que se hashea y se compara.
func normalizarCodigoRespaldo(codigo string) string {
	return strings.ToUpper(strings.NewReplacer(separadorCodigoRespaldo, "", " ", "").Replace(codigo))
}

// hmacDeCodigoRespaldo deriva el codigo normalizado con OTP_PEPPER para no guardarlo en claro.
func hmacDeCodigoRespaldo(pepper []byte, codigo string) []byte {
	return hmacDeCodigo(pepper, normalizarCodigoRespaldo(codigo))
}
