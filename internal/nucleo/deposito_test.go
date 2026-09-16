package nucleo

import (
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// HU-NF-07 Criterio 16.4: el generador produce dep-<uuid v4> y no repite valores.
func TestNuevaReferenciaDeDepositoEsUnicaYConFormato(t *testing.T) {
	vistas := map[string]bool{}
	for range 100 {
		referencia := NuevaReferenciaDeDeposito()
		if !strings.HasPrefix(referencia, "dep-") {
			t.Fatalf("referencia = %q, quiero el prefijo dep-", referencia)
		}
		id, err := uuid.Parse(strings.TrimPrefix(referencia, "dep-"))
		if err != nil {
			t.Fatalf("referencia %q no lleva un UUID: %v", referencia, err)
		}
		if id.Version() != 4 {
			t.Errorf("referencia %q lleva UUID v%d, quiero v4", referencia, id.Version())
		}
		if vistas[referencia] {
			t.Fatalf("referencia repetida: %q", referencia)
		}
		vistas[referencia] = true
	}
}

// La fila cruda se envuelve con el monto en Centavos y la fecha de creación en UTC.
func TestIntencionDeDepositoEnvuelveLaFilaCruda(t *testing.T) {
	id := uuid.New()
	zona := time.FixedZone("UTC-5", -5*3600)
	creada := time.Date(2026, 9, 16, 10, 30, 0, 0, zona)

	intencion := intencionDeDeposito(repositorio.Deposito{
		ID:         pgtype.UUID{Bytes: id, Valid: true},
		UsuarioID:  "u-1",
		Monto:      9007199254740993,
		Moneda:     "COP",
		Referencia: "dep-" + id.String(),
		Estado:     EstadoDepositoPendiente,
		CreadaEn:   pgtype.Timestamptz{Time: creada, Valid: true},
	})

	if intencion.ID != id {
		t.Errorf("ID = %s, quiero %s", intencion.ID, id)
	}
	if intencion.Monto != Centavos(9007199254740993) {
		t.Errorf("Monto = %d, quiero 9007199254740993 exacto", intencion.Monto)
	}
	if intencion.Estado != EstadoDepositoPendiente {
		t.Errorf("Estado = %q, quiero %q", intencion.Estado, EstadoDepositoPendiente)
	}
	if !intencion.CreadaEn.Equal(creada) || intencion.CreadaEn.Location() != time.UTC {
		t.Errorf("CreadaEn = %s (%s), quiero el mismo instante en UTC", intencion.CreadaEn, intencion.CreadaEn.Location())
	}
}
