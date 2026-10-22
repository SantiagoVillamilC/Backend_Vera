package copilot

import (
	"strings"
	"testing"
	"time"
)

func TestReferenciasCalendarioColombia(t *testing.T) {
	for nombre, tc := range map[string]struct{ utc, semana, hoy, mes string }{
		"lunes":               {"2026-10-19T15:00:00Z", "2026-10-12 a 2026-10-18", "2026-10-19", "2026-10-01"},
		"domingo en colombia": {"2026-10-19T02:00:00Z", "2026-10-05 a 2026-10-11", "2026-10-18", "2026-10-01"},
		"cambio de año":       {"2027-01-01T15:00:00Z", "2026-12-21 a 2026-12-27", "2027-01-01", "2027-01-01"},
	} {
		t.Run(nombre, func(t *testing.T) {
			ahora, err := time.Parse(time.RFC3339, tc.utc)
			if err != nil {
				t.Fatal(err)
			}
			got := referenciasCalendario(ahora)
			for _, esperado := range []string{"hoy=" + tc.hoy, "semana pasada=" + tc.semana, "este mes=" + tc.mes + " a " + tc.hoy} {
				if !strings.Contains(got, esperado) {
					t.Fatalf("falta %q en %q", esperado, got)
				}
			}
		})
	}
}
