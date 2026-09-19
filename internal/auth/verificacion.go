package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// intentosMaximos es el tope de intentos que consume un desafío antes de invalidarse.
const intentosMaximos = 3

const (
	// resultadoVerificacionOk marca el código correcto y vigente.
	resultadoVerificacionOk = "ok"
	// resultadoVerificacionCodigoInvalido marca el código que no coincide.
	resultadoVerificacionCodigoInvalido = "codigo_invalido"
	// resultadoVerificacionExpirado marca el desafío vencido.
	resultadoVerificacionExpirado = "expirado"
	// resultadoVerificacionDemasiadosIntentos marca el desafío sin intentos disponibles.
	resultadoVerificacionDemasiadosIntentos = "demasiados_intentos"
	// resultadoVerificacionInvalido marca el desafío inexistente, reusado o de otro propósito.
	resultadoVerificacionInvalido = "invalido"
)

// ResultadoDesafio es el desafío verificado que el llamador usa para aplicar el efecto de su propósito.
type ResultadoDesafio struct {
	ParteID        uuid.UUID
	CredencialID   uuid.UUID
	Proposito      Proposito
	Contexto       string
	CelularDestino string
}

// VerificarDesafio verifica el código en una transacción propia y confirma el intento aunque falle.
func (g *GestorDesafios) VerificarDesafio(ctx context.Context, id uuid.UUID, codigo string, proposito Proposito) (ResultadoDesafio, error) {
	var resultado ResultadoDesafio
	var dominio error
	if _, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		resultado, dominio = g.VerificarDesafioEn(ctx, q, id, codigo, proposito)
		return struct{}{}, nil
	}); err != nil {
		return ResultadoDesafio{}, err
	}
	return resultado, dominio
}

// VerificarDesafioEn consume un intento, valida vigencia, propósito y código, y marca el uso sobre el Querier recibido.
func (g *GestorDesafios) VerificarDesafioEn(ctx context.Context, q *repositorio.Queries, id uuid.UUID, codigo string, proposito Proposito) (ResultadoDesafio, error) {
	fila, err := q.IncrementarIntentoDesafio(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return ResultadoDesafio{}, g.resolverIntentoNoDisponible(ctx, q, id)
	}
	if err != nil {
		return ResultadoDesafio{}, fmt.Errorf("auth: incrementar el intento del desafío: %w", err)
	}
	if fila.Expirado {
		if _, err := q.MarcarDesafioExpirado(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
			return ResultadoDesafio{}, fmt.Errorf("auth: marcar el desafío expirado: %w", err)
		}
		if err := g.registrarVerificacion(ctx, q, fila.ParteID, fila.CredencialID, fila.Proposito, resultadoVerificacionExpirado); err != nil {
			return ResultadoDesafio{}, err
		}
		return ResultadoDesafio{}, ErrCodigoExpirado
	}
	if Proposito(fila.Proposito) != proposito {
		if err := g.registrarVerificacion(ctx, q, fila.ParteID, fila.CredencialID, fila.Proposito, resultadoVerificacionInvalido); err != nil {
			return ResultadoDesafio{}, err
		}
		return ResultadoDesafio{}, ErrDesafioInvalido
	}
	if subtle.ConstantTimeCompare(hmacDeCodigo(g.pepper, codigo), fila.CodigoHmac) != 1 {
		if fila.Intentos >= intentosMaximos {
			if _, err := q.InvalidarDesafio(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
				return ResultadoDesafio{}, fmt.Errorf("auth: invalidar el desafío agotado: %w", err)
			}
		}
		if err := g.registrarVerificacion(ctx, q, fila.ParteID, fila.CredencialID, fila.Proposito, resultadoVerificacionCodigoInvalido); err != nil {
			return ResultadoDesafio{}, err
		}
		return ResultadoDesafio{}, ErrCodigoInvalido
	}
	filas, err := q.MarcarDesafioVerificado(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if err != nil {
		return ResultadoDesafio{}, fmt.Errorf("auth: marcar el desafío verificado: %w", err)
	}
	if filas == 0 {
		if err := g.registrarVerificacion(ctx, q, fila.ParteID, fila.CredencialID, fila.Proposito, resultadoVerificacionInvalido); err != nil {
			return ResultadoDesafio{}, err
		}
		return ResultadoDesafio{}, ErrDesafioInvalido
	}
	if err := g.registrarVerificacion(ctx, q, fila.ParteID, fila.CredencialID, fila.Proposito, resultadoVerificacionOk); err != nil {
		return ResultadoDesafio{}, err
	}
	return resultadoDeDesafio(fila), nil
}

// resolverIntentoNoDisponible clasifica el desafío sin fila de incremento como agotado o inválido.
func (g *GestorDesafios) resolverIntentoNoDisponible(ctx context.Context, q *repositorio.Queries, id uuid.UUID) error {
	fila, err := q.DesafioPorID(ctx, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDesafioInvalido
	}
	if err != nil {
		return fmt.Errorf("auth: leer el desafío sin intento disponible: %w", err)
	}
	if fila.Intentos >= intentosMaximos && (fila.Estado == "pendiente" || fila.Estado == "invalidado") {
		if fila.Estado == "pendiente" {
			if _, err := q.InvalidarDesafio(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
				return fmt.Errorf("auth: invalidar el desafío agotado: %w", err)
			}
		}
		if err := g.registrarVerificacion(ctx, q, fila.ParteID, fila.CredencialID, fila.Proposito, resultadoVerificacionDemasiadosIntentos); err != nil {
			return err
		}
		return ErrDemasiadosIntentos
	}
	if err := g.registrarVerificacion(ctx, q, fila.ParteID, fila.CredencialID, fila.Proposito, resultadoVerificacionInvalido); err != nil {
		return err
	}
	return ErrDesafioInvalido
}

// registrarVerificacion escribe el evento de la verificación con el propósito y sin el código.
func (g *GestorDesafios) registrarVerificacion(ctx context.Context, q *repositorio.Queries, parteID, credencialID pgtype.UUID, proposito, resultado string) error {
	return registrarEvento(ctx, g.eventos, q, Evento{
		Tipo:         TipoEventoVerificacion,
		Actor:        uuidDePgtype(parteID),
		CredencialID: uuidDePgtype(credencialID),
		Resultado:    resultado,
		Fecha:        time.Now().UTC(),
		Detalle:      map[string]string{"proposito": proposito},
	})
}

// resultadoDeDesafio mapea la fila confirmada al resultado que consume el llamador.
func resultadoDeDesafio(fila repositorio.IncrementarIntentoDesafioRow) ResultadoDesafio {
	return ResultadoDesafio{
		ParteID:        uuidDePgtype(fila.ParteID),
		CredencialID:   uuidDePgtype(fila.CredencialID),
		Proposito:      Proposito(fila.Proposito),
		Contexto:       fila.ContextoSolicitado.String,
		CelularDestino: fila.CelularDestino,
	}
}
