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

// SolicitudEnvioPorLlave es la petición del envío interbancario: la llave de Bre-B, su tipo, el monto y la clave de idempotencia.
type SolicitudEnvioPorLlave struct {
	UsuarioID      string
	CuentaOrigen   string
	TipoLlave      string
	Llave          string
	Monto          Centavos
	Moneda         string
	IdempotencyKey string
}

// TransferirPorLlave reserva el envío por llave y encola su despacho en la misma transacción; devuelve la guardada si la clave ya existía.
func (s *Servicio) TransferirPorLlave(ctx context.Context, solicitud SolicitudEnvioPorLlave) (Transferencia, bool, error) {
	if solicitud.Moneda == "" {
		solicitud.Moneda = MonedaCOP
	}
	transferencia, err := s.ejecutarEnvioPorLlave(ctx, solicitud)
	if err == nil {
		return transferencia, true, nil
	}
	return s.reintentoDeEnvioPorLlave(ctx, solicitud, err)
}

// ejecutarEnvioPorLlave valida, resuelve la llave y el KYC antes de la transacción y abre la reserva con outbox (HU-NF-10, criterios 1 a 5).
func (s *Servicio) ejecutarEnvioPorLlave(ctx context.Context, solicitud SolicitudEnvioPorLlave) (Transferencia, error) {
	if solicitud.Monto <= 0 {
		return Transferencia{}, ErrMontoInvalido
	}
	resolucion, err := s.ResolverLlave(ctx, solicitud.TipoLlave, solicitud.Llave)
	if err != nil {
		return Transferencia{}, err
	}
	if s.kyc == nil {
		return Transferencia{}, ErrKYCNoDisponible
	}
	nivel, err := s.kyc.NivelKYC(ctx, solicitud.UsuarioID)
	if err != nil {
		return Transferencia{}, fmt.Errorf("nucleo: leer el nivel de KYC de %q: %w", solicitud.UsuarioID, err)
	}
	return s.reservarYEncolar(ctx, solicitud, resolucion, nivel, time.Now().UTC())
}

// reservarYEncolar abre la única transacción: crea la fila, verifica límites y saldo, retiene, encola el despacho con la resolución y la deja reservada (criterios 8 a 10).
func (s *Servicio) reservarYEncolar(ctx context.Context, solicitud SolicitudEnvioPorLlave, resolucion ResolucionDirectorio, nivel string, ocurridoEn time.Time) (Transferencia, error) {
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (Transferencia, error) {
			if err := bloquearOrdenadas(ctx, q, solicitud.CuentaOrigen); err != nil {
				return Transferencia{}, err
			}
			fila, err := q.InsertarTransferencia(ctx, repositorio.InsertarTransferenciaParams{
				ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
				UsuarioID:      solicitud.UsuarioID,
				CuentaOrigen:   solicitud.CuentaOrigen,
				LlaveDestino:   solicitud.Llave,
				Monto:          solicitud.Monto.Int64(),
				Moneda:         solicitud.Moneda,
				IdempotencyKey: solicitud.IdempotencyKey,
				Estado:         EstadoTransferenciaCreada,
			})
			if err != nil {
				return Transferencia{}, err
			}
			transferenciaID := uuid.UUID(fila.ID.Bytes)
			gravamen, err := verificarLimite(ctx, q, solicitud.UsuarioID, nivel, solicitud.Monto, ocurridoEn)
			if err != nil {
				return Transferencia{}, err
			}
			if err := verificarSaldoDeReserva(ctx, q, solicitud.CuentaOrigen, solicitud.Monto, gravamen); err != nil {
				return Transferencia{}, err
			}
			if err := retener(ctx, q, SolicitudRetencion{
				TransferenciaID: transferenciaID,
				UsuarioID:       solicitud.UsuarioID,
				Monto:           solicitud.Monto,
				Gravamen:        gravamen,
				Moneda:          solicitud.Moneda,
				OcurridoEn:      ocurridoEn,
			}); err != nil {
				return Transferencia{}, err
			}
			if _, err := encolarDespacho(ctx, q, transferenciaID, resolucion); err != nil {
				return Transferencia{}, err
			}
			reservada, err := q.AvanzarEstadoTransferencia(ctx, repositorio.AvanzarEstadoTransferenciaParams{
				ID:             fila.ID,
				EstadoEsperado: EstadoTransferenciaCreada,
				EstadoNuevo:    EstadoTransferenciaReservada,
			})
			if err != nil {
				return Transferencia{}, fmt.Errorf("nucleo: reservar la transferencia %s: %w", transferenciaID, err)
			}
			return transferenciaDeFila(reservada), nil
		})
}

// reintentoDeEnvioPorLlave resuelve el reenvío de una clave ya usada leyendo la transferencia guardada tras el fallo (criterio 7).
func (s *Servicio) reintentoDeEnvioPorLlave(ctx context.Context, solicitud SolicitudEnvioPorLlave, causa error) (Transferencia, bool, error) {
	guardada, err := s.obtenerTransferenciaPorIdempotencia(ctx, solicitud.UsuarioID, solicitud.IdempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transferencia{}, false, causa
	}
	if err != nil {
		return Transferencia{}, false, err
	}
	if guardada.CuentaOrigen != solicitud.CuentaOrigen || guardada.LlaveDestino != solicitud.Llave ||
		guardada.Monto != solicitud.Monto || guardada.Moneda != solicitud.Moneda {
		return Transferencia{}, false, ErrIdempotenciaEnConflicto
	}
	return guardada, false, nil
}
