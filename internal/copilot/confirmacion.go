package copilot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrAccionNoEncontrada no distingue acciones ajenas, inexistentes o de otro origen.
	ErrAccionNoEncontrada = errors.New("asistente: acción no encontrada")
	// ErrAccionNoPendiente rechaza ejecutar de nuevo una propuesta resuelta sin transferencia.
	ErrAccionNoPendiente = errors.New("asistente: acción no pendiente")
	// ErrAccionExpirada indica que la propuesta agotó su vigencia.
	ErrAccionExpirada = errors.New("asistente: acción expirada")
	// ErrCodigoAccionRequerido exige el segundo factor vinculado a la propuesta.
	ErrCodigoAccionRequerido = errors.New("asistente: código de confirmación requerido")
)

// ConfirmacionAccion contiene sólo pruebas de confirmación; nunca monto ni destino.
type ConfirmacionAccion struct {
	DesafioID            uuid.UUID
	Codigo               string
	SinVerificacionLocal bool
}

// AccionResuelta proyecta datos releídos del servidor y el resultado del núcleo.
type AccionResuelta struct {
	Accion        TarjetaTexto
	Transferencia *nucleo.Transferencia
}

// ControlAcciones es la frontera de ejecución HTTP, sin acceso desde el catálogo de IA.
type ControlAcciones struct {
	pool           *pgxpool.Pool
	nucleo         *nucleo.Servicio
	otp            *auth.GestorDesafios
	consentimiento *ControlConsentimiento
	umbral         nucleo.Centavos
}

// NuevoControlAcciones conecta la confirmación explícita con auth y núcleo.
func NuevoControlAcciones(pool *pgxpool.Pool, n *nucleo.Servicio, otp *auth.GestorDesafios, c *ControlConsentimiento, umbral nucleo.Centavos) (*ControlAcciones, error) {
	if pool == nil || n == nil || otp == nil || c == nil || umbral <= 0 {
		return nil, ErrNoDisponibleIA
	}
	return &ControlAcciones{pool, n, otp, c, umbral}, nil
}

type resultadoAccion struct {
	respuesta AccionResuelta
	entrega   *auth.EntregaConfirmacion
	dominio   error
}

func rolConfirmacion(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "SET LOCAL ROLE rol_asistente")
	return err
}
func (s *ControlAcciones) leer(ctx context.Context, id uuid.UUID) (repositorio.AccionPendiente, error) {
	parte, err := parteDelAsistente(ctx)
	if err != nil {
		return repositorio.AccionPendiente{}, err
	}
	ident, _ := identidad.DeContexto(ctx)
	a, err := EnTransaccionAsistente(ctx, s.pool, func(q *repositorio.Queries) (repositorio.AccionPendiente, error) {
		return q.ObtenerAccion(ctx, repositorio.ObtenerAccionParams{ID: uuidCiclo(id), UsuarioID: uuidCiclo(parte)})
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && a.UsuarioFinancieroID != ident.UsuarioID {
		return a, ErrAccionNoEncontrada
	}
	return a, err
}

func (s *ControlAcciones) conAccion(ctx context.Context, id uuid.UUID, fn func(pgx.Tx, *repositorio.Queries, repositorio.AccionPendiente) (resultadoAccion, error)) (resultadoAccion, error) {
	a, err := s.leer(ctx, id)
	if err != nil {
		return resultadoAccion{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return resultadoAccion{}, err
	}
	defer func() {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(c)
	}()
	if err := rolConfirmacion(ctx, tx); err != nil {
		return resultadoAccion{}, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		return resultadoAccion{}, err
	}
	q := repositorio.New(tx)
	c, err := q.BloquearConversacionConfirmacion(ctx, repositorio.BloquearConversacionConfirmacionParams{ID: a.ConversacionID, UsuarioID: a.UsuarioID})
	if err != nil {
		return resultadoAccion{}, err
	}
	a, err = q.BloquearAccion(ctx, repositorio.BloquearAccionParams{ID: a.ID, UsuarioID: a.UsuarioID})
	if err != nil {
		return resultadoAccion{}, err
	}
	if c.Estado != "activo" && a.Estado == "pendiente" {
		return resultadoAccion{}, ErrAccionNoEncontrada
	}
	r, err := fn(tx, q, a)
	if err != nil {
		return resultadoAccion{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return resultadoAccion{}, err
	}
	return r, nil
}

type lectorConsentimientoEn interface {
	ConsentimientoAsistenteVigenteEn(context.Context, pgx.Tx, uuid.UUID) (bool, error)
}

func (s *ControlAcciones) consentirEn(ctx context.Context, tx pgx.Tx, q *repositorio.Queries, a repositorio.AccionPendiente) (error, error) {
	l, ok := s.consentimiento.lector.(lectorConsentimientoEn)
	if !ok {
		return nil, ErrNoDisponibleIA
	}
	permitido, err := l.ConsentimientoAsistenteVigenteEn(ctx, tx, uuid.UUID(a.UsuarioID.Bytes))
	if roleErr := rolConfirmacion(ctx, tx); roleErr != nil {
		return nil, roleErr
	}
	if err != nil {
		return nil, err
	}
	if permitido {
		return nil, nil
	}
	if a.Estado == "pendiente" {
		a, err = q.CancelarAccion(ctx, repositorio.CancelarAccionParams{ID: a.ID, UsuarioID: a.UsuarioID})
		if err != nil {
			return nil, err
		}
		if err := anexarEstadoAccion(ctx, q, a); err != nil {
			return nil, err
		}
	}
	return ErrConsentimientoAsistente, nil
}

func vencerSiCorresponde(ctx context.Context, q *repositorio.Queries, a repositorio.AccionPendiente) (bool, error) {
	if a.Estado == "vencida" {
		return true, nil
	}
	if a.Estado != "pendiente" || a.ExpiraEn.Time.After(time.Now()) {
		return false, nil
	}
	a, err := q.VencerAccion(ctx, repositorio.VencerAccionParams{ID: a.ID, UsuarioID: a.UsuarioID})
	if err != nil {
		return false, err
	}
	return true, anexarEstadoAccion(ctx, q, a)
}
func anexarEstadoAccion(ctx context.Context, q *repositorio.Queries, a repositorio.AccionPendiente, estadoTransferencia ...string) error {
	texto := fmt.Sprintf("Estado registrado por el servidor: la acción %s quedó %s.", uuid.UUID(a.ID.Bytes), a.Estado)
	if a.CodigoError.Valid {
		texto += " Motivo: " + a.CodigoError.String + "."
	}
	if len(estadoTransferencia) > 0 {
		texto += " Estado de la transferencia: " + estadoTransferencia[0] + ". Si está reservada, su resolución por el riel sigue pendiente."
	}
	_, err := q.AnexarMensaje(ctx, repositorio.AnexarMensajeParams{ConversacionID: a.ConversacionID, UsuarioID: a.UsuarioID, Rol: "asistente", Canal: "texto", Contenido: texto, Metadatos: []byte(`{"evento_accion":true}`)})
	return err
}
func tarjetaAccion(a repositorio.AccionPendiente) TarjetaTexto {
	t := TarjetaTexto{ID: uuid.UUID(a.ID.Bytes), Estado: a.Estado, Tipo: a.Tipo, Nombre: a.NombreMostrado, Celular: a.CelularEnmascarado.String, Entidad: a.EntidadDestino.String, TipoLlave: a.TipoLlave.String, Monto: nucleo.Centavos(a.Monto), RequiereCodigo: a.RequiereCodigo, ExpiraEn: a.ExpiraEn.Time}
	if a.Tipo == "transferencia_llave" {
		t.Llave = "***"
	}
	return t
}

// SolicitarCodigo vincula y persiste el desafío antes de entregarlo por el canal OTP.
func (s *ControlAcciones) SolicitarCodigo(ctx context.Context, id uuid.UUID, origen string) (auth.Desafio, error) {
	a, err := s.leer(ctx, id)
	if err != nil {
		return auth.Desafio{}, err
	}
	if err := s.consentimiento.Exigir(ctx); err != nil {
		return auth.Desafio{}, err
	}
	if a.Estado != "pendiente" {
		if a.Estado == "vencida" {
			return auth.Desafio{}, ErrAccionExpirada
		}
		return auth.Desafio{}, ErrAccionNoPendiente
	}
	if !a.ExpiraEn.Time.After(time.Now()) {
		_, err := s.Cancelar(ctx, id)
		return auth.Desafio{}, err
	}
	plan, err := s.otp.PlanificarCodigoTransferencia(ctx, id, origen)
	if err != nil {
		return auth.Desafio{}, err
	}
	r, err := s.conAccion(ctx, id, func(tx pgx.Tx, q *repositorio.Queries, a repositorio.AccionPendiente) (resultadoAccion, error) {
		if vencida, err := vencerSiCorresponde(ctx, q, a); err != nil {
			return resultadoAccion{}, err
		} else if vencida {
			return resultadoAccion{dominio: ErrAccionExpirada}, nil
		}
		if a.Estado != "pendiente" {
			return resultadoAccion{dominio: ErrAccionNoPendiente}, nil
		}
		if dominio, err := s.consentirEn(ctx, tx, q, a); err != nil || dominio != nil {
			return resultadoAccion{dominio: dominio}, err
		}
		previo := uuid.Nil
		if a.DesafioID.Valid {
			previo = uuid.UUID(a.DesafioID.Bytes)
		}
		e, err := plan.PrepararEn(ctx, tx, previo)
		if roleErr := rolConfirmacion(ctx, tx); roleErr != nil {
			return resultadoAccion{}, roleErr
		}
		if err != nil {
			if esErrorOTP(err) {
				return resultadoAccion{dominio: err}, nil
			}
			return resultadoAccion{}, err
		}
		if _, err := q.VincularDesafioAccion(ctx, repositorio.VincularDesafioAccionParams{ID: a.ID, UsuarioID: a.UsuarioID, DesafioID: uuidCiclo(e.Desafio.ID)}); err != nil {
			return resultadoAccion{}, err
		}
		return resultadoAccion{entrega: e}, nil
	})
	if err != nil {
		return auth.Desafio{}, err
	}
	if r.dominio != nil {
		return auth.Desafio{}, r.dominio
	}
	if err := r.entrega.Entregar(ctx); err != nil {
		return auth.Desafio{}, err
	}
	return r.entrega.Desafio, nil
}

// Cancelar resuelve la propuesta propia sin requerir que el consentimiento siga vigente.
func (s *ControlAcciones) Cancelar(ctx context.Context, id uuid.UUID) (AccionResuelta, error) {
	r, err := s.conAccion(ctx, id, func(_ pgx.Tx, q *repositorio.Queries, a repositorio.AccionPendiente) (resultadoAccion, error) {
		if vencida, err := vencerSiCorresponde(ctx, q, a); err != nil {
			return resultadoAccion{}, err
		} else if vencida {
			return resultadoAccion{dominio: ErrAccionExpirada}, nil
		}
		if a.Estado == "cancelada" {
			return resultadoAccion{respuesta: AccionResuelta{Accion: tarjetaAccion(a)}}, nil
		}
		if a.Estado != "pendiente" {
			return resultadoAccion{dominio: ErrAccionNoPendiente}, nil
		}
		a, err := q.CancelarAccion(ctx, repositorio.CancelarAccionParams{ID: a.ID, UsuarioID: a.UsuarioID})
		if err != nil {
			return resultadoAccion{}, err
		}
		if err := anexarEstadoAccion(ctx, q, a); err != nil {
			return resultadoAccion{}, err
		}
		return resultadoAccion{respuesta: AccionResuelta{Accion: tarjetaAccion(a)}}, nil
	})
	if err != nil {
		return AccionResuelta{}, err
	}
	return r.respuesta, r.dominio
}

// Confirmar confirma ledger, OTP y acción juntos; cualquier fallo técnico revierte los tres.
func (s *ControlAcciones) Confirmar(ctx context.Context, id uuid.UUID, sol ConfirmacionAccion) (AccionResuelta, error) {
	a, err := s.leer(ctx, id)
	if err != nil {
		return AccionResuelta{}, err
	}
	if err := s.consentimiento.Exigir(ctx); err != nil {
		if errors.Is(err, ErrConsentimientoAsistente) {
			_, cancelErr := s.Cancelar(ctx, id)
			if cancelErr != nil && !errors.Is(cancelErr, ErrAccionNoPendiente) && !errors.Is(cancelErr, ErrAccionExpirada) {
				return AccionResuelta{}, cancelErr
			}
		}
		return AccionResuelta{}, err
	}
	var plan *nucleo.PlanTransferencia
	var planErr error
	requiere := a.RequiereCodigo || nucleo.Centavos(a.Monto) > s.umbral || sol.SinVerificacionLocal
	if a.Estado == "pendiente" && a.ExpiraEn.Time.After(time.Now()) && (!requiere || sol.DesafioID != uuid.Nil && sol.Codigo != "") {
		origen := a.UsuarioFinancieroID
		cuenta := nucleo.NombreCuentaDisponible(origen)
		if a.Tipo == "transferencia_interna" {
			plan, planErr = s.nucleo.PlanificarTransferencia(ctx, nucleo.SolicitudTransferencia{UsuarioID: origen, CuentaOrigen: cuenta, LlaveDestino: a.Destino, Monto: nucleo.Centavos(a.Monto), Moneda: a.Moneda, IdempotencyKey: id.String()})
		} else {
			plan, planErr = s.nucleo.PlanificarTransferenciaPorLlave(ctx, nucleo.SolicitudEnvioPorLlave{UsuarioID: origen, CuentaOrigen: cuenta, TipoLlave: a.TipoLlave.String, Llave: a.Destino, Monto: nucleo.Centavos(a.Monto), Moneda: a.Moneda, IdempotencyKey: id.String()})
		}
	}
	r, err := s.conAccion(ctx, id, func(tx pgx.Tx, q *repositorio.Queries, a repositorio.AccionPendiente) (resultadoAccion, error) {
		if vencida, err := vencerSiCorresponde(ctx, q, a); err != nil {
			return resultadoAccion{}, err
		} else if vencida {
			return resultadoAccion{dominio: ErrAccionExpirada}, nil
		}
		if a.Estado == "confirmada" {
			t, err := s.nucleo.ConsultarTransferenciaEn(ctx, tx, a.UsuarioFinancieroID, uuid.UUID(a.TransferenciaID.Bytes))
			if err != nil {
				return resultadoAccion{}, err
			}
			return resultadoAccion{respuesta: AccionResuelta{Accion: tarjetaAccion(a), Transferencia: &t}}, nil
		}
		if a.Estado != "pendiente" {
			return resultadoAccion{dominio: ErrAccionNoPendiente}, nil
		}
		if dominio, err := s.consentirEn(ctx, tx, q, a); err != nil || dominio != nil {
			return resultadoAccion{dominio: dominio}, err
		}
		if a.RequiereCodigo || nucleo.Centavos(a.Monto) > s.umbral || sol.SinVerificacionLocal {
			if !a.RequiereCodigo {
				var err error
				a, err = q.ElevarCodigoAccion(ctx, repositorio.ElevarCodigoAccionParams{ID: a.ID, UsuarioID: a.UsuarioID})
				if err != nil {
					return resultadoAccion{}, err
				}
			}
			if sol.DesafioID == uuid.Nil || sol.Codigo == "" || !a.DesafioID.Valid || uuid.UUID(a.DesafioID.Bytes) != sol.DesafioID {
				return resultadoAccion{dominio: ErrCodigoAccionRequerido}, nil
			}
			err := s.otp.VerificarCodigoTransferenciaEn(ctx, tx, id, sol.DesafioID, sol.Codigo)
			if roleErr := rolConfirmacion(ctx, tx); roleErr != nil {
				return resultadoAccion{}, roleErr
			}
			if err != nil {
				if esErrorOTP(err) {
					return resultadoAccion{dominio: err}, nil
				}
				return resultadoAccion{}, err
			}
		}
		if planErr != nil {
			return resolverFalloAccion(ctx, q, a, planErr)
		}
		if plan == nil {
			return resultadoAccion{}, ErrNoDisponibleIA
		}
		t, err := plan.EjecutarEn(ctx, tx)
		if roleErr := rolConfirmacion(ctx, tx); roleErr != nil {
			return resultadoAccion{}, roleErr
		}
		if err != nil {
			return resolverFalloAccion(ctx, q, a, err)
		}
		a, err = q.ConfirmarAccion(ctx, repositorio.ConfirmarAccionParams{ID: a.ID, UsuarioID: a.UsuarioID, TransferenciaID: uuidCiclo(t.ID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return resultadoAccion{}, ErrAccionExpirada
		}
		if err != nil {
			return resultadoAccion{}, err
		}
		if err := anexarEstadoAccion(ctx, q, a, t.Estado); err != nil {
			return resultadoAccion{}, err
		}
		return resultadoAccion{respuesta: AccionResuelta{Accion: tarjetaAccion(a), Transferencia: &t}}, nil
	})
	if errors.Is(err, ErrAccionExpirada) {
		_, _ = s.Cancelar(ctx, id)
	}
	if err != nil {
		return AccionResuelta{}, err
	}
	return r.respuesta, r.dominio
}

func resolverFalloAccion(ctx context.Context, q *repositorio.Queries, a repositorio.AccionPendiente, causa error) (resultadoAccion, error) {
	codigo := CodigoRechazoNucleo(causa)
	if codigo == "" {
		return resultadoAccion{}, causa
	}
	a, err := q.FallarAccion(ctx, repositorio.FallarAccionParams{ID: a.ID, UsuarioID: a.UsuarioID, CodigoError: textoOpcionalPreparacion(codigo)})
	if errors.Is(err, pgx.ErrNoRows) {
		return resultadoAccion{}, ErrAccionExpirada
	}
	if err != nil {
		return resultadoAccion{}, err
	}
	if err := anexarEstadoAccion(ctx, q, a); err != nil {
		return resultadoAccion{}, err
	}
	return resultadoAccion{dominio: causa}, nil
}

// CodigoRechazoNucleo enumera rechazos definitivos; nunca convierte una caída en acción fallida.
func CodigoRechazoNucleo(err error) string {
	for _, c := range []struct {
		err    error
		codigo string
	}{{nucleo.ErrSaldoInsuficiente, "InsufficientFunds"}, {nucleo.ErrTopeOperacionExcedido, "OperationLimitExceeded"}, {nucleo.ErrCupoMensualExcedido, "MonthlyQuotaExceeded"}, {nucleo.ErrDestinoNoExiste, "RecipientNotFound"}, {nucleo.ErrLlaveNoEncontrada, "RecipientNotFound"}, {nucleo.ErrDestinoPropio, "SelfTransferNotAllowed"}, {nucleo.ErrIdempotenciaEnConflicto, "IdempotencyConflict"}, {nucleo.ErrMontoInvalido, "ValidationError"}, {nucleo.ErrMonedaInvalida, "ValidationError"}, {nucleo.ErrLlaveInvalida, "ValidationError"}} {
		if errors.Is(err, c.err) {
			return c.codigo
		}
	}
	return ""
}
func esErrorOTP(err error) bool {
	for _, e := range []error{auth.ErrCodigoInvalido, auth.ErrCodigoExpirado, auth.ErrDemasiadosIntentos, auth.ErrDesafioInvalido, auth.ErrEnfriamientoReenvio, auth.ErrDemasiadosReenvios} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}
