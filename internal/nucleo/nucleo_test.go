package nucleo

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

func TestPresentarUnidadesMayores(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   Centavos
		want string
	}{
		"cero":            {0, "0.00"},
		"cinco":           {5, "0.05"},
		"menos cinco":     {-5, "-0.05"},
		"cien":            {100, "1.00"},
		"un millon":       {1000000, "10000.00"},
		"menos un millon": {-1000000, "-10000.00"},
		"maximo int64":    {Centavos(math.MaxInt64), "92233720368547758.07"},
		"minimo int64":    {Centavos(math.MinInt64), "-92233720368547758.08"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.in.PresentarUnidadesMayores(); got != tc.want {
				t.Errorf("PresentarUnidadesMayores(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSumarDesbordamiento(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		a, b Centavos
	}{
		"maximo mas uno":       {Centavos(math.MaxInt64), 1},
		"minimo mas menos uno": {Centavos(math.MinInt64), -1},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := tc.a.Sumar(tc.b); !errors.Is(err, ErrDesbordamiento) {
				t.Fatalf("Sumar(%d, %d) error = %v, want ErrDesbordamiento", tc.a, tc.b, err)
			}
		})
	}
}

func TestRestarDesbordamiento(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		a, b Centavos
	}{
		"maximo menos menos uno": {Centavos(math.MaxInt64), -1},
		"minimo menos uno":       {Centavos(math.MinInt64), 1},
		"cero menos minimo":      {0, Centavos(math.MinInt64)},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := tc.a.Restar(tc.b); !errors.Is(err, ErrDesbordamiento) {
				t.Fatalf("Restar(%d, %d) error = %v, want ErrDesbordamiento", tc.a, tc.b, err)
			}
		})
	}
}

func TestSumarRestarUnPasoAdentro(t *testing.T) {
	t.Parallel()

	if got, err := Centavos(math.MaxInt64).Sumar(-1); err != nil || got != Centavos(math.MaxInt64-1) {
		t.Errorf("Sumar(MaxInt64, -1) = %d, %v; want %d, nil", got, err, int64(math.MaxInt64-1))
	}
	if got, err := Centavos(math.MinInt64).Sumar(1); err != nil || got != Centavos(math.MinInt64+1) {
		t.Errorf("Sumar(MinInt64, 1) = %d, %v; want %d, nil", got, err, int64(math.MinInt64+1))
	}
	if got, err := Centavos(math.MaxInt64).Restar(1); err != nil || got != Centavos(math.MaxInt64-1) {
		t.Errorf("Restar(MaxInt64, 1) = %d, %v; want %d, nil", got, err, int64(math.MaxInt64-1))
	}
	if got, err := Centavos(math.MinInt64).Restar(-1); err != nil || got != Centavos(math.MinInt64+1) {
		t.Errorf("Restar(MinInt64, -1) = %d, %v; want %d, nil", got, err, int64(math.MinInt64+1))
	}
}

// TestInt64EnvuelveEnSilencio deja en la suite la razón por la que Sumar y Restar existen.
func TestInt64EnvuelveEnSilencio(t *testing.T) {
	t.Parallel()

	x := int64(math.MaxInt64)
	x++
	if x != math.MinInt64 {
		t.Fatalf("int64 envolvió a %d, want %d", x, int64(math.MinInt64))
	}
}

func TestCentavosMarshalJSON(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		in   Centavos
		want string
	}{
		"cero":         {0, `"0"`},
		"negativo":     {-1000000, `"-1000000"`},
		"maximo int64": {Centavos(math.MaxInt64), `"9223372036854775807"`},
		"minimo int64": {Centavos(math.MinInt64), `"-9223372036854775808"`},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("Marshal(%d) error = %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Errorf("Marshal(%d) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnmarshalCentavosRechaza(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"numero json":         `1000000`,
		"null":                `null`,
		"cadena vacia":        `""`,
		"con decimales":       `"12.50"`,
		"notacion cientifica": `"1e3"`,
		"con espacio":         `" 5"`,
		"bool":                `true`,
		"objeto":              `{}`,
		"fuera de int64":      `"9223372036854775808"`,
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var c Centavos
			if err := json.Unmarshal([]byte(in), &c); !errors.Is(err, ErrMontoInvalido) {
				t.Fatalf("Unmarshal(%s) error = %v, want ErrMontoInvalido", in, err)
			}
		})
	}
}

func TestUnmarshalCentavosAceptaCadenaYNullOpcional(t *testing.T) {
	t.Parallel()

	var c Centavos
	if err := json.Unmarshal([]byte(`"9007199254740993"`), &c); err != nil {
		t.Fatalf("Unmarshal cadena: error inesperado: %v", err)
	}
	if c != Centavos(9007199254740993) {
		t.Errorf("Unmarshal cadena = %d, want %d", c, int64(9007199254740993))
	}

	var p *Centavos
	if err := json.Unmarshal([]byte(`null`), &p); err != nil {
		t.Fatalf("Unmarshal null: error inesperado: %v", err)
	}
	if p != nil {
		t.Errorf("Unmarshal null dejó %d, want nil", *p)
	}
}

func TestRellenosDevuelvenPendiente(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	if _, err := (KYCNoDisponible{}).NivelKYC(ctx, "usuario"); !errors.Is(err, ErrKYCNoDisponible) {
		t.Errorf("KYCNoDisponible.NivelKYC error = %v, want ErrKYCNoDisponible", err)
	}
	if _, err := (RielNoDisponible{}).Enviar(ctx, OrdenRiel{}); !errors.Is(err, ErrRielNoDisponible) {
		t.Errorf("RielNoDisponible.Enviar error = %v, want ErrRielNoDisponible", err)
	}
	if _, err := (CustodioNoDisponible{}).Extracto(ctx, time.Time{}); !errors.Is(err, ErrCustodioNoDisponible) {
		t.Errorf("CustodioNoDisponible.Extracto error = %v, want ErrCustodioNoDisponible", err)
	}
}
