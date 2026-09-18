package nucleo

import (
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type filaSinResultado struct {
	err error
}

func (f filaSinResultado) Scan(...any) error { return f.err }

// La fila cruda se envuelve con el monto en Centavos y las dos fechas en UTC.
func TestTransferenciaDeFilaEnvuelveLaFilaCruda(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	zona := time.FixedZone("UTC-5", -5*3600)
	creada := time.Date(2026, 9, 17, 10, 30, 0, 0, zona)
	actualizada := creada.Add(2 * time.Minute)

	transferencia := transferenciaDeFila(repositorio.Transferencia{
		ID:             pgtype.UUID{Bytes: id, Valid: true},
		UsuarioID:      "u-1",
		CuentaOrigen:   "pasivo:usuario:u-1:disponible",
		LlaveDestino:   "llave-beto",
		Monto:          9007199254740993,
		Moneda:         "COP",
		IdempotencyKey: "k-1",
		Estado:         EstadoTransferenciaCreada,
		CreadaEn:       pgtype.Timestamptz{Time: creada, Valid: true},
		ActualizadaEn:  pgtype.Timestamptz{Time: actualizada, Valid: true},
	})

	if transferencia.ID != id {
		t.Errorf("ID = %s, quiero %s", transferencia.ID, id)
	}
	if transferencia.CuentaOrigen != "pasivo:usuario:u-1:disponible" || transferencia.LlaveDestino != "llave-beto" {
		t.Errorf("cuenta/llave = %q/%q, quiero las de la fila", transferencia.CuentaOrigen, transferencia.LlaveDestino)
	}
	if transferencia.Monto != Centavos(9007199254740993) {
		t.Errorf("Monto = %d, quiero 9007199254740993 exacto", transferencia.Monto)
	}
	if transferencia.Estado != EstadoTransferenciaCreada {
		t.Errorf("Estado = %q, quiero %q", transferencia.Estado, EstadoTransferenciaCreada)
	}
	if !transferencia.CreadaEn.Equal(creada) || transferencia.CreadaEn.Location() != time.UTC {
		t.Errorf("CreadaEn = %s (%s), quiero el mismo instante en UTC", transferencia.CreadaEn, transferencia.CreadaEn.Location())
	}
	if !transferencia.ActualizadaEn.Equal(actualizada) || transferencia.ActualizadaEn.Location() != time.UTC {
		t.Errorf("ActualizadaEn = %s (%s), quiero el mismo instante en UTC", transferencia.ActualizadaEn, transferencia.ActualizadaEn.Location())
	}
}

// NF-A2-01: el compare-and-set sin filas se traduce en ErrTransicionNoAplicada.
func TestAvanzarEstadoTransferenciaTraduceSinFilas(t *testing.T) {
	t.Parallel()

	var eventos []string
	servicio := &Servicio{pool: fakeAbridor{
		tx: &fakeTransaccion{eventos: &eventos, fila: filaSinResultado{err: pgx.ErrNoRows}},
	}}

	_, err := servicio.avanzarEstadoTransferencia(t.Context(), uuid.New(),
		EstadoTransferenciaCreada, EstadoTransferenciaReservada)
	if !errors.Is(err, ErrTransicionNoAplicada) {
		t.Fatalf("err = %v, quiero ErrTransicionNoAplicada", err)
	}
}
