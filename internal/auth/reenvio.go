package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// enfriamientoReenvio es la espera mínima entre dos envíos del mismo desafío.
	enfriamientoReenvio = 60 * time.Second
	// maxReenviosPorDesafio es el tope de reenvíos antes de invalidar el desafío.
	maxReenviosPorDesafio = 3
)

// ReenviarDesafio regenera el código de un desafío pendiente respetando enfriamiento y topes.
func (g *GestorDesafios) ReenviarDesafio(ctx context.Context, id uuid.UUID, origen string) (Desafio, error) {
	if g.racionador == nil {
		return Desafio{}, ConFailClosed(errors.New("auth: el racionamiento de OTP no está configurado"))
	}
	if err := g.racionador.ExigirOrigen(ctx, origen); err != nil {
		return Desafio{}, err
	}
	estado, err := g.leerDesafioAReenviar(ctx, id)
	if err != nil {
		return Desafio{}, err
	}
	if err := g.racionador.ExigirCuenta(ctx, estado.correo); err != nil {
		return Desafio{}, err
	}
	ahora := time.Now().UTC()
	if err := decidirReenvio(estado.fila, ahora); err != nil {
		if errors.Is(err, ErrDemasiadosReenvios) {
			if errInvalidar := g.invalidarDesafio(ctx, id); errInvalidar != nil {
				return Desafio{}, errInvalidar
			}
		}
		return Desafio{}, err
	}
	codigo, err := codigoDelDesafio(g.canal)
	if err != nil {
		return Desafio{}, err
	}
	fila, err := g.persistirReenvio(ctx, id, origen, estado, codigo, ahora)
	if err != nil {
		return Desafio{}, err
	}
	desafio := Desafio{ID: id, ExpiraEnSegundos: int(vigenciaDesafio.Seconds()), Proposito: Proposito(fila.Proposito)}
	if esRelleno(fila) {
		return desafio, nil
	}
	if _, err := g.canal.Enviar(ctx, fila.CelularDestino, codigo); err != nil {
		return Desafio{}, g.entregaFallida(ctx, fila.ID, estado.solicitud(origen), err)
	}
	return desafio, nil
}

// estadoDesafio es lo que el reenvío necesita del desafío pendiente antes de tocarlo.
type estadoDesafio struct {
	fila   repositorio.DesafioPendientePorIDRow
	correo string
}

// solicitud reconstruye la solicitud de auditoría desde la fila del desafío.
func (e estadoDesafio) solicitud(origen string) Solicitud {
	return Solicitud{
		ParteID:      uuidDePgtype(e.fila.ParteID),
		CredencialID: uuidDePgtype(e.fila.CredencialID),
		Proposito:    Proposito(e.fila.Proposito),
		Celular:      e.fila.CelularDestino,
		Contexto:     e.fila.ContextoSolicitado.String,
		Origen:       origen,
	}
}

// esRelleno exige el centinela sin parte ni credencial, sin mirar el propósito porque P-07 rellena también registro.
func esRelleno(fila repositorio.AuthDesafio) bool {
	return fila.CelularDestino == celularDeRelleno && !fila.ParteID.Valid && !fila.CredencialID.Valid
}

// leerDesafioAReenviar lee el desafío pendiente y el correo de su credencial para el tope de cuenta.
func (g *GestorDesafios) leerDesafioAReenviar(ctx context.Context, id uuid.UUID) (estadoDesafio, error) {
	var estado estadoDesafio
	encontrado := false
	_, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		fila, err := q.DesafioPendientePorID(ctx, pgtype.UUID{Bytes: id, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: leer el desafío a reenviar: %w", err)
		}
		encontrado, estado.fila = true, fila
		if fila.CredencialID.Valid {
			credencial, err := q.CredencialPorID(ctx, fila.CredencialID)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return struct{}{}, fmt.Errorf("auth: leer la credencial del reenvío: %w", err)
			}
			estado.correo = credencial.Correo
		}
		return struct{}{}, nil
	})
	if err != nil {
		return estadoDesafio{}, err
	}
	if !encontrado {
		return estadoDesafio{}, ErrDesafioInvalido
	}
	return estado, nil
}

// decidirReenvio rechaza el reenvío vencido, en enfriamiento o sobre el tope de reenvíos.
func decidirReenvio(fila repositorio.DesafioPendientePorIDRow, ahora time.Time) error {
	if fila.Expirado {
		return ErrDesafioInvalido
	}
	if hayEnfriamiento(fila.UltimoEnvioEn.Time, ahora) {
		return ErrEnfriamientoReenvio
	}
	if int(fila.Reenvios) >= maxReenviosPorDesafio {
		return ErrDemasiadosReenvios
	}
	return nil
}

// hayEnfriamiento informa si todavía no pasó la espera mínima desde el último envío.
func hayEnfriamiento(ultimoEnvio, ahora time.Time) bool {
	return ahora.Sub(ultimoEnvio) < enfriamientoReenvio
}

// invalidarDesafio marca el desafío como invalidado en una transacción propia.
func (g *GestorDesafios) invalidarDesafio(ctx context.Context, id uuid.UUID) error {
	_, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		if _, err := q.InvalidarDesafio(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
			return struct{}{}, fmt.Errorf("auth: invalidar el desafío sin reenvíos: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

// persistirReenvio reemplaza el HMAC, sube reenvíos y renueva la vigencia sin tocar los intentos.
func (g *GestorDesafios) persistirReenvio(ctx context.Context, id uuid.UUID, origen string, estado estadoDesafio, codigo string, ahora time.Time) (repositorio.AuthDesafio, error) {
	var fila repositorio.AuthDesafio
	_, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		actualizada, err := q.ActualizarReenvioDesafio(ctx, repositorio.ActualizarReenvioDesafioParams{
			ID:         pgtype.UUID{Bytes: id, Valid: true},
			CodigoHmac: hmacDeCodigo(g.pepper, codigo),
			Ahora:      pgtype.Timestamptz{Time: ahora, Valid: true},
			ExpiraEn:   pgtype.Timestamptz{Time: ahora.Add(vigenciaDesafio), Valid: true},
			Umbral:     pgtype.Timestamptz{Time: ahora.Add(-enfriamientoReenvio), Valid: true},
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return struct{}{}, ErrDesafioInvalido
		}
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: actualizar el reenvío: %w", err)
		}
		fila = actualizada
		return struct{}{}, registrarEvento(ctx, g.eventos, q, g.eventoDeDesafio(estado.solicitud(origen), ahora, resultadoDesafioReenviado))
	})
	return fila, err
}
