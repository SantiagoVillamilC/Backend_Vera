package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizarCorreo(t *testing.T) {
	t.Parallel()

	casos := map[string]struct{ entrada, quiero string }{
		"mayusculas y espacios": {"  Ana@Centinela.TEST  ", "ana@centinela.test"},
		"nfc compone la tilde":  {"Jos\u00e9@Ejemplo.com", "josé@ejemplo.com"},
		"nfd compone la tilde":  {"Jose\u0301@Ejemplo.com", "josé@ejemplo.com"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if got := NormalizarCorreo(caso.entrada); got != caso.quiero {
				t.Errorf("NormalizarCorreo(%q) = %q, quiero %q", caso.entrada, got, caso.quiero)
			}
		})
	}
}

func TestNormalizarCelular(t *testing.T) {
	t.Parallel()

	valido, err := NormalizarCelular("+573001234567")
	if err != nil || valido != "+573001234567" {
		t.Errorf("NormalizarCelular valido = %q, %v; quiero +573001234567, nil", valido, err)
	}
	nacional, err := NormalizarCelular("3001234567")
	if err != nil || nacional != "+573001234567" {
		t.Errorf("NormalizarCelular nacional = %q, %v; quiero +573001234567, nil", nacional, err)
	}
	for _, invalido := range []string{"+12345", "no-es-numero", ""} {
		if _, err := NormalizarCelular(invalido); !errors.Is(err, ErrCelularInvalido) {
			t.Errorf("NormalizarCelular(%q) err = %v, quiero ErrCelularInvalido", invalido, err)
		}
	}
}

func TestNormalizarNombreCompleto(t *testing.T) {
	t.Parallel()

	validos := map[string]string{
		"apostrofo y guion": "Ana María O'Connor-López",
		"un solo script":    "李 明",
		"espacios al borde": "  Ana Centinela  ",
	}
	for nombre, entrada := range validos {
		t.Run("valido "+nombre, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizarNombreCompleto(entrada)
			if err != nil || got != strings.TrimSpace(entrada) {
				t.Errorf("NormalizarNombreCompleto(%q) = %q, %v; quiero %q, nil", entrada, got, err, strings.TrimSpace(entrada))
			}
		})
	}

	invalidos := map[string]string{
		"una runa":          "A",
		"digitos":           "Ana 123",
		"doble espacio":     "Ana  Centinela",
		"guion final":       "Ana-",
		"apostrofo inicial": "'Ana",
		"dos scripts":       "Ana Мария",
		"solo marcas":       "\u0301\u0301",
		"mas de 100 runas":  strings.Repeat("á", 101),
	}
	for nombre, entrada := range invalidos {
		t.Run("invalido "+nombre, func(t *testing.T) {
			t.Parallel()
			if _, err := NormalizarNombreCompleto(entrada); !errors.Is(err, ErrNombreInvalido) {
				t.Errorf("NormalizarNombreCompleto(%q) err = %v, quiero ErrNombreInvalido", entrada, err)
			}
		})
	}
}

func TestNormalizarConsentimientos(t *testing.T) {
	t.Parallel()

	obligatorios := []SolicitudDeConsentimiento{
		{Tipo: TipoConsentimientoTratamiento, VersionTexto: "v1", Aceptado: true},
		{Tipo: TipoConsentimientoWhatsApp, VersionTexto: "v1", Aceptado: true},
	}
	resueltos, err := normalizarConsentimientos(append(obligatorios, SolicitudDeConsentimiento{Tipo: TipoConsentimientoBiometricos, VersionTexto: "v1", Aceptado: false}))
	if err != nil {
		t.Fatalf("normalizarConsentimientos validos: %v", err)
	}
	if len(resueltos) != 3 {
		t.Errorf("consentimientos resueltos = %d, quiero 3", len(resueltos))
	}

	faltantes := map[string][]SolicitudDeConsentimiento{
		"sin whatsapp":      {{Tipo: TipoConsentimientoTratamiento, VersionTexto: "v1", Aceptado: true}},
		"tratamiento falso": {{Tipo: TipoConsentimientoTratamiento, VersionTexto: "v1", Aceptado: false}, {Tipo: TipoConsentimientoWhatsApp, VersionTexto: "v1", Aceptado: true}},
		"solo biometricos":  {{Tipo: TipoConsentimientoBiometricos, VersionTexto: "v1", Aceptado: true}},
		"sin version":       {{Tipo: TipoConsentimientoTratamiento, VersionTexto: "", Aceptado: true}, {Tipo: TipoConsentimientoWhatsApp, VersionTexto: "v1", Aceptado: true}},
	}
	for nombre, entrantes := range faltantes {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if _, err := normalizarConsentimientos(entrantes); !errors.Is(err, ErrConsentimientoRequerido) {
				t.Errorf("err = %v, quiero ErrConsentimientoRequerido", err)
			}
		})
	}

	duplicados, err := normalizarConsentimientos([]SolicitudDeConsentimiento{
		{Tipo: TipoConsentimientoTratamiento, VersionTexto: "v1", Aceptado: true},
		{Tipo: TipoConsentimientoWhatsApp, VersionTexto: "v1", Aceptado: true},
		{Tipo: TipoConsentimientoTratamiento, VersionTexto: "v2", Aceptado: true},
	})
	if err != nil {
		t.Fatalf("normalizarConsentimientos duplicados: %v", err)
	}
	if len(duplicados) != 2 || duplicados[0].versionTexto != "v2" {
		t.Errorf("duplicados = %+v, quiero 2 con el ultimo tratamiento v2", duplicados)
	}
}
