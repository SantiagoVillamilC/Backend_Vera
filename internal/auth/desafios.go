package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// vigenciaDesafio es la vida del código y del desafío desde su creación.
	vigenciaDesafio = 5 * time.Minute
	// rangoCodigo es el espacio de un código de seis dígitos.
	rangoCodigo = 1000000
	// longitudCodigo es la cantidad de dígitos que viaja al cliente.
	longitudCodigo = 6
	// resultadoDesafioCreado marca el desafío persistido antes de entregar.
	resultadoDesafioCreado = "creado"
	// resultadoEntregaFallida marca el desafío que el canal no pudo entregar.
	resultadoEntregaFallida = "entrega_fallida"
	// resultadoDesafioReenviado marca el código regenerado que reemplazó al anterior.
	resultadoDesafioReenviado = "reenviado"
)

// Proposito es el motivo del desafío y queda atado a la verificación de A2-06.2.
type Proposito string

const (
	// PropositoRegistro verifica el celular capturado en el alta.
	PropositoRegistro Proposito = "registro"
	// PropositoLogin es el segundo factor de un inicio de sesión.
	PropositoLogin Proposito = "login"
	// PropositoRecuperacion reinicia el acceso a la cuenta.
	PropositoRecuperacion Proposito = "recuperacion"
	// PropositoCambioNumero verifica el número nuevo del cambio.
	PropositoCambioNumero Proposito = "cambio_numero"
	// PropositoCambioNumeroViejo reautentica contra el número anterior.
	PropositoCambioNumeroViejo Proposito = "cambio_numero_viejo"
)

// Solicitud reúne los datos con los que se crea un desafío OTP.
type Solicitud struct {
	ParteID           uuid.UUID
	CredencialID      uuid.UUID
	Proposito         Proposito
	Celular           string
	Contexto          string
	Origen            string
	CorreoNormalizado string
}

// Desafio es lo que recibe el cliente: su id, la vigencia en segundos y el propósito, nunca el código.
type Desafio struct {
	ID               uuid.UUID
	ExpiraEnSegundos int
	Proposito        Proposito
}

// LectorOptIn consulta si la parte tiene un opt-in vigente para recibir el OTP.
type LectorOptIn interface {
	OptInVigente(ctx context.Context, parteID uuid.UUID) (bool, error)
}

// SiempreOptIn es el stub de pruebas mientras el consentimiento real llega con A1-05.1.
type SiempreOptIn struct{}

// OptInVigente responde que sí para cualquier parte.
func (SiempreOptIn) OptInVigente(context.Context, uuid.UUID) (bool, error) { return true, nil }

var _ LectorOptIn = SiempreOptIn{}

// GestorDesafios crea los desafíos OTP y los entrega por el canal tras confirmar la transacción.
type GestorDesafios struct {
	pool       abridorDeTransaccionesAuth
	canal      CanalOTP
	eventos    RegistradorEventos
	optIn      LectorOptIn
	pepper     []byte
	contadores Contadores
	claves     *Claves
	racionador *RacionadorOTP
}

// OpcionGestorDesafios ajusta el gestor durante su construcción.
type OpcionGestorDesafios func(*GestorDesafios)

// ConRacionamiento entrega a A2-06.3 el puerto y las claves con los que aplica los topes agregados.
func ConRacionamiento(contadores Contadores, claves *Claves) OpcionGestorDesafios {
	return func(g *GestorDesafios) {
		g.contadores, g.claves = contadores, claves
	}
}

// NuevoGestorDesafios construye el servicio y exige un OTP_PEPPER de al menos 32 bytes.
func NuevoGestorDesafios(pool *pgxpool.Pool, canal CanalOTP, eventos RegistradorEventos, optIn LectorOptIn, pepper []byte, opciones ...OpcionGestorDesafios) (*GestorDesafios, error) {
	if len(pepper) < 32 {
		return nil, fmt.Errorf("auth: OTP_PEPPER debe tener al menos 32 bytes, tiene %d", len(pepper))
	}
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	if optIn == nil {
		optIn = SiempreOptIn{}
	}
	g := &GestorDesafios{pool: poolDeAuth{pool: pool}, canal: canal, eventos: eventos, optIn: optIn, pepper: pepper}
	for _, opcion := range opciones {
		opcion(g)
	}
	if g.contadores != nil || g.claves != nil {
		racionador, err := NuevoRacionadorOTP(g.contadores, g.claves)
		if err != nil {
			return nil, err
		}
		g.racionador = racionador
	}
	return g, nil
}

// CrearDesafioRacionado aplica los topes agregados de cuenta y origen antes de crear el desafío.
func (g *GestorDesafios) CrearDesafioRacionado(ctx context.Context, solicitud Solicitud) (Desafio, error) {
	if g.racionador == nil {
		return Desafio{}, ConFailClosed(errors.New("auth: el racionamiento de OTP no está configurado"))
	}
	if solicitud.CorreoNormalizado == "" {
		return Desafio{}, ConFailClosed(errors.New("auth: el desafío racionado necesita el correo normalizado"))
	}
	if err := g.racionador.Exigir(ctx, solicitud.CorreoNormalizado, solicitud.Origen); err != nil {
		return Desafio{}, err
	}
	return g.CrearDesafio(ctx, solicitud)
}

// CrearDesafio genera el código, lo persiste con HMAC, lo entrega por el canal y devuelve su vigencia.
func (g *GestorDesafios) CrearDesafio(ctx context.Context, solicitud Solicitud) (Desafio, error) {
	if err := solicitud.valida(); err != nil {
		return Desafio{}, err
	}
	if err := g.exigirDestinoVerificado(ctx, solicitud); err != nil {
		return Desafio{}, err
	}
	if err := g.exigirOptIn(ctx, solicitud); err != nil {
		return Desafio{}, err
	}
	codigo, err := codigoDelDesafio(g.canal)
	if err != nil {
		return Desafio{}, err
	}
	ahora := time.Now().UTC()
	fila, err := g.persistir(ctx, solicitud, codigo, ahora)
	if err != nil {
		return Desafio{}, err
	}
	if _, err := g.canal.Enviar(ctx, solicitud.Celular, codigo); err != nil {
		return Desafio{}, g.entregaFallida(ctx, fila.ID, solicitud, err)
	}
	return Desafio{ID: uuidDePgtype(fila.ID), ExpiraEnSegundos: int(vigenciaDesafio.Seconds()), Proposito: solicitud.Proposito}, nil
}

// persistir inserta el desafío y su evento en una sola transacción y confirma antes de entregar.
func (g *GestorDesafios) persistir(ctx context.Context, solicitud Solicitud, codigo string, ahora time.Time) (repositorio.AuthDesafio, error) {
	return enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.AuthDesafio, error) {
		fila, err := q.InsertarDesafio(ctx, repositorio.InsertarDesafioParams{
			ID:                 pgtype.UUID{Bytes: uuid.New(), Valid: true},
			ParteID:            uuidOPgtype(solicitud.ParteID),
			CredencialID:       uuidOPgtype(solicitud.CredencialID),
			Proposito:          string(solicitud.Proposito),
			CelularDestino:     solicitud.Celular,
			CodigoHmac:         hmacDeCodigo(g.pepper, codigo),
			CreadoEn:           pgtype.Timestamptz{Time: ahora, Valid: true},
			ExpiraEn:           pgtype.Timestamptz{Time: ahora.Add(vigenciaDesafio), Valid: true},
			UltimoEnvioEn:      pgtype.Timestamptz{Time: ahora, Valid: true},
			ContextoSolicitado: pgtype.Text{String: solicitud.Contexto, Valid: solicitud.Contexto != ""},
		})
		if err != nil {
			return repositorio.AuthDesafio{}, fmt.Errorf("auth: insertar el desafío: %w", err)
		}
		if err := registrarEvento(ctx, g.eventos, q, g.eventoDeDesafio(solicitud, ahora, resultadoDesafioCreado)); err != nil {
			return repositorio.AuthDesafio{}, err
		}
		return fila, nil
	})
}

// entregaFallida invalida el desafío ya persistido, publica el evento y devuelve ErrEntregaFallida.
func (g *GestorDesafios) entregaFallida(ctx context.Context, id pgtype.UUID, solicitud Solicitud, causa error) error {
	_, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		if _, err := q.InvalidarDesafio(ctx, id); err != nil {
			return struct{}{}, fmt.Errorf("auth: invalidar el desafío sin entrega: %w", err)
		}
		return struct{}{}, registrarEvento(ctx, g.eventos, q, g.eventoDeDesafio(solicitud, time.Now().UTC(), resultadoEntregaFallida))
	})
	return fmt.Errorf("%w: %w", ErrEntregaFallida, errors.Join(causa, err))
}

// exigirDestinoVerificado rechaza login y recuperación cuando la credencial no tiene el celular verificado.
func (g *GestorDesafios) exigirDestinoVerificado(ctx context.Context, solicitud Solicitud) error {
	if solicitud.Proposito == PropositoRegistro || solicitud.Proposito == PropositoCambioNumero {
		return nil
	}
	if solicitud.CredencialID == uuid.Nil {
		return ErrCelularSinVerificar
	}
	verificado, err := enTransaccion(ctx, g.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (bool, error) {
		credencial, err := q.CredencialPorID(ctx, pgtype.UUID{Bytes: solicitud.CredencialID, Valid: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("auth: leer la credencial del desafío: %w", err)
		}
		return credencial.CelularVerificadoEn.Valid, nil
	})
	if err != nil {
		return err
	}
	if !verificado {
		return ErrCelularSinVerificar
	}
	return nil
}

// exigirOptIn exige el consentimiento vigente cuando el desafío pertenece a una parte conocida.
func (g *GestorDesafios) exigirOptIn(ctx context.Context, solicitud Solicitud) error {
	if solicitud.ParteID == uuid.Nil {
		return nil
	}
	vigente, err := g.optIn.OptInVigente(ctx, solicitud.ParteID)
	if err != nil {
		return fmt.Errorf("auth: consultar el opt-in de la parte: %w", err)
	}
	if !vigente {
		return ErrConsentimientoRequerido
	}
	return nil
}

// eventoDeDesafio arma el evento de auditoría del desafío sin el código ni su HMAC.
func (g *GestorDesafios) eventoDeDesafio(solicitud Solicitud, fecha time.Time, resultado string) Evento {
	return Evento{
		Tipo:         TipoEventoDesafio,
		Actor:        solicitud.ParteID,
		CredencialID: solicitud.CredencialID,
		Resultado:    resultado,
		Fecha:        fecha,
		Origen:       solicitud.Origen,
		Contexto:     solicitud.Contexto,
		Detalle:      map[string]string{"proposito": string(solicitud.Proposito), "contexto_solicitado": solicitud.Contexto},
	}
}

// valida rechaza un propósito fuera del catálogo y un destino vacío.
func (s Solicitud) valida() error {
	if !s.Proposito.valido() {
		return fmt.Errorf("auth: propósito de desafío desconocido %q", s.Proposito)
	}
	if s.Celular == "" {
		return errors.New("auth: el desafío necesita un celular de destino")
	}
	return nil
}

// valido reporta si el propósito está en el catálogo cerrado de la columna.
func (p Proposito) valido() bool {
	switch p {
	case PropositoRegistro, PropositoLogin, PropositoRecuperacion, PropositoCambioNumero, PropositoCambioNumeroViejo:
		return true
	default:
		return false
	}
}

// canalConCodigoFijo es el canal simulado que puede imponer un código determinista.
type canalConCodigoFijo interface {
	CodigoFijo() (string, bool)
}

// codigoDelDesafio usa el código fijo del simulador si lo tiene y si no genera seis dígitos de crypto/rand.
func codigoDelDesafio(canal CanalOTP) (string, error) {
	if simulado, ok := canal.(canalConCodigoFijo); ok {
		if codigo, hay := simulado.CodigoFijo(); hay {
			return codigo, nil
		}
	}
	return codigoAleatorio()
}

// codigoAleatorio devuelve seis dígitos sin el sesgo del módulo.
func codigoAleatorio() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(rangoCodigo))
	if err != nil {
		return "", fmt.Errorf("auth: generar el código del desafío: %w", err)
	}
	return fmt.Sprintf("%0*d", longitudCodigo, n.Int64()), nil
}

// hmacDeCodigo deriva el código con HMAC-SHA256 y OTP_PEPPER para que en la base no quede en claro.
func hmacDeCodigo(pepper []byte, codigo string) []byte {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(codigo))
	return mac.Sum(nil)
}
