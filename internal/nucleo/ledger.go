package nucleo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// CuentaCustodiaBanco es la cuenta ómnibus del banco custodio.
const CuentaCustodiaBanco = "activo:custodia:banco"

// CuentaLiquidacionPendienteRiel es el dinero que salió del usuario y aún no se liquidó contra el riel.
const CuentaLiquidacionPendienteRiel = "pasivo:liquidacion_pendiente:riel"

// CuentaGMFPorPagar es el gravamen retenido y pendiente de entregar a la autoridad tributaria.
const CuentaGMFPorPagar = "pasivo:gmf_por_pagar"

// Los seis tipos de asiento del conjunto cerrado del diario (HU-NF-05, Dependencias).
const (
	TipoAsientoDeposito              = "deposito"
	TipoAsientoRetencion             = "retencion"
	TipoAsientoTransferenciaInterna  = "transferencia_interna"
	TipoAsientoDespachoRiel          = "despacho_riel"
	TipoAsientoLiquidacionConfirmada = "liquidacion_confirmada"
	TipoAsientoCompensacion          = "compensacion"
)

// ErrUsuarioVacio reporta la apertura de subcuentas sin un usuario_id que las identifique.
var ErrUsuarioVacio = errors.New("nucleo: el usuario_id no puede estar vacío")

// ErrUsuarioIDInvalido reporta un usuario_id que no tiene forma de UUID.
var ErrUsuarioIDInvalido = errors.New("nucleo: el usuario_id debe ser un UUID válido")

// NombreCuentaDisponible arma la subcuenta de lo que el usuario puede gastar.
func NombreCuentaDisponible(usuarioID string) string {
	return "pasivo:usuario:" + usuarioID + ":disponible"
}

// NombreCuentaRetenido arma la subcuenta del dinero apartado en una operación en curso.
func NombreCuentaRetenido(usuarioID string) string {
	return "pasivo:usuario:" + usuarioID + ":retenido"
}

// SolicitudRetencion es lo que el asiento de retención necesita, con el gravamen en cero cuando no aplica (HU-NF-06, criterio 13).
type SolicitudRetencion struct {
	TransferenciaID uuid.UUID
	UsuarioID       string
	Monto           Centavos
	Gravamen        Centavos
	Moneda          string
	OcurridoEn      time.Time
}

// retener escribe el asiento de retención del emisor, de dos líneas o de cuatro cuando lleva gravamen (HU-NF-06, criterios 7 y 13).
func retener(ctx context.Context, q *repositorio.Queries, solicitud SolicitudRetencion) error {
	if solicitud.Moneda == "" {
		solicitud.Moneda = MonedaCOP
	}
	lineas, err := lineasDeRetencion(solicitud)
	if err != nil {
		return err
	}
	asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
		Tipo:            TipoAsientoRetencion,
		TransferenciaID: pgtype.UUID{Bytes: solicitud.TransferenciaID, Valid: solicitud.TransferenciaID != uuid.Nil},
		OcurridoEn:      pgtype.Timestamptz{Time: solicitud.OcurridoEn, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("nucleo: asentar la retención: %w", err)
	}
	for _, linea := range lineas {
		linea.EntryID = asiento.ID
		if _, err := q.InsertarLinea(ctx, linea); err != nil {
			return fmt.Errorf("nucleo: escribir una línea de la retención: %w", err)
		}
	}
	return nil
}

// lineasDeRetencion arma las dos líneas del monto y, si hay gravamen, sus dos líneas propias separadas (HU-NF-06, criterio 13.3).
func lineasDeRetencion(solicitud SolicitudRetencion) ([]repositorio.InsertarLineaParams, error) {
	if solicitud.Monto <= 0 || solicitud.Gravamen < 0 {
		return nil, ErrMontoInvalido
	}
	disponible := NombreCuentaDisponible(solicitud.UsuarioID)
	retenido := NombreCuentaRetenido(solicitud.UsuarioID)
	creditoMonto, err := Centavos(0).Restar(solicitud.Monto)
	if err != nil {
		return nil, err
	}
	lineas := []repositorio.InsertarLineaParams{
		{CuentaID: disponible, Monto: solicitud.Monto.Int64(), Moneda: solicitud.Moneda},
		{CuentaID: retenido, Monto: creditoMonto.Int64(), Moneda: solicitud.Moneda},
	}
	if solicitud.Gravamen == 0 {
		return lineas, nil
	}
	creditoGravamen, err := Centavos(0).Restar(solicitud.Gravamen)
	if err != nil {
		return nil, err
	}
	return append(lineas,
		repositorio.InsertarLineaParams{CuentaID: disponible, Monto: solicitud.Gravamen.Int64(), Moneda: solicitud.Moneda},
		repositorio.InsertarLineaParams{CuentaID: CuentaGMFPorPagar, Monto: creditoGravamen.Int64(), Moneda: solicitud.Moneda},
	), nil
}

// SolicitudTransferenciaInterna es lo que el asiento de transferencia interna necesita del emisor y el receptor.
type SolicitudTransferenciaInterna struct {
	TransferenciaID uuid.UUID
	EmisorID        string
	ReceptorID      string
	Monto           Centavos
	Moneda          string
	OcurridoEn      time.Time
}

// transferirInterna asienta la salida del retenido del emisor hacia el disponible del receptor, sin tocar el activo (Apéndice B).
func transferirInterna(ctx context.Context, q *repositorio.Queries, solicitud SolicitudTransferenciaInterna) error {
	if solicitud.Monto <= 0 {
		return ErrMontoInvalido
	}
	if solicitud.Moneda == "" {
		solicitud.Moneda = MonedaCOP
	}
	credito, err := Centavos(0).Restar(solicitud.Monto)
	if err != nil {
		return err
	}
	asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
		Tipo:            TipoAsientoTransferenciaInterna,
		TransferenciaID: pgtype.UUID{Bytes: solicitud.TransferenciaID, Valid: solicitud.TransferenciaID != uuid.Nil},
		OcurridoEn:      pgtype.Timestamptz{Time: solicitud.OcurridoEn, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("nucleo: asentar la transferencia interna: %w", err)
	}
	lineas := []repositorio.InsertarLineaParams{
		{EntryID: asiento.ID, CuentaID: NombreCuentaRetenido(solicitud.EmisorID), Monto: solicitud.Monto.Int64(), Moneda: solicitud.Moneda},
		{EntryID: asiento.ID, CuentaID: NombreCuentaDisponible(solicitud.ReceptorID), Monto: credito.Int64(), Moneda: solicitud.Moneda},
	}
	for _, linea := range lineas {
		if _, err := q.InsertarLinea(ctx, linea); err != nil {
			return fmt.Errorf("nucleo: escribir una línea de la transferencia interna: %w", err)
		}
	}
	return nil
}

// AbrirSubcuentasDeUsuario crea las dos subcuentas del usuario y devuelve cuántas creó; es idempotente.
func (s *Servicio) AbrirSubcuentasDeUsuario(ctx context.Context, usuarioID string) (int64, error) {
	if usuarioID == "" {
		return 0, ErrUsuarioVacio
	}
	if _, err := uuid.Parse(usuarioID); err != nil {
		return 0, ErrUsuarioIDInvalido
	}
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (int64, error) {
			return q.InsertarSubcuentasDeUsuario(ctx, repositorio.InsertarSubcuentasDeUsuarioParams{
				Disponible: NombreCuentaDisponible(usuarioID),
				Retenido:   NombreCuentaRetenido(usuarioID),
				TitularID:  pgtype.Text{String: usuarioID, Valid: true},
			})
		})
}

// Saldo devuelve, con el signo del usuario, lo que puede gastar en una cuenta del diario.
func (s *Servicio) Saldo(ctx context.Context, cuentaID string) (Centavos, error) {
	saldo, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (int64, error) {
			return q.SaldoDeCuenta(ctx, cuentaID)
		})
	if err != nil {
		return 0, fmt.Errorf("nucleo: consultar saldo de %q: %w", cuentaID, err)
	}
	return Centavos(0).Restar(Centavos(saldo))
}

// ErrEsperaDeBloqueoAgotada reporta que una cuenta no se pudo bloquear dentro del límite de espera.
var ErrEsperaDeBloqueoAgotada = errors.New("nucleo: la espera por el candado de las cuentas superó el límite")

// ErrCuentaDelCandadoAusente reporta que alguna cuenta pedida para el candado no existe.
var ErrCuentaDelCandadoAusente = errors.New("nucleo: alguna cuenta del candado no existe")

// bloquearOrdenadas toma el candado ordenado por identificador antes de los INSERT que referencian la cuenta; invertirlo produce deadlock (capítulo 12).
func bloquearOrdenadas(ctx context.Context, q *repositorio.Queries, cuentas ...string) error {
	esperadas := cuentasUnicasOrdenadas(cuentas)
	if len(esperadas) == 0 {
		return nil
	}
	bloqueadas, err := q.BloquearCuentasOrdenadas(ctx, esperadas)
	if err != nil {
		if esEsperaDeBloqueoAgotada(err) {
			return fmt.Errorf("%w: %w", ErrEsperaDeBloqueoAgotada, err)
		}
		return fmt.Errorf("nucleo: bloquear %d cuenta(s): %w", len(esperadas), err)
	}
	if len(bloqueadas) != len(esperadas) {
		return fmt.Errorf("%w: se pidieron %d y se bloquearon %d", ErrCuentaDelCandadoAusente, len(esperadas), len(bloqueadas))
	}
	return nil
}

// cuentasUnicasOrdenadas deduplica y ordena los identificadores para fijar el orden del candado.
func cuentasUnicasOrdenadas(cuentas []string) []string {
	unicas := make(map[string]struct{}, len(cuentas))
	ordenadas := make([]string, 0, len(cuentas))
	for _, cuenta := range cuentas {
		if cuenta == "" {
			continue
		}
		if _, visto := unicas[cuenta]; visto {
			continue
		}
		unicas[cuenta] = struct{}{}
		ordenadas = append(ordenadas, cuenta)
	}
	sort.Strings(ordenadas)
	return ordenadas
}

// esEsperaDeBloqueoAgotada detecta el SQLSTATE que Postgres devuelve cuando vence el lock_timeout.
func esEsperaDeBloqueoAgotada(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable
}
