package copilot

import (
	"context"
	"errors"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrConsentimientoAsistente permite distinguir la falta de autorización de una caída de auth.
var ErrConsentimientoAsistente = errors.New("asistente: consentimiento vigente requerido")

// LectorConsentimientoAsistente es implementado por auth sin compartir tablas.
type LectorConsentimientoAsistente interface {
	ConsentimientoAsistenteVigente(context.Context, uuid.UUID) (bool, error)
}

// CanceladorPorConsentimiento cancela sólo la acción pendiente de su titular.
type CanceladorPorConsentimiento interface {
	CancelarPorConsentimiento(context.Context, uuid.UUID, uuid.UUID) error
}

// ControlConsentimiento consulta auth en cada mensaje, confirmación y llamada al proveedor.
type ControlConsentimiento struct {
	lector     LectorConsentimientoAsistente
	cancelador CanceladorPorConsentimiento
}

// NuevoControlConsentimiento une la lectura de auth con la cancelación del asistente.
func NuevoControlConsentimiento(lector LectorConsentimientoAsistente, cancelador CanceladorPorConsentimiento) *ControlConsentimiento {
	return &ControlConsentimiento{lector: lector, cancelador: cancelador}
}

func parteDelAsistente(ctx context.Context) (uuid.UUID, error) {
	id, ok := identidad.DeContexto(ctx)
	if !ok {
		return uuid.Nil, ErrConsentimientoAsistente
	}
	parte, err := uuid.Parse(id.ParteID)
	if err != nil || parte == uuid.Nil {
		return uuid.Nil, ErrConsentimientoAsistente
	}
	return parte, nil
}

// Exigir falla cerrado; los errores del repositorio nunca se confunden con revocación.
func (c *ControlConsentimiento) Exigir(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parte, err := parteDelAsistente(ctx)
	if err != nil {
		return err
	}
	if c == nil || c.lector == nil {
		return ErrNoDisponibleIA
	}
	vigente, err := c.lector.ConsentimientoAsistenteVigente(ctx, parte)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrNoDisponibleIA
	}
	if !vigente {
		return ErrConsentimientoAsistente
	}
	return nil
}

// ExigirConfirmacion cancela la acción propia antes de devolver ConsentRequired.
// La ejecución financiera y su coordinación transaccional pertenecen a SCRUM-137.
func (c *ControlConsentimiento) ExigirConfirmacion(ctx context.Context, accion uuid.UUID) error {
	err := c.Exigir(ctx)
	if !errors.Is(err, ErrConsentimientoAsistente) {
		return err
	}
	parte, errParte := parteDelAsistente(ctx)
	if errParte != nil {
		return errParte
	}
	if c == nil || c.cancelador == nil {
		return ErrNoDisponibleIA
	}
	if err := c.cancelador.CancelarPorConsentimiento(ctx, parte, accion); err != nil {
		return ErrNoDisponibleIA
	}
	return ErrConsentimientoAsistente
}

type proveedorConsentido struct {
	control   *ControlConsentimiento
	siguiente ProveedorIA
}

// ProtegerProveedor verifica autorización antes de CADA llamada, incluidas las vueltas del orquestador.
func (c *ControlConsentimiento) ProtegerProveedor(proveedor ProveedorIA) ProveedorIA {
	return &proveedorConsentido{control: c, siguiente: proveedor}
}

func (p *proveedorConsentido) Generar(ctx context.Context, s SolicitudIA) (RespuestaIA, error) {
	if err := p.control.Exigir(ctx); err != nil {
		return RespuestaIA{}, err
	}
	if p.siguiente == nil {
		return RespuestaIA{}, ErrNoDisponibleIA
	}
	return p.siguiente.Generar(ctx, s)
}

// CanceladorConsentimientoPostgres reutiliza el CAS del módulo del asistente.
type CanceladorConsentimientoPostgres struct{ pool *pgxpool.Pool }

// NuevoCanceladorConsentimiento construye el adaptador de cancelación en PostgreSQL.
func NuevoCanceladorConsentimiento(pool *pgxpool.Pool) *CanceladorConsentimientoPostgres {
	return &CanceladorConsentimientoPostgres{pool: pool}
}

// CancelarPorConsentimiento sólo cambia acciones pendientes pertenecientes a la parte.
func (c *CanceladorConsentimientoPostgres) CancelarPorConsentimiento(ctx context.Context, parte, accion uuid.UUID) error {
	_, err := EnTransaccionAsistente(ctx, c.pool, func(q *repositorio.Queries) (struct{}, error) {
		_, err := q.CancelarAccion(ctx, repositorio.CancelarAccionParams{ID: pgtype.UUID{Bytes: accion, Valid: true}, UsuarioID: pgtype.UUID{Bytes: parte, Valid: true}})
		if errors.Is(err, pgx.ErrNoRows) {
			return struct{}{}, nil
		} // ajena, ausente o ya terminal: no revela existencia
		return struct{}{}, err
	})
	return err
}
