package auth

import (
	"context"
	"encoding/json"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/go-chi/httprate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// clavesDeDetallePermitidas es la lista blanca del detalle: todo lo demás se descarta antes de persistir.
var clavesDeDetallePermitidas = map[string]struct{}{
	"proposito":           {},
	"motivo":              {},
	"dispositivo":         {},
	"contexto_solicitado": {},
	"celular_enmascarado": {},
}

// registradorConTx es un registrador que sabe escribir con el Querier de una transacción abierta.
type registradorConTx interface {
	EnTransaccion(q *repositorio.Queries) RegistradorEventos
}

// RegistradorEventosPostgres persiste el trail de auth, dentro de una transacción ajena o en una propia.
type RegistradorEventosPostgres struct {
	abridor abridorDeTransaccionesAuth
	enTx    *repositorio.Queries
}

// NuevoRegistradorEventos construye el registrador que abre su propia transacción por evento.
func NuevoRegistradorEventos(pool *pgxpool.Pool) *RegistradorEventosPostgres {
	return &RegistradorEventosPostgres{abridor: poolDeAuth{pool: pool}}
}

// NuevoRegistradorEventosEnTx construye el registrador ligado al Querier de una transacción abierta.
func NuevoRegistradorEventosEnTx(q *repositorio.Queries) *RegistradorEventosPostgres {
	return &RegistradorEventosPostgres{enTx: q}
}

// EnTransaccion devuelve el registrador ligado al Querier de una transacción abierta.
func (r *RegistradorEventosPostgres) EnTransaccion(q *repositorio.Queries) RegistradorEventos {
	return &RegistradorEventosPostgres{abridor: r.abridor, enTx: q}
}

var _ RegistradorEventos = (*RegistradorEventosPostgres)(nil)

// Registrar persiste el evento con el detalle filtrado y el origen canonicalizado.
func (r *RegistradorEventosPostgres) Registrar(ctx context.Context, e Evento) error {
	detalle, err := detallePermitido(e.Detalle)
	if err != nil {
		return err
	}
	ocurrido := e.Fecha
	if ocurrido.IsZero() {
		ocurrido = time.Now().UTC()
	}
	params := repositorio.RegistrarEventoAutenticacionParams{
		Tipo:         e.Tipo,
		ActorParteID: uuidOPgtype(e.Actor),
		CredencialID: uuidOPgtype(e.CredencialID),
		Resultado:    e.Resultado,
		OcurridoEn:   pgtype.Timestamptz{Time: ocurrido, Valid: true},
		Origen:       httprate.CanonicalizeIP(e.Origen),
		Contexto:     e.Contexto,
		SesionID:     uuidOPgtype(e.SesionID),
		Detalle:      detalle,
	}
	if r.enTx != nil {
		return r.enTx.RegistrarEventoAutenticacion(ctx, params)
	}
	_, err = enTransaccion(ctx, r.abridor, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		return struct{}{}, q.RegistrarEventoAutenticacion(ctx, params)
	})
	return err
}

// detallePermitido copia solo las claves de la lista blanca y descarta el resto.
func detallePermitido(detalle map[string]string) ([]byte, error) {
	permitido := make(map[string]string, len(detalle))
	for clave, valor := range detalle {
		if _, ok := clavesDeDetallePermitidas[clave]; ok {
			permitido[clave] = valor
		}
	}
	return json.Marshal(permitido)
}

// uuidOPgtype mapea el uuid cero a NULL y el resto al tipo de pgx.
func uuidOPgtype(id uuid.UUID) pgtype.UUID {
	if id == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}
