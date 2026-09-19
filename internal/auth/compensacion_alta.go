package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// MotivoCompensacionEntregaFallida es el motivo del cierre de un alta que quedó sin desafío.
	MotivoCompensacionEntregaFallida = "compensacion_entrega_fallida"
	// tiempoDeCompensacion acota el cierre, que corre aunque el request original ya esté cancelado.
	tiempoDeCompensacion = 5 * time.Second
)

// CompensadorDeAlta cierra la credencial pendiente cuyo desafío no se pudo crear tras el commit del alta.
type CompensadorDeAlta interface {
	CompensarAlta(ctx context.Context, credencialID uuid.UUID) error
}

// compensadorDeAltaPostgres cierra la credencial con el gestor de estados en una transacción propia.
type compensadorDeAltaPostgres struct {
	pool   *pgxpool.Pool
	gestor *GestorDeEstados
}

// CompensarAlta transiciona pendiente_verificacion a cerrado para liberar el correo y el celular.
func (c compensadorDeAltaPostgres) CompensarAlta(ctx context.Context, credencialID uuid.UUID) error {
	_, err := enTransaccion(ctx, poolDeAuth{pool: c.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, c.gestor.TransicionarCredencial(ctx, q, credencialID, EstadoPendienteVerificacion, EstadoCerrado, MotivoCompensacionEntregaFallida)
		})
	return err
}

// OpcionDeRegistrador ajusta el registrador durante su construcción.
type OpcionDeRegistrador func(*RegistradorDePersonas)

// ConCompensador reemplaza el compensador por defecto, que cierra la credencial sobre el pool del registrador.
func ConCompensador(compensador CompensadorDeAlta) OpcionDeRegistrador {
	return func(s *RegistradorDePersonas) { s.compensador = compensador }
}

// compensarAlta cierra la credencial huérfana y consigna el fallo sin enmascarar el error del desafío.
func (s *RegistradorDePersonas) compensarAlta(ctx context.Context, credencialID uuid.UUID) {
	ctx, cancelar := context.WithTimeout(context.WithoutCancel(ctx), tiempoDeCompensacion)
	defer cancelar()
	if err := s.compensador.CompensarAlta(ctx, credencialID); err != nil {
		slog.ErrorContext(ctx, "no se pudo compensar el alta sin desafío, la cuenta queda pendiente", "credencial_id", credencialID, "err_compensacion", err)
	}
}
