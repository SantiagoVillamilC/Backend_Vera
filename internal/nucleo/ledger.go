package nucleo

import (
	"context"
	"errors"

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
