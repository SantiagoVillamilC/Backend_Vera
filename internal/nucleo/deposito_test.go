package nucleo

import (
	"errors"
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

// HU-NF-07 Criterio 18: con el plazo apagado (cero) el barrido no abre transacción ni expira nada.
func TestExpirarIntencionesConPlazoApagadoNoTocaLaBase(t *testing.T) {
	t.Parallel()

	expiradas, err := (&Servicio{}).ExpirarIntenciones(t.Context(), 0)
	if err != nil {
		t.Fatalf("ExpirarIntenciones(0) = %v, quiero nil", err)
	}
	if expiradas != 0 {
		t.Errorf("ExpirarIntenciones(0) = %d, quiero 0", expiradas)
	}
}

// HU-NF-07 Criterio 19: la escalada deja la operación, la referencia y el estado por el puerto.
func TestEscalarConfirmacionTardiaCapturaLaReferencia(t *testing.T) {
	t.Parallel()

	falso := &rastreadorFalso{}
	servicio := &Servicio{rastreador: falso}
	servicio.escalarConfirmacionTardia(t.Context(), repositorio.Deposito{
		Referencia: "dep-x",
		UsuarioID:  "u-1",
		Estado:     EstadoDepositoExpirada,
	})

	if len(falso.capturas) != 1 {
		t.Fatalf("capturas = %d, quiero 1", len(falso.capturas))
	}
	captura := falso.capturas[0]
	if captura.contexto.Operacion != "confirmacion_tardia" {
		t.Errorf("operacion = %q, quiero confirmacion_tardia", captura.contexto.Operacion)
	}
	if !errors.Is(captura.err, ErrConfirmacionTardia) {
		t.Errorf("err = %v, quiero ErrConfirmacionTardia", captura.err)
	}
	if !tieneCampo(captura.contexto.Campos, "referencia", "dep-x") {
		t.Errorf("campos = %v, quiero la referencia dep-x", captura.contexto.Campos)
	}
}

// HU-NF-07 Criterio 19: sin rastreador inyectado la escalada no rompe el flujo.
func TestEscalarConfirmacionTardiaSinRastreadorNoFalla(t *testing.T) {
	t.Parallel()

	(&Servicio{}).escalarConfirmacionTardia(t.Context(), repositorio.Deposito{Referencia: "dep-x"})
}

func tieneCampo(campos []Campo, clave, valor string) bool {
	for _, c := range campos {
		if c.Clave == clave && c.Valor == valor {
			return true
		}
	}
	return false
}
