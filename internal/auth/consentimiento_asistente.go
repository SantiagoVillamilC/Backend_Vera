package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// EstadoConsentimientoAsistente contiene el texto actual incluso sin eventos del usuario.
type EstadoConsentimientoAsistente struct {
	VersionTexto string
	Texto        string
	Autorizado   bool
}

// EstadoAsistente obtiene texto y autorización en el mismo snapshot, sin caché.
func (s *ServicioDeConsentimientos) EstadoAsistente(ctx context.Context, parteID uuid.UUID) (EstadoConsentimientoAsistente, error) {
	if parteID == uuid.Nil {
		return EstadoConsentimientoAsistente{}, ErrConsentimientoRequerido
	}
	fila, err := enTransaccion(ctx, s.abridor, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.EstadoConsentimientoAsistenteRow, error) {
		return q.EstadoConsentimientoAsistente(ctx, uuidOPgtype(parteID))
	})
	if err != nil {
		return EstadoConsentimientoAsistente{}, fmt.Errorf("auth: consultar consentimiento del asistente: %w", err)
	}
	return EstadoConsentimientoAsistente{VersionTexto: fila.Version, Texto: fila.Texto, Autorizado: fila.Autorizado}, nil
}

// ConsentimientoAsistenteVigente expone la autorización sin acceso a tablas desde copilot.
func (s *ServicioDeConsentimientos) ConsentimientoAsistenteVigente(ctx context.Context, parteID uuid.UUID) (bool, error) {
	estado, err := s.EstadoAsistente(ctx, parteID)
	return estado.Autorizado, err
}

func validarVersionAsistente(ctx context.Context, q *repositorio.Queries, version string) error {
	texto, err := q.TextoVigenteAsistente(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVersionDeConsentimientoInexistente
	}
	if err != nil {
		return fmt.Errorf("auth: leer versión vigente del asistente: %w", err)
	}
	if texto.Version != version {
		return ErrVersionDeConsentimientoInexistente
	}
	return nil
}
