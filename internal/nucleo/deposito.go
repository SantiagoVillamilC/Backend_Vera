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

// Los cuatro estados del ciclo de vida de una intención de depósito.
const (
	EstadoDepositoPendiente  = "pendiente"
	EstadoDepositoConfirmada = "confirmada"
	EstadoDepositoRechazada  = "rechazada"
	EstadoDepositoExpirada   = "expirada"
)

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

// ErrConfirmacionConflictiva reporta un monto o una moneda distintos a los de la intención.
var ErrConfirmacionConflictiva = errors.New("nucleo: la confirmación no coincide con la intención")

// ErrConfirmacionTardia reporta una confirmación sobre una intención rechazada o expirada.
var ErrConfirmacionTardia = errors.New("nucleo: confirmación tardía sobre una intención no pendiente")

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

// ConfirmacionDeposito es la señal del custodio simulado que confirma una intención pendiente.
type ConfirmacionDeposito struct {
	Referencia string
	Monto      Centavos
	Moneda     string
	OcurridoEn time.Time
	CUS        string
}

// ConfirmarDeposito confirma la intención, escribe el asiento de fondeo y es idempotente en una sola transacción.
func (s *Servicio) ConfirmarDeposito(ctx context.Context, confirmacion ConfirmacionDeposito) (IntencionDeposito, error) {
	moneda := confirmacion.Moneda
	if moneda == "" {
		moneda = MonedaCOP
	}
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (IntencionDeposito, error) {
			intencion, err := q.ObtenerDepositoPorReferencia(ctx, confirmacion.Referencia)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return IntencionDeposito{}, ErrDepositoNoEncontrado
				}
				return IntencionDeposito{}, fmt.Errorf("nucleo: leer el depósito %q: %w", confirmacion.Referencia, err)
			}
			if !montoYMonedaCoinciden(intencion, confirmacion.Monto, moneda) {
				return IntencionDeposito{}, ErrConfirmacionConflictiva
			}

			confirmada, err := q.ConfirmarDeposito(ctx, repositorio.ConfirmarDepositoParams{
				ID:  intencion.ID,
				Cus: textoONulo(confirmacion.CUS),
			})
			if err == nil {
				return s.asentarFondeo(ctx, q, confirmada, confirmacion.OcurridoEn)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return IntencionDeposito{}, fmt.Errorf("nucleo: confirmar el depósito %q: %w", confirmacion.Referencia, err)
			}
			return s.resolverConfirmacionRepetida(ctx, q, confirmacion, moneda)
		})
}

// textoONulo distingue el texto vacío del ausente para que la columna opcional quede NULL.
func textoONulo(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// RechazarDeposito deja rechazada una intención pendiente por su referencia, sin escribir el diario.
func (s *Servicio) RechazarDeposito(ctx context.Context, referencia string) (IntencionDeposito, error) {
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (IntencionDeposito, error) {
			intencion, err := q.ObtenerDepositoPorReferencia(ctx, referencia)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return IntencionDeposito{}, ErrDepositoNoEncontrado
				}
				return IntencionDeposito{}, fmt.Errorf("nucleo: leer el depósito %q: %w", referencia, err)
			}
			filas, err := q.RechazarDeposito(ctx, intencion.ID)
			if err != nil {
				return IntencionDeposito{}, fmt.Errorf("nucleo: rechazar el depósito %q: %w", referencia, err)
			}
			if filas == 1 {
				intencion.Estado = EstadoDepositoRechazada
				return intencionDeDeposito(intencion), nil
			}
			actual, err := q.ObtenerDepositoPorReferencia(ctx, referencia)
			if err != nil {
				return IntencionDeposito{}, fmt.Errorf("nucleo: releer el depósito %q: %w", referencia, err)
			}
			return resolverRechazoRepetido(actual)
		})
}

// resolverRechazoRepetido decide el resultado de un rechazo que no movió ninguna fila.
func resolverRechazoRepetido(intencion repositorio.Deposito) (IntencionDeposito, error) {
	switch intencion.Estado {
	case EstadoDepositoRechazada:
		return intencionDeDeposito(intencion), nil
	case EstadoDepositoConfirmada:
		return IntencionDeposito{}, ErrConfirmacionConflictiva
	case EstadoDepositoExpirada:
		return IntencionDeposito{}, ErrConfirmacionTardia
	default:
		return IntencionDeposito{}, fmt.Errorf("nucleo: el depósito %q quedó en %q tras un compare-and-set fallido", intencion.Referencia, intencion.Estado)
	}
}

// ExpirarIntenciones marca expiradas las intenciones pendientes más viejas que el plazo y devuelve cuántas.
func (s *Servicio) ExpirarIntenciones(ctx context.Context, ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, nil
	}
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (int, error) {
			filas, err := q.ExpiarDepositosVencidos(ctx, ttl.Seconds())
			if err != nil {
				return 0, fmt.Errorf("nucleo: expirar intenciones pendientes: %w", err)
			}
			return int(filas), nil
		})
}

// montoYMonedaCoinciden compara la confirmación contra los datos inmutables de la intención.
func montoYMonedaCoinciden(intencion repositorio.Deposito, monto Centavos, moneda string) bool {
	return intencion.Monto == monto.Int64() && intencion.Moneda == moneda
}

// resolverConfirmacionRepetida relee la intención tras un compare-and-set sin filas.
func (s *Servicio) resolverConfirmacionRepetida(ctx context.Context, q *repositorio.Queries, confirmacion ConfirmacionDeposito, moneda string) (IntencionDeposito, error) {
	actual, err := q.ObtenerDepositoPorReferencia(ctx, confirmacion.Referencia)
	if err != nil {
		return IntencionDeposito{}, fmt.Errorf("nucleo: releer el depósito %q: %w", confirmacion.Referencia, err)
	}
	switch actual.Estado {
	case EstadoDepositoConfirmada:
		if !montoYMonedaCoinciden(actual, confirmacion.Monto, moneda) {
			return IntencionDeposito{}, ErrConfirmacionConflictiva
		}
		return intencionDeDeposito(actual), nil
	case EstadoDepositoRechazada, EstadoDepositoExpirada:
		s.escalarConfirmacionTardia(ctx, actual)
		return IntencionDeposito{}, ErrConfirmacionTardia
	default:
		return IntencionDeposito{}, fmt.Errorf("nucleo: el depósito %q quedó en %q tras un compare-and-set fallido", confirmacion.Referencia, actual.Estado)
	}
}

// escalarConfirmacionTardia deja la traza de una confirmación que llegó sobre una intención ya cerrada.
func (s *Servicio) escalarConfirmacionTardia(ctx context.Context, intencion repositorio.Deposito) {
	if s.rastreador == nil {
		return
	}
	s.rastreador.Capturar(ctx, ErrConfirmacionTardia, ContextoError{
		Operacion: "confirmacion_tardia",
		Canal:     CanalSoloLog,
		Campos: []Campo{
			{Clave: "referencia", Valor: intencion.Referencia},
			{Clave: "usuario_id", Valor: intencion.UsuarioID},
			{Clave: "estado", Valor: intencion.Estado},
		},
	})
}

// asentarFondeo escribe el asiento de fondeo con débito en custodia y crédito en el disponible.
func (s *Servicio) asentarFondeo(ctx context.Context, q *repositorio.Queries, intencion repositorio.Deposito, ocurridoEn time.Time) (IntencionDeposito, error) {
	asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
		Tipo:       TipoAsientoDeposito,
		DepositoID: intencion.ID,
		OcurridoEn: pgtype.Timestamptz{Time: ocurridoEn, Valid: true},
	})
	if err != nil {
		return IntencionDeposito{}, s.falloDeAsiento(ctx, intencion, err)
	}
	monto := Centavos(intencion.Monto)
	credito, err := Centavos(0).Restar(monto)
	if err != nil {
		return IntencionDeposito{}, s.falloDeAsiento(ctx, intencion, err)
	}
	lineas := []repositorio.InsertarLineaParams{
		{EntryID: asiento.ID, CuentaID: CuentaCustodiaBanco, Monto: monto.Int64(), Moneda: intencion.Moneda},
		{EntryID: asiento.ID, CuentaID: NombreCuentaDisponible(intencion.UsuarioID), Monto: credito.Int64(), Moneda: intencion.Moneda},
	}
	for _, linea := range lineas {
		if _, err := q.InsertarLinea(ctx, linea); err != nil {
			return IntencionDeposito{}, s.falloDeAsiento(ctx, intencion, err)
		}
	}
	return intencionDeDeposito(intencion), nil
}

// falloDeAsiento captura la falla de escritura del asiento y la devuelve para un 500.
func (s *Servicio) falloDeAsiento(ctx context.Context, intencion repositorio.Deposito, err error) error {
	if s.rastreador != nil {
		s.rastreador.Capturar(ctx, err, ContextoError{
			Operacion: "confirmar_deposito",
			Canal:     CanalSoloLog,
			Campos: []Campo{
				{Clave: "referencia", Valor: intencion.Referencia},
				{Clave: "usuario_id", Valor: intencion.UsuarioID},
			},
		})
	}
	return fmt.Errorf("nucleo: asentar el fondeo del depósito %q: %w", intencion.Referencia, err)
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
