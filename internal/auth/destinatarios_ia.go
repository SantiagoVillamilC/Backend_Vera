package auth

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/text/unicode/norm"
)

// DestinatarioIA conserva el identificador únicamente dentro del backend.
type DestinatarioIA struct{ ID, Nombre, Celular string }

func nombreBusquedaIA(nombre string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(strings.Join(strings.Fields(nombre), " "))) {
		if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// BuscarDestinatariosPrevios sólo busca personas dentro de los IDs autorizados del núcleo.
func (r *ResolutorDestino) BuscarDestinatariosPrevios(ctx context.Context, refs []string, nombre string) ([]DestinatarioIA, error) {
	if len(refs) > 100 {
		return nil, errors.New("auth: página de destinatarios excesiva")
	}
	ids := make([]pgtype.UUID, 0, len(refs))
	for _, ref := range refs {
		if id, err := uuid.Parse(ref); err == nil && id != uuid.Nil {
			ids = append(ids, pgtype.UUID{Bytes: id, Valid: true})
		}
	}
	return enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) ([]DestinatarioIA, error) {
		filas, err := q.BuscarDestinatariosPrevios(ctx, repositorio.BuscarDestinatariosPreviosParams{Ids: ids, Nombre: nombreBusquedaIA(nombre), EnfriamientoSegundos: r.enfriamiento.Seconds()})
		if err != nil {
			return nil, err
		}
		salida := make([]DestinatarioIA, 0, len(filas))
		for _, f := range filas {
			salida = append(salida, DestinatarioIA{ID: uuid.UUID(f.ParteID.Bytes).String(), Nombre: f.Nombre, Celular: f.Celular})
		}
		return salida, nil
	})
}

// DestinatarioPorCelularIA aplica la misma elegibilidad del envío en una transacción.
func (r *ResolutorDestino) DestinatarioPorCelularIA(ctx context.Context, celular string) (DestinatarioIA, bool, error) {
	type resultado struct {
		d      DestinatarioIA
		existe bool
	}
	res, err := enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (resultado, error) {
		id, err := q.ResolverDestinoPorCelular(ctx, repositorio.ResolverDestinoPorCelularParams{Celular: celular, EnfriamientoSegundos: r.enfriamiento.Seconds()})
		if errors.Is(err, pgx.ErrNoRows) {
			return resultado{}, nil
		}
		if err != nil {
			return resultado{}, err
		}
		filas, err := q.BuscarDestinatariosPrevios(ctx, repositorio.BuscarDestinatariosPreviosParams{Ids: []pgtype.UUID{id}, Nombre: "", EnfriamientoSegundos: r.enfriamiento.Seconds()})
		if err != nil {
			return resultado{}, err
		}
		if len(filas) == 0 {
			return resultado{}, nil
		}
		f := filas[0]
		return resultado{DestinatarioIA{uuid.UUID(f.ParteID.Bytes).String(), f.Nombre, f.Celular}, true}, nil
	})
	return res.d, res.existe, err
}
