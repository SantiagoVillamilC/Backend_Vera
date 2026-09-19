package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// motivoRegistro es el motivo corto de la transicion inicial de la credencial.
	motivoRegistro = "registro_persona"
	// resultadoRegistroCreado marca el evento de alta.
	resultadoRegistroCreado = "creado"
	// metodoRegistro identifica el origen funcional de la fila de consentimiento.
	metodoRegistro = "registro"
)

// SolicitudDeRegistro es el alta de persona que el handler entrega al servicio.
type SolicitudDeRegistro struct {
	Correo          string
	Contrasena      string
	Celular         string
	NombreCompleto  string
	TipoDocumento   string
	NumeroDocumento string
	Consentimientos []SolicitudDeConsentimiento
	Origen          string
	UserAgent       string
}

// SolicitudDeConsentimiento es la aceptacion que el cliente declara haber mostrado.
type SolicitudDeConsentimiento struct {
	Tipo         string
	VersionTexto string
	Aceptado     bool
}

// Registro es la cuenta pendiente creada, con su desafio y sin ningun secreto.
type Registro struct {
	ParteID        uuid.UUID
	CredencialID   uuid.UUID
	Correo         string
	Celular        string
	NombreCompleto string
	Estado         EstadoCredencial
	FechaCreacion  time.Time
	Desafio        Desafio
}

// CreadorDeDesafioDeRegistro crea el desafio racionado del alta; lo satisface el GestorDesafios de A2-06.
type CreadorDeDesafioDeRegistro interface {
	CrearDesafioRacionado(ctx context.Context, solicitud Solicitud) (Desafio, error)
}

// AbridorDeSubcuentas es el metodo publico del nucleo que el registro dispara tras el commit.
type AbridorDeSubcuentas interface {
	AbrirSubcuentasDeUsuario(ctx context.Context, usuarioID string) (int64, error)
}

// RegistradorDePersonas valida, deriva y persiste el alta; no emite tokens.
type RegistradorDePersonas struct {
	pool       *pgxpool.Pool
	derivador  Derivador
	eventos    RegistradorEventos
	desafios   CreadorDeDesafioDeRegistro
	subcuentas AbridorDeSubcuentas
}

// NuevoRegistradorDePersonas exige pool, derivador, desafios y subcuentas, y rellena los eventos con el nulo.
func NuevoRegistradorDePersonas(pool *pgxpool.Pool, derivador Derivador, eventos RegistradorEventos, desafios CreadorDeDesafioDeRegistro, subcuentas AbridorDeSubcuentas) (*RegistradorDePersonas, error) {
	if pool == nil || derivador == nil || desafios == nil || subcuentas == nil {
		return nil, errors.New("auth: el registro necesita pool, derivador, desafios y subcuentas")
	}
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	return &RegistradorDePersonas{pool: pool, derivador: derivador, eventos: eventos, desafios: desafios, subcuentas: subcuentas}, nil
}

// RegistrarPersona normaliza, deriva la contrasena, crea la cuenta pendiente y encola el OTP de registro.
func (s *RegistradorDePersonas) RegistrarPersona(ctx context.Context, solicitud SolicitudDeRegistro) (Registro, error) {
	entrada, err := normalizarSolicitudDeRegistro(solicitud)
	if err != nil {
		return Registro{}, err
	}
	phc, err := s.derivador.Derivar(ctx, solicitud.Contrasena)
	if err != nil {
		return Registro{}, err
	}
	datos, err := s.persistir(ctx, entrada, phc)
	if err != nil {
		return Registro{}, MapearErrorDeConsulta(err)
	}
	s.abrirSubcuentasEnSegundoPlano(ctx, datos.parteID)
	desafio, err := s.crearDesafioDeRegistro(ctx, datos, entrada)
	if err != nil {
		return Registro{}, err
	}
	return datos.resultado(desafio), nil
}

// persistir escribe parte, persona, credencial, transicion, consentimientos y evento en una sola transaccion.
func (s *RegistradorDePersonas) persistir(ctx context.Context, entrada entradaDeRegistro, phc string) (datosPersistidos, error) {
	return enTransaccion(ctx, poolDeAuth{pool: s.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (datosPersistidos, error) {
			return s.escribirCuenta(ctx, q, entrada, phc)
		})
}

// escribirCuenta inserta las filas del alta y devuelve sus identificadores.
func (s *RegistradorDePersonas) escribirCuenta(ctx context.Context, q *repositorio.Queries, entrada entradaDeRegistro, phc string) (datosPersistidos, error) {
	ahora := time.Now().UTC()
	parteID, credencialID := uuid.New(), uuid.New()
	if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: uuidOPgtype(parteID), Tipo: "persona"}); err != nil {
		return datosPersistidos{}, fmt.Errorf("auth: insertar la parte: %w", err)
	}
	if _, err := q.CrearPersona(ctx, repositorio.CrearPersonaParams{
		ParteID:       uuidOPgtype(parteID),
		TipoDocumento: entrada.tipoDocumento,
		Documento:     entrada.numeroDocumento,
		Nombre:        entrada.nombreCompleto,
	}); err != nil {
		return datosPersistidos{}, fmt.Errorf("auth: insertar la persona: %w", err)
	}
	if _, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
		ID:            uuidOPgtype(credencialID),
		ParteID:       uuidOPgtype(parteID),
		Correo:        entrada.correo,
		Celular:       entrada.celular,
		ValorDerivado: pgtype.Text{String: phc, Valid: true},
		Estado:        string(EstadoPendienteVerificacion),
	}); err != nil {
		return datosPersistidos{}, fmt.Errorf("auth: insertar la credencial: %w", err)
	}
	if err := RegistrarTransicion(ctx, q, credencialID, "", EstadoPendienteVerificacion, motivoRegistro); err != nil {
		return datosPersistidos{}, err
	}
	if err := s.registrarConsentimientos(ctx, q, parteID, entrada.consentimientos, entrada.origen, entrada.userAgent, ahora); err != nil {
		return datosPersistidos{}, err
	}
	if err := s.registrarEventoDeRegistro(ctx, q, parteID, credencialID, entrada, ahora); err != nil {
		return datosPersistidos{}, err
	}
	return datosPersistidos{
		parteID:        parteID,
		credencialID:   credencialID,
		correo:         entrada.correo,
		celular:        entrada.celular,
		nombreCompleto: entrada.nombreCompleto,
		creadaEn:       ahora,
	}, nil
}

// registrarConsentimientos copia el texto vigente del servidor y persiste cada consentimiento declarado.
func (s *RegistradorDePersonas) registrarConsentimientos(ctx context.Context, q *repositorio.Queries, parteID uuid.UUID, consentimientos []consentimientoResuelto, origen, userAgent string, ahora time.Time) error {
	for _, consentimiento := range consentimientos {
		texto, err := q.TextoDeConsentimiento(ctx, repositorio.TextoDeConsentimientoParams{Tipo: consentimiento.tipo, Version: consentimiento.versionTexto})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConsentimientoRequerido
		}
		if err != nil {
			return fmt.Errorf("auth: leer el texto del consentimiento %q: %w", consentimiento.tipo, err)
		}
		accion := AccionConsentimientoRevocar
		if consentimiento.aceptado {
			accion = AccionConsentimientoOtorgar
		}
		if _, err := q.CrearConsentimiento(ctx, repositorio.CrearConsentimientoParams{
			ID:           uuidOPgtype(uuid.New()),
			ParteID:      uuidOPgtype(parteID),
			Tipo:         consentimiento.tipo,
			VersionTexto: texto.Version,
			Texto:        texto.Texto,
			Aceptado:     consentimiento.aceptado,
			Accion:       accion,
			Actor:        uuidOPgtype(parteID),
			Origen:       origen,
			UserAgent:    userAgent,
			Metodo:       metodoRegistro,
			OcurridoEn:   pgtype.Timestamptz{Time: ahora, Valid: true},
		}); err != nil {
			return fmt.Errorf("auth: insertar el consentimiento %q: %w", consentimiento.tipo, err)
		}
	}
	return nil
}

// registrarEventoDeRegistro publica el evento de alta sin contrasena ni valor derivado.
func (s *RegistradorDePersonas) registrarEventoDeRegistro(ctx context.Context, q *repositorio.Queries, parteID, credencialID uuid.UUID, entrada entradaDeRegistro, ahora time.Time) error {
	return registrarEvento(ctx, s.eventos, q, Evento{
		Tipo:         TipoEventoRegistro,
		Actor:        parteID,
		CredencialID: credencialID,
		Resultado:    resultadoRegistroCreado,
		Fecha:        ahora,
		Origen:       entrada.origen,
		Contexto:     motivoRegistro,
	})
}

// abrirSubcuentasEnSegundoPlano intenta el ledger y encola la compensacion si falla, nunca en silencio.
func (s *RegistradorDePersonas) abrirSubcuentasEnSegundoPlano(ctx context.Context, parteID uuid.UUID) {
	if _, err := s.subcuentas.AbrirSubcuentasDeUsuario(ctx, parteID.String()); err == nil {
		return
	} else if errEncolar := s.encolarAperturaDeSubcuentas(ctx, parteID); errEncolar != nil {
		slog.ErrorContext(ctx, "no se pudo abrir ni encolar las subcuentas del registro", "err", err, "err_encolar", errEncolar)
	}
}

// encolarAperturaDeSubcuentas deja el aviso interno en auth.outbox para reintentar la apertura.
func (s *RegistradorDePersonas) encolarAperturaDeSubcuentas(ctx context.Context, parteID uuid.UUID) error {
	_, err := enTransaccion(ctx, poolDeAuth{pool: s.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, EncolarAviso(ctx, q, Aviso{Tipo: TipoAvisoAbrirSubcuentas, ParteID: parteID.String()})
		})
	return err
}

// crearDesafioDeRegistro crea el desafio racionado con el correo ya normalizado.
func (s *RegistradorDePersonas) crearDesafioDeRegistro(ctx context.Context, datos datosPersistidos, entrada entradaDeRegistro) (Desafio, error) {
	return s.desafios.CrearDesafioRacionado(ctx, Solicitud{
		ParteID:           datos.parteID,
		CredencialID:      datos.credencialID,
		Proposito:         PropositoRegistro,
		Celular:           datos.celular,
		Origen:            entrada.origen,
		CorreoNormalizado: datos.correo,
	})
}

// ManejadorAbrirSubcuentas adapta el metodo publico del nucleo al handler del outbox.
func ManejadorAbrirSubcuentas(abridor AbridorDeSubcuentas) ManejadorDeAviso {
	return func(ctx context.Context, _ string, payload []byte) (string, error) {
		var datos payloadAviso
		if err := json.Unmarshal(payload, &datos); err != nil {
			return "", fmt.Errorf("auth: leer el payload de abrir_subcuentas: %w", err)
		}
		if datos.ParteID == "" {
			return "", errors.New("auth: el aviso abrir_subcuentas no trae parte_id")
		}
		if _, err := abridor.AbrirSubcuentasDeUsuario(ctx, datos.ParteID); err != nil {
			return "", fmt.Errorf("auth: abrir las subcuentas de %s: %w", datos.ParteID, err)
		}
		return "", nil
	}
}

// consentimientoResuelto es un consentimiento del request ya deduplicado por tipo.
type consentimientoResuelto struct {
	tipo         string
	versionTexto string
	aceptado     bool
}

// entradaDeRegistro reúne los datos ya normalizados y validados del alta.
type entradaDeRegistro struct {
	correo          string
	celular         string
	nombreCompleto  string
	tipoDocumento   string
	numeroDocumento string
	origen          string
	userAgent       string
	consentimientos []consentimientoResuelto
}

// datosPersistidos es lo que la transaccion deja listo para el desafio y la respuesta.
type datosPersistidos struct {
	parteID        uuid.UUID
	credencialID   uuid.UUID
	correo         string
	celular        string
	nombreCompleto string
	creadaEn       time.Time
}

// resultado arma el Registro de respuesta con el desafio ya emitido.
func (d datosPersistidos) resultado(desafio Desafio) Registro {
	return Registro{
		ParteID:        d.parteID,
		CredencialID:   d.credencialID,
		Correo:         d.correo,
		Celular:        d.celular,
		NombreCompleto: d.nombreCompleto,
		Estado:         EstadoPendienteVerificacion,
		FechaCreacion:  d.creadaEn,
		Desafio:        desafio,
	}
}

// normalizarSolicitudDeRegistro normaliza correo, celular y nombre, y exige los consentimientos obligatorios.
func normalizarSolicitudDeRegistro(solicitud SolicitudDeRegistro) (entradaDeRegistro, error) {
	celular, err := NormalizarCelular(solicitud.Celular)
	if err != nil {
		return entradaDeRegistro{}, err
	}
	nombre, err := NormalizarNombreCompleto(solicitud.NombreCompleto)
	if err != nil {
		return entradaDeRegistro{}, err
	}
	consentimientos, err := normalizarConsentimientos(solicitud.Consentimientos)
	if err != nil {
		return entradaDeRegistro{}, err
	}
	return entradaDeRegistro{
		correo:          NormalizarCorreo(solicitud.Correo),
		celular:         celular,
		nombreCompleto:  nombre,
		tipoDocumento:   solicitud.TipoDocumento,
		numeroDocumento: solicitud.NumeroDocumento,
		origen:          solicitud.Origen,
		userAgent:       solicitud.UserAgent,
		consentimientos: consentimientos,
	}, nil
}

// normalizarConsentimientos deduplica por tipo y exige aceptados tratamiento de datos y WhatsApp.
func normalizarConsentimientos(entrantes []SolicitudDeConsentimiento) ([]consentimientoResuelto, error) {
	porTipo, orden := map[string]SolicitudDeConsentimiento{}, make([]string, 0, len(entrantes))
	for _, consentimiento := range entrantes {
		if _, visto := porTipo[consentimiento.Tipo]; !visto {
			orden = append(orden, consentimiento.Tipo)
		}
		porTipo[consentimiento.Tipo] = consentimiento
	}
	for _, obligatorio := range []string{TipoConsentimientoTratamiento, TipoConsentimientoWhatsApp} {
		consentimiento, presente := porTipo[obligatorio]
		if !presente || !consentimiento.Aceptado {
			return nil, ErrConsentimientoRequerido
		}
	}
	resueltos := make([]consentimientoResuelto, 0, len(orden))
	for _, tipo := range orden {
		consentimiento := porTipo[tipo]
		if consentimiento.VersionTexto == "" {
			return nil, ErrConsentimientoRequerido
		}
		resueltos = append(resueltos, consentimientoResuelto{tipo: tipo, versionTexto: consentimiento.VersionTexto, aceptado: consentimiento.Aceptado})
	}
	return resueltos, nil
}
