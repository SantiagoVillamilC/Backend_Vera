package nucleo

import (
	"errors"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestCuentasUnicasOrdenadasFijaElOrdenDelCandado cubre la regla de una línea del capítulo 12.
func TestCuentasUnicasOrdenadasFijaElOrdenDelCandado(t *testing.T) {
	t.Parallel()

	got := cuentasUnicasOrdenadas([]string{"pasivo:usuario:b", "", "pasivo:usuario:a", "pasivo:usuario:b"})
	querer := []string{"pasivo:usuario:a", "pasivo:usuario:b"}
	if len(got) != len(querer) {
		t.Fatalf("cuentas = %v, quiero %v", got, querer)
	}
	for i := range querer {
		if got[i] != querer[i] {
			t.Errorf("cuenta %d = %q, quiero %q", i, got[i], querer[i])
		}
	}
}

// TestBloquearOrdenadasSinCuentasNoTocaLaBase cubre que una lista vacía no abre ninguna consulta.
func TestBloquearOrdenadasSinCuentasNoTocaLaBase(t *testing.T) {
	t.Parallel()

	if err := bloquearOrdenadas(t.Context(), nil); err != nil {
		t.Fatalf("bloquearOrdenadas sin cuentas = %v, quiero nil", err)
	}
}

// TestEsEsperaDeBloqueoAgotadaReconoceElLockTimeout cubre el mapeo del SQLSTATE 55P03 a un error de dominio.
func TestEsEsperaDeBloqueoAgotadaReconoceElLockTimeout(t *testing.T) {
	t.Parallel()

	if !esEsperaDeBloqueoAgotada(&pgconn.PgError{Code: pgerrcode.LockNotAvailable}) {
		t.Errorf("un PgError 55P03 debe reconocerse como espera agotada")
	}
	if esEsperaDeBloqueoAgotada(&pgconn.PgError{Code: pgerrcode.DeadlockDetected}) {
		t.Errorf("un deadlock no es una espera agotada")
	}
	if esEsperaDeBloqueoAgotada(errors.New("otro")) {
		t.Errorf("un error ajeno no es una espera agotada")
	}
}
