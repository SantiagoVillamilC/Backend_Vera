package nucleo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Los siete estados del ciclo de vida de una transferencia (capítulo 8).
const (
	EstadoTransferenciaCreada        = "creada"
	EstadoTransferenciaReservada     = "reservada"
	EstadoTransferenciaDespachada    = "despachada"
	EstadoTransferenciaConfirmada    = "confirmada"
	EstadoTransferenciaFallida       = "fallida"
	EstadoTransferenciaIndeterminada = "indeterminada"
	EstadoTransferenciaCompensada    = "compensada"
)

// restriccionIdempotenciaTransferencia es la restricción única que la creación debe distinguir.
const restriccionIdempotenciaTransferencia = "transferencia_idempotencia_unica"

// ErrTransferenciaNoEncontrada reporta una transferencia inexistente o de otro usuario.
var ErrTransferenciaNoEncontrada = errors.New("nucleo: transferencia no encontrada")

// ErrTransicionNoAplicada reporta un compare-and-set de estado que no movió ninguna fila.
var ErrTransicionNoAplicada = errors.New("nucleo: transición de estado no aplicada")

// ErrCuentaOrigenAjena reporta una cuenta origen inexistente o cuyo titular no es el usuario.
var ErrCuentaOrigenAjena = errors.New("nucleo: la cuenta origen no pertenece al usuario")

// ErrIdempotenciaEnConflicto reporta una llave de idempotencia reutilizada con otro cuerpo.
var ErrIdempotenciaEnConflicto = errors.New("nucleo: la llave de idempotencia ya existe con otro cuerpo")

// Transferencia es la vista de una transferencia, sin tipos del repositorio.
type Transferencia struct {
	ID            uuid.UUID
	UsuarioID     string
	CuentaOrigen  string
	LlaveDestino  string
	Monto         Centavos
	Moneda        string
	Estado        string
	CreadaEn      time.Time
	ActualizadaEn time.Time
}

// CrearTransferencia inserta la transferencia en estado creada; con una clave ya usada devuelve la guardada.
func (s *Servicio) CrearTransferencia(ctx context.Context, usuarioID, cuentaOrigen, llaveDestino string, monto Centavos, moneda, idempotencyKey string) (Transferencia, bool, error) {
	if moneda == "" {
		moneda = MonedaCOP
	}
	transferencia, err := s.insertarTransferencia(ctx, usuarioID, cuentaOrigen, llaveDestino, monto, moneda, idempotencyKey)
	if err == nil {
		return transferencia, true, nil
	}
	if !esViolacionDeUnicidad(err) || restriccionViolada(err) != restriccionIdempotenciaTransferencia {
		return Transferencia{}, false, fmt.Errorf("nucleo: crear transferencia de %q: %w", usuarioID, err)
	}
	guardada, err := s.obtenerTransferenciaPorIdempotencia(ctx, usuarioID, idempotencyKey)
	if err != nil {
		return Transferencia{}, false, err
	}
	if guardada.CuentaOrigen != cuentaOrigen || guardada.LlaveDestino != llaveDestino ||
		guardada.Monto != monto || guardada.Moneda != moneda {
		return Transferencia{}, false, ErrIdempotenciaEnConflicto
	}
	return guardada, false, nil
}

// ConsultarTransferencia devuelve la transferencia del usuario; la de otro usuario no se expone.
func (s *Servicio) ConsultarTransferencia(ctx context.Context, usuarioID string, id uuid.UUID) (Transferencia, error) {
	fila, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Transferencia, error) {
			return q.ObtenerTransferencia(ctx, pgtype.UUID{Bytes: id, Valid: true})
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Transferencia{}, ErrTransferenciaNoEncontrada
		}
		return Transferencia{}, fmt.Errorf("nucleo: consultar transferencia %s: %w", id, err)
	}
	if fila.UsuarioID != usuarioID {
		return Transferencia{}, ErrTransferenciaNoEncontrada
	}
	return transferenciaDeFila(fila), nil
}

// avanzarEstadoTransferencia aplica la transición si el estado esperado coincide; sin filas devuelve ErrTransicionNoAplicada.
func (s *Servicio) avanzarEstadoTransferencia(ctx context.Context, id uuid.UUID, estadoEsperado, estadoNuevo string) (Transferencia, error) {
	fila, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Transferencia, error) {
			return q.AvanzarEstadoTransferencia(ctx, repositorio.AvanzarEstadoTransferenciaParams{
				ID:             pgtype.UUID{Bytes: id, Valid: true},
				EstadoEsperado: estadoEsperado,
				EstadoNuevo:    estadoNuevo,
			})
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return Transferencia{}, ErrTransicionNoAplicada
	}
	if err != nil {
		return Transferencia{}, fmt.Errorf("nucleo: avanzar la transferencia %s a %q: %w", id, estadoNuevo, err)
	}
	return transferenciaDeFila(fila), nil
}

// insertarTransferencia verifica el titular de la cuenta origen e inserta la transferencia en la misma transacción.
func (s *Servicio) insertarTransferencia(ctx context.Context, usuarioID, cuentaOrigen, llaveDestino string, monto Centavos, moneda, idempotencyKey string) (Transferencia, error) {
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (Transferencia, error) {
			titular, err := q.ObtenerTitularDeCuenta(ctx, cuentaOrigen)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return Transferencia{}, ErrCuentaOrigenAjena
				}
				return Transferencia{}, fmt.Errorf("nucleo: leer el titular de la cuenta %q: %w", cuentaOrigen, err)
			}
			if !titular.Valid || titular.String != usuarioID {
				return Transferencia{}, ErrCuentaOrigenAjena
			}
			fila, err := q.InsertarTransferencia(ctx, repositorio.InsertarTransferenciaParams{
				ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
				UsuarioID:      usuarioID,
				CuentaOrigen:   cuentaOrigen,
				LlaveDestino:   llaveDestino,
				Monto:          monto.Int64(),
				Moneda:         moneda,
				IdempotencyKey: idempotencyKey,
				Estado:         EstadoTransferenciaCreada,
			})
			if err != nil {
				return Transferencia{}, err
			}
			return transferenciaDeFila(fila), nil
		})
}

// obtenerTransferenciaPorIdempotencia lee en una transacción aparte la transferencia ya guardada por la clave.
func (s *Servicio) obtenerTransferenciaPorIdempotencia(ctx context.Context, usuarioID, idempotencyKey string) (Transferencia, error) {
	fila, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Transferencia, error) {
			return q.ObtenerTransferenciaPorIdempotencyKey(ctx, repositorio.ObtenerTransferenciaPorIdempotencyKeyParams{
				UsuarioID:      usuarioID,
				IdempotencyKey: idempotencyKey,
			})
		})
	if err != nil {
		return Transferencia{}, fmt.Errorf("nucleo: leer transferencia de %q: %w", usuarioID, err)
	}
	return transferenciaDeFila(fila), nil
}

// transferenciaDeFila envuelve la fila cruda con el monto en Centavos y las dos fechas en UTC.
func transferenciaDeFila(fila repositorio.Transferencia) Transferencia {
	return Transferencia{
		ID:            uuid.UUID(fila.ID.Bytes),
		UsuarioID:     fila.UsuarioID,
		CuentaOrigen:  fila.CuentaOrigen,
		LlaveDestino:  fila.LlaveDestino,
		Monto:         Centavos(fila.Monto),
		Moneda:        fila.Moneda,
		Estado:        fila.Estado,
		CreadaEn:      fila.CreadaEn.Time.UTC(),
		ActualizadaEn: fila.ActualizadaEn.Time.UTC(),
	}
}
