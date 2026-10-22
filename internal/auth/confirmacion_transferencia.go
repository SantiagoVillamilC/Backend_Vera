package auth

import (
	"context"
	"errors"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PropositoConfirmacionTransferencia separa los OTP financieros de login y recuperación.
const PropositoConfirmacionTransferencia Proposito = "confirmacion_transferencia"

type poolConfirmacion struct{ tx pgx.Tx }

func (p poolConfirmacion) Begin(ctx context.Context) (transaccionDeAuth, error) {
	return p.tx.Begin(ctx)
}

// PlanCodigoTransferencia contiene datos de sesión resueltos por auth, nunca del cliente.
type PlanCodigoTransferencia struct {
	gestor    *GestorDesafios
	solicitud Solicitud
}

// EntregaConfirmacion mantiene el código privado hasta entregarlo tras el commit exterior.
type EntregaConfirmacion struct {
	Desafio   Desafio
	gestor    *GestorDesafios
	solicitud Solicitud
	codigo    string
}

func (e *EntregaConfirmacion) String() string { return "entrega de código de confirmación" }

func referenciaConfirmacion(ctx context.Context, accion uuid.UUID) string {
	id, _ := identidad.DeContexto(ctx)
	return "accion:" + accion.String() + ":" + id.UsuarioID
}
func credencialConfirmacion(ctx context.Context, q *repositorio.Queries) (repositorio.Credencial, error) {
	id, ok := identidad.DeContexto(ctx)
	if !ok {
		return repositorio.Credencial{}, ErrSinIdentidad
	}
	familia, err := uuid.Parse(id.SesionID)
	if err != nil {
		return repositorio.Credencial{}, ErrSinIdentidad
	}
	c, err := q.CredencialDeSesion(ctx, pgtype.UUID{Bytes: familia, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrSinIdentidad
	}
	if err != nil {
		return c, err
	}
	if c.Estado != "activo" {
		return c, ErrCuentaSuspendida
	}
	// ParteID es el actor del token; el contexto financiero puede ser otra empresa.
	if uuidDePgtype(c.ParteID).String() != id.ParteID {
		return c, ErrSinIdentidad
	}
	if !c.CelularVerificadoEn.Valid {
		return c, ErrCelularSinVerificar
	}
	return c, nil
}

// PlanificarCodigoTransferencia comprueba actor, celular, opt-in y racionamiento sin tx ajeno abierto.
func (g *GestorDesafios) PlanificarCodigoTransferencia(ctx context.Context, accion uuid.UUID, origen string) (*PlanCodigoTransferencia, error) {
	if g == nil || g.racionador == nil {
		return nil, ErrAuthNoDisponible
	}
	c, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.Credencial, error) {
		return credencialConfirmacion(ctx, q)
	})
	if err != nil {
		return nil, err
	}
	sol := Solicitud{ParteID: uuidDePgtype(c.ParteID), CredencialID: uuidDePgtype(c.ID), Celular: c.Celular, Proposito: PropositoConfirmacionTransferencia, Contexto: referenciaConfirmacion(ctx, accion), CorreoNormalizado: c.Correo, Origen: origen}
	if err := g.exigirOptIn(ctx, sol); err != nil {
		return nil, err
	}
	if err := g.racionador.Exigir(ctx, c.Correo, origen); err != nil {
		return nil, err
	}
	return &PlanCodigoTransferencia{g, sol}, nil
}

// PrepararEn persiste el desafío o su reenvío en el mismo tx que lo vincula a la acción.
// No entrega ningún código hasta que el llamador confirme ese tx.
func (p *PlanCodigoTransferencia) PrepararEn(ctx context.Context, tx pgx.Tx, previo uuid.UUID) (*EntregaConfirmacion, error) {
	g := p.gestor
	e, err := enTransaccion(ctx, poolConfirmacion{tx}, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (*EntregaConfirmacion, error) {
		c, err := credencialConfirmacion(ctx, q)
		if err != nil {
			return nil, err
		}
		if uuidDePgtype(c.ID) != p.solicitud.CredencialID || c.Celular != p.solicitud.Celular {
			return nil, ErrDesafioInvalido
		}
		codigo, err := codigoDelDesafio(g.canal)
		if err != nil {
			return nil, err
		}
		ahora := time.Now().UTC()
		var d Desafio
		if previo == uuid.Nil {
			fila, err := g.persistirEn(ctx, q, p.solicitud, codigo, ahora)
			if err != nil {
				return nil, err
			}
			d = Desafio{ID: uuidDePgtype(fila.ID), ExpiraEnSegundos: 300, Proposito: PropositoConfirmacionTransferencia}
		} else {
			fila, err := q.DesafioPorID(ctx, pgtype.UUID{Bytes: previo, Valid: true})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrDesafioInvalido
			}
			if err != nil {
				return nil, err
			}
			if uuidDePgtype(fila.ParteID) != p.solicitud.ParteID || fila.ContextoSolicitado.String != p.solicitud.Contexto || fila.Proposito != string(PropositoConfirmacionTransferencia) || fila.CelularDestino != c.Celular {
				return nil, ErrDesafioInvalido
			}
			if fila.Intentos >= intentosMaximos {
				return nil, ErrDemasiadosIntentos
			}
			reentregable := fila.Estado == "invalidado" && fila.Intentos == 0
			if fila.Estado != "pendiente" && !reentregable {
				return nil, ErrDesafioInvalido
			}
			if err := decidirReenvio(repositorio.DesafioPendientePorIDRow{Expirado: fila.Expirado, UltimoEnvioEn: fila.UltimoEnvioEn, Reenvios: fila.Reenvios}, ahora); err != nil {
				return nil, err
			}
			var r repositorio.AuthDesafio
			if fila.Estado == "invalidado" {
				r, err = q.ReenviarEntregaConfirmacion(ctx, repositorio.ReenviarEntregaConfirmacionParams{ID: fila.ID, CodigoHmac: hmacDeCodigo(g.pepper, codigo), Ahora: pgtype.Timestamptz{Time: ahora, Valid: true}, ExpiraEn: pgtype.Timestamptz{Time: ahora.Add(vigenciaDesafio), Valid: true}, Umbral: pgtype.Timestamptz{Time: ahora.Add(-enfriamientoReenvio), Valid: true}})
			} else {
				r, err = q.ActualizarReenvioDesafio(ctx, repositorio.ActualizarReenvioDesafioParams{ID: fila.ID, CodigoHmac: hmacDeCodigo(g.pepper, codigo), Ahora: pgtype.Timestamptz{Time: ahora, Valid: true}, ExpiraEn: pgtype.Timestamptz{Time: ahora.Add(vigenciaDesafio), Valid: true}, Umbral: pgtype.Timestamptz{Time: ahora.Add(-enfriamientoReenvio), Valid: true}})
			}
			if err != nil {
				return nil, err
			}
			if err := registrarEvento(ctx, g.eventos, q, g.eventoDeDesafio(p.solicitud, ahora, resultadoDesafioReenviado)); err != nil {
				return nil, err
			}
			d = Desafio{ID: uuidDePgtype(r.ID), ExpiraEnSegundos: 300, Proposito: PropositoConfirmacionTransferencia}
		}
		return &EntregaConfirmacion{d, g, p.solicitud, codigo}, nil
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}

// Entregar sólo se llama después del commit que vinculó el desafío a la acción.
func (e *EntregaConfirmacion) Entregar(ctx context.Context) error {
	defer func() { e.codigo = "" }()
	if _, err := e.gestor.canal.Enviar(ctx, e.solicitud.Celular, e.codigo); err != nil {
		return e.gestor.entregaFallida(ctx, pgtype.UUID{Bytes: e.Desafio.ID, Valid: true}, e.solicitud, err)
	}
	return nil
}

// VerificarCodigoTransferenciaEn comprueba actor, acción y origen antes de consumir el intento.
// El llamador confirma los errores de código para conservar los intentos fallidos.
func (g *GestorDesafios) VerificarCodigoTransferenciaEn(ctx context.Context, tx pgx.Tx, accion, desafio uuid.UUID, codigo string) error {
	var dominio error
	_, err := enTransaccion(ctx, poolConfirmacion{tx}, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		c, err := credencialConfirmacion(ctx, q)
		if err != nil {
			return struct{}{}, err
		}
		d, err := q.DesafioPorID(ctx, pgtype.UUID{Bytes: desafio, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			dominio = ErrDesafioInvalido
			return struct{}{}, nil
		}
		if err != nil {
			return struct{}{}, err
		}
		if d.ParteID != c.ParteID || d.CredencialID != c.ID || d.Proposito != string(PropositoConfirmacionTransferencia) || d.ContextoSolicitado.String != referenciaConfirmacion(ctx, accion) {
			dominio = ErrDesafioInvalido
			return struct{}{}, nil
		}
		_, dominio = g.VerificarDesafioEn(ctx, q, desafio, codigo, PropositoConfirmacionTransferencia)
		return struct{}{}, nil
	})
	if err != nil {
		return err
	}
	return dominio
}

// ConsentimientoAsistenteVigenteEn relee el consentimiento sin abrir otra conexión.
func (s *ServicioDeConsentimientos) ConsentimientoAsistenteVigenteEn(ctx context.Context, tx pgx.Tx, parte uuid.UUID) (bool, error) {
	r, err := enTransaccion(ctx, poolConfirmacion{tx}, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.EstadoConsentimientoAsistenteRow, error) {
		return q.EstadoConsentimientoAsistente(ctx, pgtype.UUID{Bytes: parte, Valid: true})
	})
	return r.Autorizado, err
}
