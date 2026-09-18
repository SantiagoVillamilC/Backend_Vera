package nucleo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// dbDeOutbox es un doble escrito a mano que registra el INSERT de la intención de despacho.
type dbDeOutbox struct {
	tipo    string
	payload []byte
}

// Exec no lo usa el encolado.
func (db *dbDeOutbox) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

// Query no lo usa el encolado.
func (db *dbDeOutbox) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, nil
}

// QueryRow simula el RETURNING de la fila de outbox.
func (db *dbDeOutbox) QueryRow(_ context.Context, _ string, args ...interface{}) pgx.Row {
	db.tipo = args[0].(string)
	db.payload = args[1].([]byte)
	return filaConID{id: 1}
}

func TestEncolarDespachoEscribeLaIntencion(t *testing.T) {
	t.Parallel()

	transferenciaID := uuid.New()
	db := &dbDeOutbox{}
	id, err := encolarDespacho(t.Context(), repositorio.New(db), transferenciaID)
	if err != nil {
		t.Fatalf("encolarDespacho: %v", err)
	}
	if id != 1 {
		t.Errorf("id de la fila encolada = %d, quiero 1", id)
	}
	if db.tipo != TipoOutboxDespachoTransferencia {
		t.Errorf("tipo = %q, quiero %q", db.tipo, TipoOutboxDespachoTransferencia)
	}
	var payload PayloadDespacho
	if err := json.Unmarshal(db.payload, &payload); err != nil {
		t.Fatalf("payload no es JSON: %v", err)
	}
	if payload.TransferenciaID != transferenciaID {
		t.Errorf("transferencia_id = %s, quiero %s", payload.TransferenciaID, transferenciaID)
	}
}

// M3: ordenDeTransferencia mapea campo por campo lo que el riel necesita, con datos distinguibles.
func TestOrdenDeTransferenciaMapeaCadaCampo(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	transferencia := repositorio.Transferencia{
		ID:             pgtype.UUID{Bytes: id, Valid: true},
		UsuarioID:      "usuario-9f",
		CuentaOrigen:   "pasivo:usuario:usuario-9f:disponible",
		LlaveDestino:   "llave-destino-77",
		Monto:          1234567,
		Moneda:         "COP",
		IdempotencyKey: "idem-abc",
		Estado:         EstadoTransferenciaReservada,
	}

	orden := ordenDeTransferencia(transferencia)
	if orden.TransferenciaID != id {
		t.Errorf("TransferenciaID = %s, quiero %s", orden.TransferenciaID, id)
	}
	if orden.IdempotencyKey != transferencia.IdempotencyKey {
		t.Errorf("IdempotencyKey = %q, quiero %q", orden.IdempotencyKey, transferencia.IdempotencyKey)
	}
	if orden.Llave != transferencia.LlaveDestino {
		t.Errorf("Llave = %q, quiero %q (la llave destino, no otra columna)", orden.Llave, transferencia.LlaveDestino)
	}
	if orden.Llave == transferencia.CuentaOrigen {
		t.Error("Llave salió de CuentaOrigen")
	}
	if orden.Monto != Centavos(transferencia.Monto) {
		t.Errorf("Monto = %d, quiero %d", orden.Monto, transferencia.Monto)
	}
	if orden.Moneda != transferencia.Moneda {
		t.Errorf("Moneda = %q, quiero %q", orden.Moneda, transferencia.Moneda)
	}
}

// TestPoliticaReintentoOutboxDemora fija la curva resuelta: min(Base*Factor^(intentos-1), Tope).
func TestPoliticaReintentoOutboxDemora(t *testing.T) {
	t.Parallel()

	porDefecto := PoliticaReintentoOutboxPorDefecto()
	tests := map[string]struct {
		politica PoliticaReintentoOutbox
		intentos int
		quiero   time.Duration
	}{
		"primer intento":                 {politica: porDefecto, intentos: 1, quiero: 30 * time.Second},
		"segundo intento":                {politica: porDefecto, intentos: 2, quiero: time.Minute},
		"tercer intento":                 {politica: porDefecto, intentos: 3, quiero: 2 * time.Minute},
		"tope por intentos":              {politica: porDefecto, intentos: 7, quiero: 30 * time.Minute},
		"cero intentos":                  {politica: porDefecto, intentos: 0, quiero: 0},
		"factor uno":                     {politica: PoliticaReintentoOutbox{Base: 5 * time.Second, Factor: 1, Tope: time.Hour}, intentos: 4, quiero: 5 * time.Second},
		"tope pequeno":                   {politica: PoliticaReintentoOutbox{Base: time.Second, Factor: 10, Tope: 3 * time.Second}, intentos: 2, quiero: 3 * time.Second},
		"politica vacia usa el default":  {politica: PoliticaReintentoOutbox{}, intentos: 2, quiero: time.Minute},
		"tope no alcanzado en el limite": {politica: porDefecto, intentos: 6, quiero: 16 * time.Minute},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := tc.politica.Demora(tc.intentos); got != tc.quiero {
				t.Errorf("Demora(%d) = %v, quiero %v", tc.intentos, got, tc.quiero)
			}
		})
	}
}
