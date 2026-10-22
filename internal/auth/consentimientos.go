package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tipos de consentimiento que la columna acepta, en el mismo vocabulario del contrato.
const (
	TipoConsentimientoTratamiento = "tratamiento_datos"
	TipoConsentimientoWhatsApp    = "whatsapp_optin"
	TipoConsentimientoBiometricos = "biometricos"
	TipoConsentimientoAsistente   = "asistente_ia"
)

// Acciones del consentimiento: cada otorgamiento o revocacion es una fila nueva.
const (
	AccionConsentimientoOtorgar = "otorgar"
	AccionConsentimientoRevocar = "revocar"
)

// MetodoConsentimientoPorDefecto es el medio funcional con el que se registra un consentimiento sin metodo explicito.
const MetodoConsentimientoPorDefecto = "app"

// maxRunasUserAgent acota el user_agent que entra al log de consentimientos.
const maxRunasUserAgent = 512

// LectorOptInDeConsentimientos implementa el puerto LectorOptIn de A2-06.1 sobre auth.consentimientos.
type LectorOptInDeConsentimientos struct {
	consultor consultorDeUltimoConsentimiento
}

// consultorDeUltimoConsentimiento es la lectura que el lector necesita; el adaptador de Postgres la respalda.
type consultorDeUltimoConsentimiento interface {
	UltimoConsentimientoPorTipo(ctx context.Context, parteID uuid.UUID, tipo string) (aceptado bool, encontrado bool, err error)
}

// NuevoLectorOptInDeConsentimientos construye el lector sobre el pool de la API.
func NuevoLectorOptInDeConsentimientos(pool *pgxpool.Pool) *LectorOptInDeConsentimientos {
	return &LectorOptInDeConsentimientos{consultor: consultorDeConsentimientosPostgres{abridor: poolDeAuth{pool: pool}}}
}

var _ LectorOptIn = (*LectorOptInDeConsentimientos)(nil)

// OptInVigente responde con la ULTIMA fila de WhatsApp de la parte: revocar despues de otorgar da false.
func (l *LectorOptInDeConsentimientos) OptInVigente(ctx context.Context, parteID uuid.UUID) (bool, error) {
	aceptado, encontrado, err := l.consultor.UltimoConsentimientoPorTipo(ctx, parteID, TipoConsentimientoWhatsApp)
	if err != nil {
		return false, fmt.Errorf("auth: consultar el ultimo consentimiento de WhatsApp: %w", err)
	}
	return encontrado && aceptado, nil
}

// consultorDeConsentimientosPostgres lee la ultima fila del tipo en su propia transaccion como rol_auth.
type consultorDeConsentimientosPostgres struct{ abridor abridorDeTransaccionesAuth }

var _ consultorDeUltimoConsentimiento = consultorDeConsentimientosPostgres{}

// UltimoConsentimientoPorTipo devuelve la fila mas reciente del tipo; la ausencia se reporta como no encontrado.
func (c consultorDeConsentimientosPostgres) UltimoConsentimientoPorTipo(ctx context.Context, parteID uuid.UUID, tipo string) (bool, bool, error) {
	resultado, err := enTransaccion(ctx, c.abridor,
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (consultaDeUltimoConsentimiento, error) {
			vigente, err := q.UltimoConsentimientoPorTipo(ctx, repositorio.UltimoConsentimientoPorTipoParams{
				ParteID: uuidOPgtype(parteID),
				Tipo:    tipo,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return consultaDeUltimoConsentimiento{}, nil
			}
			if err != nil {
				return consultaDeUltimoConsentimiento{}, err
			}
			return consultaDeUltimoConsentimiento{aceptado: vigente, encontrado: true}, nil
		})
	if err != nil {
		return false, false, err
	}
	return resultado.aceptado, resultado.encontrado, nil
}

// consultaDeUltimoConsentimiento es el resultado de la lectura cuando enTransaccion exige un solo valor.
type consultaDeUltimoConsentimiento struct {
	aceptado   bool
	encontrado bool
}

// Consentimiento es una fila append-only del log: cada accion copia el texto exacto que se presento.
type Consentimiento struct {
	Tipo         string
	VersionTexto string
	Texto        string
	Aceptado     bool
	Accion       string
	Origen       string
	UserAgent    string
	Metodo       string
	Fecha        time.Time
}

// SolicitudDeConsentimientoNuevo es lo que el handler entrega al servicio para registrar un consentimiento.
type SolicitudDeConsentimientoNuevo struct {
	ParteID      uuid.UUID
	Tipo         string
	VersionTexto string
	Aceptado     bool
	Metodo       string
	Origen       string
	UserAgent    string
}

// ServicioDeConsentimientos consulta y registra consentimientos de una persona, con su log inmutable.
type ServicioDeConsentimientos struct {
	abridor abridorDeTransaccionesAuth
}

// NuevoServicioDeConsentimientos construye el servicio sobre el pool de la API.
func NuevoServicioDeConsentimientos(pool *pgxpool.Pool) *ServicioDeConsentimientos {
	return &ServicioDeConsentimientos{abridor: poolDeAuth{pool: pool}}
}

// Listar devuelve todas las filas de la parte, mas reciente primero, como prueba de lo que se presento.
func (s *ServicioDeConsentimientos) Listar(ctx context.Context, parteID uuid.UUID) ([]Consentimiento, error) {
	filas, err := enTransaccion(ctx, s.abridor,
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) ([]repositorio.Consentimiento, error) {
			return q.ListarConsentimientosDeParte(ctx, uuidOPgtype(parteID))
		})
	if err != nil {
		return nil, fmt.Errorf("auth: listar los consentimientos de la parte: %w", err)
	}
	consentimientos := make([]Consentimiento, 0, len(filas))
	for _, fila := range filas {
		consentimientos = append(consentimientos, consentimientoDeFila(fila))
	}
	return consentimientos, nil
}

// Registrar copia el texto vigente del servidor y agrega una fila; la revocacion del opt-in unico se rechaza (P-13).
func (s *ServicioDeConsentimientos) Registrar(ctx context.Context, solicitud SolicitudDeConsentimientoNuevo) (Consentimiento, error) {
	solicitud.Metodo = metodoDeConsentimiento(solicitud.Metodo)
	solicitud.UserAgent = recortarUserAgent(solicitud.UserAgent)
	if err := s.exigirRevocacionPermitida(ctx, solicitud); err != nil {
		return Consentimiento{}, err
	}
	fila, err := enTransaccion(ctx, s.abridor,
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.Consentimiento, error) {
			return s.insertar(ctx, q, solicitud)
		})
	if err != nil {
		return Consentimiento{}, err
	}
	return consentimientoDeFila(fila), nil
}

// exigirRevocacionPermitida corta la revocacion de whatsapp_optin mientras sea el unico canal del OTP (P-13).
func (s *ServicioDeConsentimientos) exigirRevocacionPermitida(_ context.Context, solicitud SolicitudDeConsentimientoNuevo) error {
	if solicitud.Tipo != TipoConsentimientoWhatsApp || solicitud.Aceptado {
		return nil
	}
	return ErrWhatsappUnicoCanal
}

// insertar lee el texto exacto del catalogo y agrega la fila append-only con el actor y el origen.
func (s *ServicioDeConsentimientos) insertar(ctx context.Context, q *repositorio.Queries, solicitud SolicitudDeConsentimientoNuevo) (repositorio.Consentimiento, error) {
	if solicitud.Tipo == TipoConsentimientoAsistente {
		if err := validarVersionAsistente(ctx, q, solicitud.VersionTexto); err != nil {
			return repositorio.Consentimiento{}, err
		}
	}
	texto, err := q.TextoDeConsentimiento(ctx, repositorio.TextoDeConsentimientoParams{
		Tipo:    solicitud.Tipo,
		Version: solicitud.VersionTexto,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return repositorio.Consentimiento{}, ErrVersionDeConsentimientoInexistente
	}
	if err != nil {
		return repositorio.Consentimiento{}, fmt.Errorf("auth: leer el texto del consentimiento %q: %w", solicitud.Tipo, err)
	}
	accion := AccionConsentimientoRevocar
	if solicitud.Aceptado {
		accion = AccionConsentimientoOtorgar
	}
	fila, err := q.CrearConsentimiento(ctx, repositorio.CrearConsentimientoParams{
		ID:           uuidOPgtype(uuid.New()),
		ParteID:      uuidOPgtype(solicitud.ParteID),
		Tipo:         solicitud.Tipo,
		VersionTexto: texto.Version,
		Texto:        texto.Texto,
		Aceptado:     solicitud.Aceptado,
		Accion:       accion,
		Actor:        uuidOPgtype(solicitud.ParteID),
		Origen:       solicitud.Origen,
		UserAgent:    solicitud.UserAgent,
		Metodo:       solicitud.Metodo,
		OcurridoEn:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if err != nil {
		return repositorio.Consentimiento{}, fmt.Errorf("auth: insertar el consentimiento %q: %w", solicitud.Tipo, err)
	}
	return fila, nil
}

// consentimientoDeFila traduce la fila persistida a la forma que expone el contrato.
func consentimientoDeFila(fila repositorio.Consentimiento) Consentimiento {
	return Consentimiento{
		Tipo:         fila.Tipo,
		VersionTexto: fila.VersionTexto,
		Texto:        fila.Texto,
		Aceptado:     fila.Aceptado,
		Accion:       fila.Accion,
		Origen:       fila.Origen,
		UserAgent:    fila.UserAgent,
		Metodo:       fila.Metodo,
		Fecha:        fila.OcurridoEn.Time,
	}
}

// metodoDeConsentimiento aplica app como default y recorta el metodo recibido.
func metodoDeConsentimiento(metodo string) string {
	limpio := strings.TrimSpace(metodo)
	if limpio == "" {
		return MetodoConsentimientoPorDefecto
	}
	return limpio
}

// recortarUserAgent acota las runas del user_agent para no inflar el log inmutable.
func recortarUserAgent(userAgent string) string {
	runas := []rune(strings.TrimSpace(userAgent))
	if len(runas) > maxRunasUserAgent {
		runas = runas[:maxRunasUserAgent]
	}
	return string(runas)
}
