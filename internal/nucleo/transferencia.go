package nucleo

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// ErrDestinoObligatorio reporta una solicitud de envío sin la llave de destino.
var ErrDestinoObligatorio = errors.New("nucleo: el destino es obligatorio")

// ErrDestinoNoExiste reporta una llave de destino que no corresponde a ningún usuario.
var ErrDestinoNoExiste = errors.New("nucleo: el destino no corresponde a ningún usuario de la billetera")

// ErrDestinoPropio reporta un envío dirigido al mismo usuario que lo emite.
var ErrDestinoPropio = errors.New("nucleo: no se puede enviar dinero a sí mismo")

// ErrSaldoInsuficiente reporta que el disponible del emisor no cubre el monto más el gravamen.
var ErrSaldoInsuficiente = errors.New("nucleo: el saldo del emisor es insuficiente")

// ErrResolutorDestinoNoDisponible reporta que falta el resolutor de la llave de destino.
var ErrResolutorDestinoNoDisponible = errors.New("nucleo: no hay resolutor de destino configurado")

// ErrMonedaInvalida reporta una moneda distinta de COP, la única que admite la billetera.
var ErrMonedaInvalida = errors.New("nucleo: la moneda debe ser COP")

// ResolutorDestino traduce la llave de destino a un usuario de la billetera; existe en false cuando no la encuentra.
type ResolutorDestino interface {
	UsuarioIDDeDestino(ctx context.Context, llave string) (usuarioID string, existe bool, err error)
}

// SolicitudTransferencia es el envío interno que Transferir ejecuta en una sola transacción.
type SolicitudTransferencia struct {
	UsuarioID      string
	CuentaOrigen   string
	LlaveDestino   string
	Monto          Centavos
	Moneda         string
	IdempotencyKey string
}

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

// Transferir ejecuta el envío interno: resuelve el destino, retiene el monto y lo acredita al receptor en una sola transacción.
func (s *Servicio) Transferir(ctx context.Context, solicitud SolicitudTransferencia) (Transferencia, bool, error) {
	if solicitud.Moneda == "" {
		solicitud.Moneda = MonedaCOP
	}
	transferencia, err := s.ejecutarTransferencia(ctx, solicitud)
	if err == nil {
		return transferencia, true, nil
	}
	return s.reintentoDeTransferencia(ctx, solicitud, err)
}

// reintentoDeTransferencia resuelve el reenvío de una llave ya usada leyendo la transferencia guardada tras el fallo.
func (s *Servicio) reintentoDeTransferencia(ctx context.Context, solicitud SolicitudTransferencia, causa error) (Transferencia, bool, error) {
	guardada, err := s.obtenerTransferenciaPorIdempotencia(ctx, solicitud.UsuarioID, solicitud.IdempotencyKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transferencia{}, false, causa
	}
	if err != nil {
		return Transferencia{}, false, err
	}
	if guardada.CuentaOrigen != solicitud.CuentaOrigen || guardada.LlaveDestino != solicitud.LlaveDestino ||
		guardada.Monto != solicitud.Monto || guardada.Moneda != solicitud.Moneda {
		return Transferencia{}, false, ErrIdempotenciaEnConflicto
	}
	return guardada, false, nil
}

// ejecutarTransferencia resuelve el destino y el nivel de KYC antes de abrir la transacción de la reserva.
func (s *Servicio) ejecutarTransferencia(ctx context.Context, solicitud SolicitudTransferencia) (Transferencia, error) {
	if solicitud.Monto <= 0 {
		return Transferencia{}, ErrMontoInvalido
	}
	if solicitud.Moneda != "" && solicitud.Moneda != MonedaCOP {
		return Transferencia{}, ErrMonedaInvalida
	}
	if strings.TrimSpace(solicitud.LlaveDestino) == "" {
		return Transferencia{}, ErrDestinoObligatorio
	}
	if s.destinos == nil {
		return Transferencia{}, ErrResolutorDestinoNoDisponible
	}
	destinoID, existe, err := s.destinos.UsuarioIDDeDestino(ctx, solicitud.LlaveDestino)
	if err != nil {
		return Transferencia{}, fmt.Errorf("nucleo: no se pudo resolver el destino: %w", err)
	}
	if !existe {
		return Transferencia{}, ErrDestinoNoExiste
	}
	if destinoID == solicitud.UsuarioID {
		return Transferencia{}, ErrDestinoPropio
	}
	if s.kyc == nil {
		return Transferencia{}, ErrKYCNoDisponible
	}
	nivel, err := s.kyc.NivelKYC(ctx, solicitud.UsuarioID)
	if err != nil {
		return Transferencia{}, fmt.Errorf("nucleo: leer el nivel de KYC de %q: %w", solicitud.UsuarioID, err)
	}
	return s.reservarYTransferir(ctx, solicitud, destinoID, nivel, time.Now().UTC())
}

// reservarYTransferir abre la única transacción: candado, fila creada, límites, saldo, retención, transferencia interna y confirmación.
func (s *Servicio) reservarYTransferir(ctx context.Context, solicitud SolicitudTransferencia, destinoID, nivel string, ocurridoEn time.Time) (Transferencia, error) {
	cuentaDestino := NombreCuentaDisponible(destinoID)
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (Transferencia, error) {
			if err := bloquearOrdenadas(ctx, q, solicitud.CuentaOrigen, cuentaDestino); err != nil {
				return Transferencia{}, err
			}
			fila, err := q.InsertarTransferencia(ctx, repositorio.InsertarTransferenciaParams{
				ID:             pgtype.UUID{Bytes: uuid.New(), Valid: true},
				UsuarioID:      solicitud.UsuarioID,
				CuentaOrigen:   solicitud.CuentaOrigen,
				LlaveDestino:   solicitud.LlaveDestino,
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
			if err := transferirInterna(ctx, q, SolicitudTransferenciaInterna{
				TransferenciaID: transferenciaID,
				EmisorID:        solicitud.UsuarioID,
				ReceptorID:      destinoID,
				Monto:           solicitud.Monto,
				Moneda:          solicitud.Moneda,
				OcurridoEn:      ocurridoEn,
			}); err != nil {
				return Transferencia{}, err
			}
			confirmada, err := q.AvanzarEstadoTransferencia(ctx, repositorio.AvanzarEstadoTransferenciaParams{
				ID:             fila.ID,
				EstadoEsperado: EstadoTransferenciaCreada,
				EstadoNuevo:    EstadoTransferenciaConfirmada,
			})
			if err != nil {
				return Transferencia{}, fmt.Errorf("nucleo: confirmar la transferencia %s: %w", transferenciaID, err)
			}
			return transferenciaDeFila(confirmada), nil
		})
}

// verificarSaldoDeReserva exige que el disponible del emisor cubra el monto más el gravamen (HU-NF-06, criterio 13.4).
func verificarSaldoDeReserva(ctx context.Context, q *repositorio.Queries, cuentaOrigen string, monto, gravamen Centavos) error {
	exigido, err := SaldoExigido(monto, gravamen)
	if err != nil {
		return err
	}
	saldo, err := q.SaldoDeCuenta(ctx, cuentaOrigen)
	if err != nil {
		return fmt.Errorf("nucleo: leer el saldo de %q: %w", cuentaOrigen, err)
	}
	disponible, err := Centavos(0).Restar(Centavos(saldo))
	if err != nil {
		return err
	}
	if disponible < exigido {
		return ErrSaldoInsuficiente
	}
	return nil
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
