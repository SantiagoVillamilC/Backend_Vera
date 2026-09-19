package auth

import (
	"bytes"
	"regexp"
	"testing"
)

// patronCodigoRespaldo es la forma legible presentada del codigo.
var patronCodigoRespaldo = regexp.MustCompile(`^[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}$`)

func TestGenerarCodigosRespaldoSonDiezDistintosYLegibles(t *testing.T) {
	t.Parallel()

	codigos, err := generarCodigosRespaldo(cantidadCodigosRespaldo)
	if err != nil {
		t.Fatalf("generarCodigosRespaldo: %v", err)
	}
	if len(codigos) != cantidadCodigosRespaldo {
		t.Fatalf("codigos = %d, quiero %d", len(codigos), cantidadCodigosRespaldo)
	}
	vistos := make(map[string]bool, len(codigos))
	for _, codigo := range codigos {
		if !patronCodigoRespaldo.MatchString(codigo) {
			t.Errorf("codigo %q no cumple el patron %s", codigo, patronCodigoRespaldo)
		}
		if vistos[codigo] {
			t.Errorf("codigo repetido %q", codigo)
		}
		vistos[codigo] = true
	}
}

func TestAgruparCodigoRespaldoParteEnGruposDeCuatro(t *testing.T) {
	t.Parallel()

	if got := agruparCodigoRespaldo("ABCDEFGHIJKLMNOP"); got != "ABCD-EFGH-IJKL-MNOP" {
		t.Errorf("agrupar = %q, quiero ABCD-EFGH-IJKL-MNOP", got)
	}
}

func TestNormalizarCodigoRespaldoIgnoraSeparadoresYMayusculas(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"ABCD-EFGH-IJKL-MNOP": "ABCDEFGHIJKLMNOP",
		"abcd efgh ijkl mnop": "ABCDEFGHIJKLMNOP",
		"AbCdEfGhIjKlMnOp":    "ABCDEFGHIJKLMNOP",
	}
	for entrada, esperado := range casos {
		if got := normalizarCodigoRespaldo(entrada); got != esperado {
			t.Errorf("normalizar(%q) = %q, quiero %q", entrada, got, esperado)
		}
	}
}

func TestHMACDeCodigoRespaldoNoExponeElCodigo(t *testing.T) {
	t.Parallel()

	pepper := bytes.Repeat([]byte{0x22}, 32)
	mac := hmacDeCodigoRespaldo(pepper, "ABCD-EFGH-IJKL-MNOP")
	if len(mac) != 32 {
		t.Fatalf("HMAC de %d bytes, quiero 32", len(mac))
	}
	if bytes.Contains(mac, []byte("ABCD-EFGH-IJKL-MNOP")) || bytes.Contains(mac, []byte("ABCDEFGHIJKLMNOP")) {
		t.Error("el HMAC contiene el codigo en claro")
	}
	if !bytes.Equal(mac, hmacDeCodigoRespaldo(pepper, "abcd efgh ijkl mnop")) {
		t.Error("la forma canonica deberia dar el mismo HMAC")
	}
	if bytes.Equal(mac, hmacDeCodigoRespaldo(pepper, "ZZZZ-ZZZZ-ZZZZ-ZZZZ")) {
		t.Error("dos codigos distintos dieron el mismo HMAC")
	}
}
