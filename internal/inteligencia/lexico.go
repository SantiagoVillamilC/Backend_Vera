package inteligencia

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// HuellaLexicoV2 es el sha256 del léxico embebido, congelado el 20 de octubre de 2026. Un cambio en el
// archivo sin cambiar esta constante es un error de construcción, no una mejora.
const HuellaLexicoV2 = "1a469ab4fda9780256cd091041c1a1a20f1c6b7722d7eb4ec13d4b5906b1abfd"

// VersionLexicoV2 identifica la versión del léxico en los registros.
const VersionLexicoV2 = "lexico-v2"

//go:embed lexico/lexico_v2.csv
var lexicoV2CSV []byte

// ErrLexicoInvalido reporta un léxico que no se puede usar: huella distinta, encabezado ausente o
// palabra vacía. Falla cerrado porque un léxico mal cargado decide de más.
var ErrLexicoInvalido = errors.New("inteligencia: el léxico embebido no es válido")

// Lexico embebido es el léxico con el que se puede encender el escalón 5. Se expone por función para
// que nadie lo mute desde fuera y para verificar la huella en cada carga.
type Lexico struct {
	Version string
	Huella  string
	Pares   []ParLexico
}

// CargarLexicoV2 verifica la huella del archivo embebido y devuelve sus pares ordenados por palabra.
func CargarLexicoV2() (*Lexico, error) {
	suma := sha256.Sum256(lexicoV2CSV)
	huella := hex.EncodeToString(suma[:])
	if err := verificarHuella(huella); err != nil {
		return nil, err
	}
	lector := csv.NewReader(bytes.NewReader(lexicoV2CSV))
	lector.FieldsPerRecord = 2
	cabecera, err := lector.Read()
	if err != nil {
		return nil, fmt.Errorf("%w: leer el encabezado: %w", ErrLexicoInvalido, err)
	}
	if len(cabecera) != 2 || strings.TrimSpace(cabecera[0]) != "palabra" ||
		strings.TrimSpace(cabecera[1]) != "categoria" {
		return nil, fmt.Errorf("%w: encabezado %v, quiero palabra y categoria", ErrLexicoInvalido, cabecera)
	}
	pares := make([]ParLexico, 0, 588)
	for {
		registro, err := lector.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: leer una fila: %w", ErrLexicoInvalido, err)
		}
		palabra := strings.ToUpper(strings.TrimSpace(registro[0]))
		categoria := Categoria(strings.TrimSpace(registro[1]))
		if palabra == "" {
			return nil, fmt.Errorf("%w: palabra vacía en la fila %d", ErrLexicoInvalido, len(pares)+2)
		}
		if !EsCategoriaVisible(categoria) {
			return nil, fmt.Errorf("%w: la categoría %q de %q no es visible", ErrLexicoInvalido,
				categoria, palabra)
		}
		pares = append(pares, ParLexico{Palabra: palabra, Categoria: categoria})
	}
	if len(pares) == 0 {
		return nil, fmt.Errorf("%w: el léxico está vacío", ErrLexicoInvalido)
	}
	return &Lexico{Version: VersionLexicoV2, Huella: huella, Pares: pares}, nil
}

// verificarHuella compara la huella calculada con la congelada.
func verificarHuella(huella string) error {
	if huella != HuellaLexicoV2 {
		return fmt.Errorf("%w: huella %s distinta de %s", ErrLexicoInvalido, huella, HuellaLexicoV2)
	}
	return nil
}

// ConLexico enciende el escalón 5 con el léxico cargado.
func ConLexico(lexico *Lexico) OpcionServicio {
	return func(s *Servicio) {
		if lexico != nil {
			s.lexico = lexico
		}
	}
}

// ParesDeLexico devuelve los pares del léxico listos para la cascada, o nil si no hay léxico.
func (l *Lexico) ParesDeLexico() []ParLexico {
	if l == nil {
		return nil
	}
	return l.Pares
}

// ModoLexico es el valor de la variable LEXICO.
type ModoLexico string

// Modos del léxico. El escalón 5 arranca apagado porque en la parte de prueba no alcanzó el 0,90.
const (
	ModoLexicoApagado ModoLexico = "apagado"
	ModoLexicoV2      ModoLexico = "v2"
)

// ModoLexicoPorDefecto conserva el primer corte cuando la variable no está definida.
const ModoLexicoPorDefecto = ModoLexicoApagado

// InterpretarModoLexico valida el valor de la variable LEXICO. Un valor vacío es el modo por
// defecto y no un error, para que desplegar sin la variable no cambie el lote.
func InterpretarModoLexico(valor string) (ModoLexico, error) {
	switch ModoLexico(strings.TrimSpace(strings.ToLower(valor))) {
	case "":
		return ModoLexicoPorDefecto, nil
	case ModoLexicoApagado:
		return ModoLexicoApagado, nil
	case ModoLexicoV2:
		return ModoLexicoV2, nil
	default:
		return "", fmt.Errorf("inteligencia: LEXICO debe ser %q o %q, no %q",
			ModoLexicoApagado, ModoLexicoV2, valor)
	}
}
