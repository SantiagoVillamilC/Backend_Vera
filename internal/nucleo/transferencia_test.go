package nucleo

import (
	"context"
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

// lectorNivelKYCFalso delega en fn, para simular un lector de KYC que falla por timeout o por un tipo de parte desconocido.
type lectorNivelKYCFalso struct {
	fn func(ctx context.Context, usuarioID string) (string, error)
}

func (l lectorNivelKYCFalso) NivelKYC(ctx context.Context, usuarioID string) (string, error) {
	return l.fn(ctx, usuarioID)
}

// resolutorDestinoFalso resuelve siempre al mismo destino, sin abrir ninguna conexión real.
type resolutorDestinoFalso struct {
	destinoID string
}

func (r resolutorDestinoFalso) UsuarioIDDeDestino(context.Context, string) (string, bool, error) {
	return r.destinoID, true, nil
}

// errTipoDeParteDesconocidoSimulado imita, sin depender de internal/auth/kyc (evita el ciclo de import),
// el error que nivelDeTipo devuelve hoy para un tipo de parte fuera del modelo.
var errTipoDeParteDesconocidoSimulado = errors.New("kyc: tipo de parte desconocido: \"fantasma\"")

// TestHU_NF_06_Criterio10_EjecutarTransferenciaSinRespuestaDeKYCFallaCerrado cubre que un lector de KYC que falla, por timeout o por tipo de parte desconocido, rechaza sin abrir la transacción (HU-NF-06, criterio 10).
func TestHU_NF_06_Criterio10_EjecutarTransferenciaSinRespuestaDeKYCFallaCerrado(t *testing.T) {
	t.Parallel()

	usuarioID := uuid.NewString()
	destinos := resolutorDestinoFalso{destinoID: uuid.NewString()}

	casos := map[string]error{
		"timeout del lector":               context.DeadlineExceeded,
		"tipo de parte desconocido en kyc": errTipoDeParteDesconocidoSimulado,
	}
	for nombre, causa := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			kyc := lectorNivelKYCFalso{fn: func(context.Context, string) (string, error) {
				return "", causa
			}}
			servicio := NewServicio(nil, kyc, nil, nil, nil, ConResolutorDestino(destinos))

			solicitud := SolicitudTransferencia{
				UsuarioID:      usuarioID,
				CuentaOrigen:   "pasivo:usuario:" + usuarioID + ":disponible",
				LlaveDestino:   "llave-beto",
				Monto:          Centavos(1000),
				Moneda:         MonedaCOP,
				IdempotencyKey: "k-1",
			}
			transferencia, err := servicio.ejecutarTransferencia(t.Context(), solicitud)
			if !errors.Is(err, causa) {
				t.Fatalf("err = %v, quiero que envuelva %v: el fallo del lector de KYC no puede dejar pasar la transferencia", err, causa)
			}
			if transferencia != (Transferencia{}) {
				t.Fatalf("transferencia = %+v, quiero la vacía: ningún éxito silencioso con tope infinito", transferencia)
			}
		})
	}
}

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

// HU-NF-06, criterio 3 de HU-NF-10 que esta historia comparte sin numeral propio: la moneda
// distinta de COP se rechaza antes de resolver el destino, y la vacía pasa igual que COP explícita.
func TestEjecutarTransferenciaValidaLaMonedaAntesDeLaTransaccion(t *testing.T) {
	t.Parallel()

	base := SolicitudTransferencia{
		UsuarioID:      "u-1",
		CuentaOrigen:   "pasivo:usuario:u-1:disponible",
		LlaveDestino:   "llave-beto",
		Monto:          Centavos(1000),
		IdempotencyKey: "k-1",
	}
	servicio := &Servicio{}

	casos := map[string]struct {
		moneda   string
		esperado error
	}{
		"COP explicita pasa la validación de moneda": {moneda: "COP", esperado: ErrResolutorDestinoNoDisponible},
		"moneda vacía pasa la validación de moneda":  {moneda: "", esperado: ErrResolutorDestinoNoDisponible},
		"moneda distinta de COP se rechaza":          {moneda: "USD", esperado: ErrMonedaInvalida},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			solicitud := base
			solicitud.Moneda = caso.moneda
			_, err := servicio.ejecutarTransferencia(t.Context(), solicitud)
			if !errors.Is(err, caso.esperado) {
				t.Fatalf("err = %v, quiero %v", err, caso.esperado)
			}
		})
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
