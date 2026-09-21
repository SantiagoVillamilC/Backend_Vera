package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// EstadoCredencial es el estado de negocio de una credencial de auth.
type EstadoCredencial string

// Estados de negocio posibles de una credencial de auth.
const (
	EstadoPendienteVerificacion EstadoCredencial = "pendiente_verificacion"
	EstadoActivo                EstadoCredencial = "activo"
	EstadoSuspendido            EstadoCredencial = "suspendido"
	EstadoCerrado               EstadoCredencial = "cerrado"
	EstadoSinLogin              EstadoCredencial = "sin_login"
)

// MotivoPendienteVencida es el motivo del cierre que hace el job de limpieza.
const MotivoPendienteVencida = "pendiente_vencida"

// transicionesPermitidas fija el grafo de la cuenta; sin_login no transita (lo cierra el check de 0002).
var transicionesPermitidas = map[EstadoCredencial]map[EstadoCredencial]bool{
	EstadoPendienteVerificacion: {EstadoActivo: true, EstadoCerrado: true},
	EstadoActivo:                {EstadoSuspendido: true},
}

// ErrConflictoDeEstado indica que el compare-and-set no encontró la credencial en el estado esperado.
var ErrConflictoDeEstado = errors.New("auth: la credencial no está en el estado esperado")

// ErrTransicionNoPermitida indica que el par de estados no está en la tabla de transiciones.
var ErrTransicionNoPermitida = errors.New("auth: la transición de la credencial no está permitida")

// PuedeTransicionar reporta si la tabla admite pasar de un estado a otro.
func PuedeTransicionar(anterior, nuevo EstadoCredencial) bool {
	return transicionesPermitidas[anterior][nuevo]
}

// GestorDeEstados aplica transiciones de credencial, escribe el historial y publica el evento.
type GestorDeEstados struct{ eventos RegistradorEventos }

// NuevoGestorDeEstados construye el gestor; sin registrador usa el nulo de la fase 0.
func NuevoGestorDeEstados(eventos RegistradorEventos) *GestorDeEstados {
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	return &GestorDeEstados{eventos: eventos}
}

// TransicionarCredencial aplica el compare-and-set, escribe el historial y publica el evento en la misma transacción.
func (g *GestorDeEstados) TransicionarCredencial(ctx context.Context, q *repositorio.Queries, credencialID uuid.UUID, esperado, nuevo EstadoCredencial, motivo string) error {
	if !PuedeTransicionar(esperado, nuevo) {
		return fmt.Errorf("%w: %s -> %s", ErrTransicionNoPermitida, esperado, nuevo)
	}

	filas, err := q.TransicionarCredencial(ctx, repositorio.TransicionarCredencialParams{
		Nuevo:        string(nuevo),
		CredencialID: pgtype.UUID{Bytes: [16]byte(credencialID), Valid: true},
		Esperado:     string(esperado),
	})
	if err != nil {
		return fmt.Errorf("auth: transicionar la credencial: %w", err)
	}
	if filas == 0 {
		return fmt.Errorf("%w: %s", ErrConflictoDeEstado, credencialID)
	}

	return g.dejarRastroDeTransicion(ctx, q, credencialID, esperado, nuevo, motivo)
}

// ActivarCredencial activa la pendiente con compare-and-set, sella celular_verificado_en y deja historial y evento en la misma transacción.
func (g *GestorDeEstados) ActivarCredencial(ctx context.Context, q *repositorio.Queries, credencialID uuid.UUID, motivo string) error {
	filas, err := q.ActivarCredencial(ctx, pgtype.UUID{Bytes: [16]byte(credencialID), Valid: true})
	if err != nil {
		return fmt.Errorf("auth: activar la credencial: %w", err)
	}
	if filas == 0 {
		return fmt.Errorf("%w: %s", ErrConflictoDeEstado, credencialID)
	}

	return g.dejarRastroDeTransicion(ctx, q, credencialID, EstadoPendienteVerificacion, EstadoActivo, motivo)
}

// dejarRastroDeTransicion escribe la fila del historial y publica el evento de la transición ya aplicada.
func (g *GestorDeEstados) dejarRastroDeTransicion(ctx context.Context, q *repositorio.Queries, credencialID uuid.UUID, anterior, nuevo EstadoCredencial, motivo string) error {
	if err := registrarTransicion(ctx, q, credencialID, pgtype.Text{String: string(anterior), Valid: true}, nuevo, motivo); err != nil {
		return err
	}

	return registrarEvento(ctx, g.eventos, q, Evento{
		Tipo:         tipoDeTransicion(nuevo),
		Actor:        uuid.Nil,
		CredencialID: credencialID,
		Resultado:    string(nuevo),
		Fecha:        time.Now().UTC(),
		Contexto:     motivo,
	})
}

// RegistrarTransicion escribe la fila inicial del historial; un anterior vacío queda en NULL.
func RegistrarTransicion(ctx context.Context, q *repositorio.Queries, credencialID uuid.UUID, anterior, nuevo EstadoCredencial, motivo string) error {
	texto := pgtype.Text{}
	if anterior != "" {
		texto = pgtype.Text{String: string(anterior), Valid: true}
	}
	return registrarTransicion(ctx, q, credencialID, texto, nuevo, motivo)
}

// tipoDeTransicion mapea el estado alcanzado al tipo del catálogo de eventos de auditoría.
func tipoDeTransicion(nuevo EstadoCredencial) string {
	if nuevo == EstadoActivo {
		return TipoEventoVerificacion
	}
	return TipoEventoRevocacion
}

// registrarEvento escribe el evento en la transacción del caso de uso cuando el registrador la soporta.
func registrarEvento(ctx context.Context, r RegistradorEventos, q *repositorio.Queries, e Evento) error {
	if conTx, ok := r.(registradorConTx); ok {
		return conTx.EnTransaccion(q).Registrar(ctx, e)
	}
	return r.Registrar(ctx, e)
}

// registrarTransicion inserta la fila append-only del historial con el motivo corto.
func registrarTransicion(ctx context.Context, q *repositorio.Queries, credencialID uuid.UUID, anterior pgtype.Text, nuevo EstadoCredencial, motivo string) error {
	err := q.RegistrarTransicionCredencial(ctx, repositorio.RegistrarTransicionCredencialParams{
		CredencialID:   pgtype.UUID{Bytes: [16]byte(credencialID), Valid: true},
		EstadoAnterior: anterior,
		EstadoNuevo:    string(nuevo),
		Motivo:         pgtype.Text{String: motivo, Valid: motivo != ""},
	})
	if err != nil {
		return fmt.Errorf("auth: registrar la transición de la credencial: %w", err)
	}
	return nil
}
