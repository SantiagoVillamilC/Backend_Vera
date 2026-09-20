package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizarNIT(t *testing.T) {
	tests := map[string]struct {
		entrada string
		want    string
		wantErr bool
	}{
		"digitos planos":                  {entrada: "9001234568", want: "9001234568"},
		"con puntos y guion antes del dv": {entrada: "900.123.456-8", want: "9001234568"},
		"con espacios":                    {entrada: " 900 123 456 8 ", want: "9001234568"},
		"guion sin puntos":                {entrada: "900123456-8", want: "9001234568"},
		"dv cambiado":                     {entrada: "9001234569", wantErr: true},
		"sin digito de verificacion":      {entrada: "900123456", wantErr: true},
		"no numerico":                     {entrada: "abc", wantErr: true},
		"vacio":                           {entrada: "", wantErr: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizarNIT(tc.entrada)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NormalizarNIT(%q) err = %v, quiero error = %v", tc.entrada, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("NormalizarNIT(%q) = %q, quiero %q", tc.entrada, got, tc.want)
			}
			if tc.wantErr && !errors.Is(err, ErrNitInvalido) {
				t.Fatalf("NormalizarNIT(%q) err = %v, quiero ErrNitInvalido", tc.entrada, err)
			}
		})
	}
}

func TestNormalizarSolicitudDeRegistroEmpresa(t *testing.T) {
	base := func() SolicitudDeRegistroEmpresa {
		return SolicitudDeRegistroEmpresa{
			RazonSocial:                  "Centinela SAS",
			NIT:                          "900.123.456-8",
			Correo:                       " Empresa@Centinela.Test ",
			Celular:                      "+573001234567",
			RepresentanteNombre:          "Ana Centinela",
			RepresentanteTipoDocumento:   "CC",
			RepresentanteNumeroDocumento: "1020304050",
			Consentimientos: []SolicitudDeConsentimiento{
				{Tipo: TipoConsentimientoTratamiento, VersionTexto: "v1", Aceptado: true},
				{Tipo: TipoConsentimientoWhatsApp, VersionTexto: "v1", Aceptado: true},
			},
		}
	}

	t.Run("normaliza nit correo y celular", func(t *testing.T) {
		entrada, err := normalizarSolicitudDeRegistroEmpresa(base())
		if err != nil {
			t.Fatalf("normalizar: %v", err)
		}
		if entrada.nit != "9001234568" || entrada.correo != "empresa@centinela.test" || entrada.celular != "+573001234567" {
			t.Fatalf("entrada = %+v, quiero nit, correo y celular normalizados", entrada)
		}
		if len(entrada.consentimientos) != 2 {
			t.Fatalf("consentimientos = %d, quiero 2", len(entrada.consentimientos))
		}
	})

	t.Run("razon social vacia", func(t *testing.T) {
		solicitud := base()
		solicitud.RazonSocial = "   "
		if _, err := normalizarSolicitudDeRegistroEmpresa(solicitud); !errors.Is(err, ErrRazonSocialInvalida) {
			t.Fatalf("err = %v, quiero ErrRazonSocialInvalida", err)
		}
	})

	t.Run("razon social demasiado larga", func(t *testing.T) {
		solicitud := base()
		solicitud.RazonSocial = strings.Repeat("a", maxRunasRazonSocial+1)
		if _, err := normalizarSolicitudDeRegistroEmpresa(solicitud); !errors.Is(err, ErrRazonSocialInvalida) {
			t.Fatalf("err = %v, quiero ErrRazonSocialInvalida", err)
		}
	})

	t.Run("razon social con byte nulo", func(t *testing.T) {
		solicitud := base()
		solicitud.RazonSocial = "Centinela\x00 SAS"
		if _, err := normalizarSolicitudDeRegistroEmpresa(solicitud); !errors.Is(err, ErrRazonSocialInvalida) {
			t.Fatalf("err = %v, quiero ErrRazonSocialInvalida: un byte nulo fallaria en Postgres despues de insertar la persona", err)
		}
	})

	t.Run("nombre del representante invalido", func(t *testing.T) {
		solicitud := base()
		solicitud.RepresentanteNombre = "9"
		if _, err := normalizarSolicitudDeRegistroEmpresa(solicitud); !errors.Is(err, ErrNombreInvalido) {
			t.Fatalf("err = %v, quiero ErrNombreInvalido", err)
		}
	})
}
