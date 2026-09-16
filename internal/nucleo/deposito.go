package nucleo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// EstadoDepositoPendiente es el estado con el que nace toda intención de depósito.
const EstadoDepositoPendiente = "pendiente"

// Las dos restricciones únicas de nucleo.deposito que la creación debe distinguir.
const (
	restriccionIdempotenciaDeposito = "deposito_idempotencia_unica"
	restriccionReferenciaDeposito   = "deposito_referencia_unica"
)

// intentosReferenciaDeposito acota las regeneraciones de referencia ante una colisión global.
const intentosReferenciaDeposito = 5

// ErrDepositoNoEncontrado reporta una intención inexistente o de otro usuario; el handler la vuelve 404.
var ErrDepositoNoEncontrado = errors.New("nucleo: intención de depósito no encontrada")

// ErrReferenciaDepositoAgotada reporta que ninguna referencia libre se pudo insertar.
var ErrReferenciaDepositoAgotada = errors.New("nucleo: no se pudo generar una referencia de depósito única")

// IntencionDeposito es la vista de una intención de depósito, sin tipos del repositorio.
type IntencionDeposito struct {
	ID         uuid.UUID
	Referencia string
	Monto      Centavos
	Moneda     string
	Estado     string
	CreadaEn   time.Time
}

// NuevaReferenciaDeDeposito es el único generador de la referencia propia de la intención.
func NuevaReferenciaDeDeposito() string {
	return "dep-" + uuid.NewString()
}

// CrearIntencionDeposito crea la intención pendiente del usuario; con una clave ya usada devuelve la guardada.
func (s *Servicio) CrearIntencionDeposito(ctx context.Context, usuarioID string, monto Centavos, moneda, idempotencyKey string) (IntencionDeposito, bool, error) {
	if moneda == "" {
		moneda = MonedaCOP
	}
	for range intentosReferenciaDeposito {
		intencion, err := s.insertarIntencionDeposito(ctx, usuarioID, monto, moneda, idempotencyKey)
		if err == nil {
			return intencion, true, nil
		}
		if !esViolacionDeUnicidad(err) {
			return IntencionDeposito{}, false, fmt.Errorf("nucleo: crear intención de depósito de %q: %w", usuarioID, err)
		}
		if restriccionViolada(err) == restriccionIdempotenciaDeposito {
			guardada, err := s.obtenerIntencionPorIdempotencia(ctx, usuarioID, idempotencyKey)
			if err != nil {
				return IntencionDeposito{}, false, err
			}
			return guardada, false, nil
		}
		if restriccionViolada(err) != restriccionReferenciaDeposito {
			return IntencionDeposito{}, false, fmt.Errorf("nucleo: crear intención de depósito de %q: %w", usuarioID, err)
		}
	}
	return IntencionDeposito{}, false, ErrReferenciaDepositoAgotada
}

// ConsultarIntencionDeposito devuelve la intención del usuario; la de otro usuario no se expone.
func (s *Servicio) ConsultarIntencionDeposito(ctx context.Context, usuarioID string, id uuid.UUID) (IntencionDeposito, error) {
	fila, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Deposito, error) {
			return q.ObtenerDeposito(ctx, pgtype.UUID{Bytes: id, Valid: true})
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IntencionDeposito{}, ErrDepositoNoEncontrado
		}
		return IntencionDeposito{}, fmt.Errorf("nucleo: consultar intención %s: %w", id, err)
	}
	if fila.UsuarioID != usuarioID {
		return IntencionDeposito{}, ErrDepositoNoEncontrado
	}
	return intencionDeDeposito(fila), nil
}

// insertarIntencionDeposito inserta una intención con referencia nueva en su propia transacción.
func (s *Servicio) insertarIntencionDeposito(ctx context.Context, usuarioID string, monto Centavos, moneda, idempotencyKey string) (IntencionDeposito, error) {
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (IntencionDeposito, error) {
			fila, err := q.InsertarDeposito(ctx, repositorio.InsertarDepositoParams{
				ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
				UsuarioID:      usuarioID,
				Monto:          monto.Int64(),
				Moneda:         moneda,
				Referencia:     NuevaReferenciaDeDeposito(),
				IdempotencyKey: idempotencyKey,
			})
			if err != nil {
				return IntencionDeposito{}, err
			}
			return intencionDeDeposito(fila), nil
		})
}

// obtenerIntencionPorIdempotencia lee en una transacción aparte la intención ya guardada por la clave.
func (s *Servicio) obtenerIntencionPorIdempotencia(ctx context.Context, usuarioID, idempotencyKey string) (IntencionDeposito, error) {
	fila, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Deposito, error) {
			return q.ObtenerDepositoPorIdempotencyKey(ctx, repositorio.ObtenerDepositoPorIdempotencyKeyParams{
				UsuarioID:      usuarioID,
				IdempotencyKey: idempotencyKey,
			})
		})
	if err != nil {
		return IntencionDeposito{}, fmt.Errorf("nucleo: leer intención de depósito de %q: %w", usuarioID, err)
	}
	return intencionDeDeposito(fila), nil
}

// intencionDeDeposito envuelve la fila cruda en el tipo del núcleo, con el monto en Centavos.
func intencionDeDeposito(fila repositorio.Deposito) IntencionDeposito {
	return IntencionDeposito{
		ID:         uuid.UUID(fila.ID.Bytes),
		Referencia: fila.Referencia,
		Monto:      Centavos(fila.Monto),
		Moneda:     fila.Moneda,
		Estado:     fila.Estado,
		CreadaEn:   fila.CreadaEn.Time.UTC(),
	}
}

// esViolacionDeUnicidad reporta si el error del driver es una violación de índice único.
func esViolacionDeUnicidad(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}

// restriccionViolada devuelve el nombre de la restricción única que rechazó la fila.
func restriccionViolada(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}
