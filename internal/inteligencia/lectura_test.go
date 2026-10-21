package inteligencia

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func marzoDe2026(dia int) time.Time {
	return time.Date(2026, time.March, dia, 0, 0, 0, 0, zonaBogota)
}

func TestCursorOpacoVaYVuelve(t *testing.T) {
	t.Parallel()

	ancla := CursorDeMovimiento{
		OcurridoEn:    time.Date(2026, 10, 20, 23, 59, 59, 123456000, time.UTC),
		TransaccionID: uuid.MustParse("123e4567-e89b-42d3-a456-426614174000"),
	}
	texto := CodificarCursor(ancla)
	for _, prohibido := range []string{ancla.TransaccionID.String(), "|", "2026"} {
		if strings.Contains(texto, prohibido) {
			t.Errorf("el cursor %q expone %q", texto, prohibido)
		}
	}

	vuelta, err := DecodificarCursor(texto)
	if err != nil {
		t.Fatalf("DecodificarCursor: %v", err)
	}
	if !vuelta.OcurridoEn.Equal(ancla.OcurridoEn) {
		t.Errorf("ocurrido_en = %s, want %s", vuelta.OcurridoEn, ancla.OcurridoEn)
	}
	if vuelta.TransaccionID != ancla.TransaccionID {
		t.Errorf("transaccion_id = %s, want %s", vuelta.TransaccionID, ancla.TransaccionID)
	}
}

func TestCursorInvalidoSeRechaza(t *testing.T) {
	t.Parallel()

	crudo := func(texto string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(texto))
	}
	casos := map[string]string{
		"vacío":               "",
		"no es base64":        "no-es-base64!",
		"versión desconocida": crudo("v2|1|123e4567-e89b-42d3-a456-426614174000"),
		"sin partes":          crudo("v1|1"),
		"instante no entero":  crudo("v1|ayer|123e4567-e89b-42d3-a456-426614174000"),
		"uuid inválido":       crudo("v1|1|no-es-uuid"),
	}
	for nombre, texto := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodificarCursor(texto); !errors.Is(err, ErrCursorInvalido) {
				t.Fatalf("error = %v, want %v", err, ErrCursorInvalido)
			}
		})
	}
}

func TestMesesCompletosAntesPortaLaReferencia(t *testing.T) {
	t.Parallel()

	limite := time.Date(2026, time.September, 1, 0, 0, 0, 0, zonaBogota)
	casos := map[string]struct {
		primero time.Time
		quiero  int
	}{
		"primer día del mes cuenta ese mes":       {primero: time.Date(2026, time.June, 1, 8, 0, 0, 0, zonaBogota), quiero: 3},
		"un día después empieza el mes siguiente": {primero: time.Date(2026, time.June, 2, 8, 0, 0, 0, zonaBogota), quiero: 2},
		"mes anterior al límite":                  {primero: time.Date(2026, time.August, 1, 0, 0, 0, 0, zonaBogota), quiero: 1},
		"mismo mes que el límite no es completo":  {primero: time.Date(2026, time.September, 1, 0, 0, 0, 0, zonaBogota), quiero: 0},
		"posterior al límite":                     {primero: time.Date(2026, time.October, 1, 0, 0, 0, 0, zonaBogota), quiero: 0},
		"cruza el año": {
			primero: time.Date(2025, time.November, 1, 0, 0, 0, 0, zonaBogota),
			quiero:  10,
		},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if meses := mesesCompletosAntes(caso.primero, limite); len(meses) != caso.quiero {
				t.Fatalf("meses = %d, want %d: %v", len(meses), caso.quiero, meses)
			}
		})
	}
}

func TestInicioDelMesValidaLaForma(t *testing.T) {
	t.Parallel()

	ahora := time.Date(2026, time.October, 20, 15, 0, 0, 0, time.UTC)
	porDefecto, err := inicioDelMes("", ahora)
	if err != nil {
		t.Fatalf("mes por defecto: %v", err)
	}
	if porDefecto.Format("2006-01-02T15:04:05Z07:00") != "2026-10-01T00:00:00-05:00" {
		t.Errorf("mes por defecto = %s", porDefecto)
	}

	explicito, err := inicioDelMes("2026-03", ahora)
	if err != nil {
		t.Fatalf("mes explícito: %v", err)
	}
	if !explicito.Equal(marzoDe2026(1)) {
		t.Errorf("inicio = %s, want %s", explicito, marzoDe2026(1))
	}

	for _, invalido := range []string{"2026-13", "2026-1", "2026-00", "marzo", "2026-03-01", "26-03"} {
		if _, err := inicioDelMes(invalido, ahora); !errors.Is(err, ErrMesInvalido) {
			t.Errorf("mes %q: error = %v, want %v", invalido, err, ErrMesInvalido)
		}
	}
}

func TestMedianaRedondeaMedioAPar(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		valores []int64
		quiero  int64
	}{
		"impar":                  {valores: []int64{300, 100, 200}, quiero: 200},
		"par exacta":             {valores: []int64{100, 300}, quiero: 200},
		"par medio entero":       {valores: []int64{100, 200}, quiero: 150},
		"par medio a par arriba": {valores: []int64{1, 2}, quiero: 2},
		"par medio a par abajo":  {valores: []int64{2, 3}, quiero: 2},
		"un solo valor":          {valores: []int64{900}, quiero: 900},
		"desordenados":           {valores: []int64{500, 100, 400, 200, 300}, quiero: 300},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()
			if mediana := medianaEnCentavos(caso.valores); mediana != caso.quiero {
				t.Fatalf("mediana = %d, want %d", mediana, caso.quiero)
			}
		})
	}
}

func TestResumenRechazaElMesAntesDeTocarLaBase(t *testing.T) {
	t.Parallel()

	if _, err := NuevoServicio(nil).Resumen(t.Context(), "usuario", "2026-13"); !errors.Is(err, ErrMesInvalido) {
		t.Fatalf("error = %v, want %v", err, ErrMesInvalido)
	}
}
