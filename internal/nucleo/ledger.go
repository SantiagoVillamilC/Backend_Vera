package nucleo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
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
