package copilot

import (
	"context"
	"errors"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PropuestasPostgres sólo tiene acceso al rol del asistente y no al dinero.
type PropuestasPostgres struct{ pool *pgxpool.Pool }

// NuevasPropuestasPostgres conecta la persistencia de propuestas al pool compartido.
func NuevasPropuestasPostgres(pool *pgxpool.Pool) *PropuestasPostgres {
	return &PropuestasPostgres{pool}
}

// GuardarPropuesta cancela o vence la anterior y crea la nueva de forma atómica.
func (g *PropuestasPostgres) GuardarPropuesta(ctx context.Context, p PropuestaTransferencia) (AccionHerramienta, error) {
	parte, err := parteDelAsistente(ctx)
	if err != nil {
		return AccionHerramienta{}, err
	}
	conv, ok := ConversacionDelContexto(ctx)
	if !ok {
		return AccionHerramienta{}, ErrHerramientaValidacion
	}
	a, err := EnTransaccionAsistente(ctx, g.pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		if _, err := q.BloquearConversacionParaAccion(ctx, repositorio.BloquearConversacionParaAccionParams{ConversacionID: uuidCiclo(conv), UsuarioID: uuidCiclo(parte)}); err != nil {
			return repositorio.AccionPendiente{}, err
		}
		if err := q.CancelarPropuestaAnterior(ctx, repositorio.CancelarPropuestaAnteriorParams{ConversacionID: uuidCiclo(conv), UsuarioID: uuidCiclo(parte)}); err != nil {
			return repositorio.AccionPendiente{}, err
		}
		return q.CrearAccion(ctx, repositorio.CrearAccionParams{UsuarioID: uuidCiclo(parte), ConversacionID: uuidCiclo(conv), Tipo: p.Tipo, Destino: p.Destino, TipoLlave: textoOpcionalPreparacion(p.TipoLlave), NombreMostrado: p.Nombre, CelularEnmascarado: textoOpcionalPreparacion(p.Celular), EntidadDestino: textoOpcionalPreparacion(p.Entidad), Monto: p.Monto.Int64(), RequiereCodigo: p.RequiereCodigo, UsuarioFinancieroID: pgtype.Text{String: p.UsuarioFinanciero, Valid: true}})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AccionHerramienta{}, ErrHerramientaNoEncontrado
	}
	if err != nil {
		return AccionHerramienta{}, err
	}
	return AccionHerramienta{AccionID: uuid.UUID(a.ID.Bytes), Destinatario: DestinatarioHerramienta{Nombre: a.NombreMostrado, Celular: a.CelularEnmascarado.String}, Monto: p.Monto, ExpiraEn: a.ExpiraEn.Time, RequiereCodigo: a.RequiereCodigo, Entidad: a.EntidadDestino.String}, nil
}

func textoOpcionalPreparacion(texto string) pgtype.Text {
	return pgtype.Text{String: texto, Valid: texto != ""}
}
