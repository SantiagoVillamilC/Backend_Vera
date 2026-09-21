package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// bytesResetToken es la entropia del token de restablecimiento antes de codificar.
	bytesResetToken = 32
	// resultadoSegundoFactorVerificado marca el segundo metodo correcto.
	resultadoSegundoFactorVerificado = "segundo_factor_verificado"
	// resultadoSegundoFactorInvalido marca el codigo de respaldo que no coincide.
	resultadoSegundoFactorInvalido = "segundo_factor_invalido"
	// resultadoContrasenaRestablecida marca el cambio de contrasena aplicado.
	resultadoContrasenaRestablecida = "contrasena_restablecida"
	// resultadoDenylistFallida marca los sid que no se pudieron publicar.
	resultadoDenylistFallida = "denylist_fallida"
	// plantillaContrasenaCambiada es el marcador de plantilla del aviso, pendiente de copy real.
	plantillaContrasenaCambiada = "contrasena_cambiada"
)

// ServicioDeRestablecimiento cierra la recuperacion: segundo metodo, contrasena nueva y aviso.
type ServicioDeRestablecimiento struct {
	pool       abridorDeTransaccionesAuth
	desafios   *GestorDesafios
	sesiones   *GestorSesiones
	derivador  Derivador
	denylist   RevocadorDeSesiones
	eventos    RegistradorEventos
	optIn      LectorOptIn
	racionador *RacionadorOTP
	pepper     []byte
	ttl        time.Duration
}

// NuevoServicioDeRestablecimiento compone el cierre de la recuperacion sobre el pool real.
func NuevoServicioDeRestablecimiento(pool *pgxpool.Pool, desafios *GestorDesafios, sesiones *GestorSesiones, derivador Derivador, denylist RevocadorDeSesiones, eventos RegistradorEventos, optIn LectorOptIn, contadores Contadores, claves *Claves, pepper []byte, ttl time.Duration) (*ServicioDeRestablecimiento, error) {
	if pool == nil || desafios == nil || sesiones == nil || derivador == nil || contadores == nil || claves == nil {
		return nil, errors.New("auth: el restablecimiento necesita pool, desafios, sesiones, derivador, contadores y claves")
	}
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	if optIn == nil {
		optIn = SiempreOptIn{}
	}
	if len(pepper) < 32 {
		return nil, fmt.Errorf("auth: OTP_PEPPER debe tener al menos 32 bytes, tiene %d", len(pepper))
	}
	if ttl <= 0 {
		return nil, errors.New("auth: la vida del token de restablecimiento debe ser positiva")
	}
	racionador, err := NuevoRacionadorOTP(contadores, claves)
	if err != nil {
		return nil, err
	}
	return &ServicioDeRestablecimiento{pool: poolDeAuth{pool: pool}, desafios: desafios, sesiones: sesiones, derivador: derivador, denylist: denylist, eventos: eventos, optIn: optIn, racionador: racionador, pepper: pepper, ttl: ttl}, nil
}

// VerificarSegundoFactor verifica el OTP de recuperacion, consume un codigo de respaldo y entrega el reset_token.
func (s *ServicioDeRestablecimiento) VerificarSegundoFactor(ctx context.Context, id uuid.UUID, codigo, codigoRespaldo, origen string) (string, error) {
	var (
		resetToken string
		dominio    error
	)
	_, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		resultado, err := s.desafios.VerificarDesafioEn(ctx, q, id, codigo, PropositoRecuperacion)
		if err != nil {
			dominio = err
			return struct{}{}, nil //nolint:nilerr // el nil confirma la transacción y el error de dominio se devuelve tras el commit
		}
		errConsumo := ConsumirCodigoRespaldo(ctx, q, s.pepper, resultado.CredencialID, codigoRespaldo)
		if errConsumo != nil && !errors.Is(errConsumo, ErrSegundoFactorRequerido) {
			return struct{}{}, errConsumo
		}
		if errConsumo != nil {
			dominio = s.registrarSegundoFactorInvalido(ctx, q, id, resultado, origen)
			return struct{}{}, nil //nolint:nilerr // el nil confirma la transacción y el error de dominio se devuelve tras el commit
		}
		resetToken, err = s.emitirTokenEn(ctx, q, resultado, origen)
		return struct{}{}, err
	})
	if err != nil {
		return "", err
	}
	if dominio != nil {
		return "", dominio
	}
	return resetToken, nil
}

// RestablecerContrasena consume el reset_token, fija la contrasena, revoca las sesiones y avisa.
func (s *ServicioDeRestablecimiento) RestablecerContrasena(ctx context.Context, resetToken, nuevaContrasena string) error {
	hash := hashDeResetToken(resetToken)
	parteID, err := s.parteDelToken(ctx, hash)
	if err != nil {
		return err
	}
	conWhatsApp, err := s.optIn.OptInVigente(ctx, parteID)
	if err != nil {
		return fmt.Errorf("auth: consultar el opt-in del aviso: %w", err)
	}
	phc, err := s.derivador.Derivar(ctx, nuevaContrasena)
	if err != nil {
		return err
	}
	var familias []uuid.UUID
	_, err = enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		credencialID, err := q.ConsumirTokenRestablecimiento(ctx, hash)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return struct{}{}, ErrResetInvalido
			}
			return struct{}{}, fmt.Errorf("auth: consumir el token de restablecimiento: %w", err)
		}
		filas, err := q.ActualizarContrasena(ctx, repositorio.ActualizarContrasenaParams{
			ValorDerivado: pgtype.Text{String: phc, Valid: true},
			ID:            credencialID,
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: fijar la contrasena nueva: %w", err)
		}
		if filas == 0 {
			return struct{}{}, ErrResetInvalido
		}
		credencial, err := q.CredencialPorID(ctx, credencialID)
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: leer la credencial restablecida: %w", err)
		}
		if err := s.encolarAvisos(ctx, q, credencial, conWhatsApp); err != nil {
			return struct{}{}, err
		}
		familiasPg, err := q.FamiliasVigentesDeParte(ctx, credencial.ParteID)
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: listar las familias vigentes: %w", err)
		}
		familias = familiasDePgtype(familiasPg)
		return struct{}{}, registrarEvento(ctx, s.eventos, q, Evento{
			Tipo:         TipoEventoRecuperacion,
			Actor:        uuidDePgtype(credencial.ParteID),
			CredencialID: uuidDePgtype(credencial.ID),
			Resultado:    resultadoContrasenaRestablecida,
			Fecha:        time.Now().UTC(),
			Detalle:      map[string]string{"motivo": motivoAviso(conWhatsApp)},
		})
	})
	if err != nil {
		return err
	}
	if err := s.sesiones.RevocarTodas(ctx, parteID); err != nil {
		return err
	}
	s.publicarDenylist(ctx, familias)
	return nil
}

// registrarSegundoFactorInvalido invalida el desafio, cuenta el fallo y publica el evento del segundo factor malo.
func (s *ServicioDeRestablecimiento) registrarSegundoFactorInvalido(ctx context.Context, q *repositorio.Queries, id uuid.UUID, resultado ResultadoDesafio, origen string) error {
	if _, err := q.InvalidarDesafioNoPendiente(ctx, uuidOPgtype(id)); err != nil {
		return fmt.Errorf("auth: invalidar el desafio del segundo factor: %w", err)
	}
	if err := s.registrarFalloDeCuenta(ctx, q, resultado.CredencialID); err != nil {
		return err
	}
	if err := registrarEvento(ctx, s.eventos, q, Evento{
		Tipo:         TipoEventoRecuperacion,
		Actor:        resultado.ParteID,
		CredencialID: resultado.CredencialID,
		Resultado:    resultadoSegundoFactorInvalido,
		Fecha:        time.Now().UTC(),
		Origen:       origen,
	}); err != nil {
		return err
	}
	return ErrSegundoFactorRequerido
}

// registrarFalloDeCuenta acumula el codigo de respaldo malo en el contador de fallos de la cuenta.
func (s *ServicioDeRestablecimiento) registrarFalloDeCuenta(ctx context.Context, q *repositorio.Queries, credencialID uuid.UUID) error {
	credencial, err := q.CredencialPorID(ctx, uuidOPgtype(credencialID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth: leer la credencial del fallo: %w", err)
	}
	return s.racionador.RegistrarFallo(ctx, credencial.Correo)
}

// emitirTokenEn inserta el token de restablecimiento y publica el evento del segundo factor verificado.
func (s *ServicioDeRestablecimiento) emitirTokenEn(ctx context.Context, q *repositorio.Queries, resultado ResultadoDesafio, origen string) (string, error) {
	claro, hash, err := nuevoTokenDeRestablecimiento()
	if err != nil {
		return "", err
	}
	ahora := time.Now().UTC()
	if err := q.InsertarTokenRestablecimiento(ctx, repositorio.InsertarTokenRestablecimientoParams{
		ID:           uuidOPgtype(uuid.New()),
		CredencialID: uuidOPgtype(resultado.CredencialID),
		TokenHash:    hash,
		ExpiraEn:     pgtype.Timestamptz{Time: ahora.Add(s.ttl), Valid: true},
	}); err != nil {
		return "", fmt.Errorf("auth: insertar el token de restablecimiento: %w", err)
	}
	if err := registrarEvento(ctx, s.eventos, q, Evento{
		Tipo:         TipoEventoRecuperacion,
		Actor:        resultado.ParteID,
		CredencialID: resultado.CredencialID,
		Resultado:    resultadoSegundoFactorVerificado,
		Fecha:        ahora,
		Origen:       origen,
	}); err != nil {
		return "", err
	}
	return claro, nil
}

// parteDelToken lee la parte duena del token vigente para poder consultar su opt-in fuera de la transaccion.
func (s *ServicioDeRestablecimiento) parteDelToken(ctx context.Context, hash []byte) (uuid.UUID, error) {
	fila, err := enTransaccion(ctx, s.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.TokenRestablecimientoVigenteRow, error) {
		return q.TokenRestablecimientoVigente(ctx, hash)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrResetInvalido
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("auth: leer el token de restablecimiento: %w", err)
	}
	return uuidDePgtype(fila.ParteID), nil
}

// encolarAvisos encola el aviso por correo y, con opt-in vigente, tambien por WhatsApp.
func (s *ServicioDeRestablecimiento) encolarAvisos(ctx context.Context, q *repositorio.Queries, credencial repositorio.Credencial, conWhatsApp bool) error {
	if err := EncolarAviso(ctx, q, Aviso{
		Tipo:      TipoAvisoContrasenaCambiada,
		Destino:   DestinoCorreo,
		Correo:    credencial.Correo,
		Plantilla: plantillaContrasenaCambiada,
	}); err != nil {
		return err
	}
	if !conWhatsApp || !credencial.CelularVerificadoEn.Valid || credencial.Celular == "" {
		return nil
	}
	return EncolarAviso(ctx, q, Aviso{
		Tipo:      TipoAvisoContrasenaCambiada,
		Destino:   DestinoWhatsApp,
		Celular:   credencial.Celular,
		Plantilla: plantillaContrasenaCambiada,
	})
}

// publicarDenylist publica cada sid revocado y deja el evento cuando Redis falla.
func (s *ServicioDeRestablecimiento) publicarDenylist(ctx context.Context, familias []uuid.UUID) {
	if s.denylist == nil || len(familias) == 0 {
		return
	}
	var fallidas []string
	for _, familia := range familias {
		if err := s.denylist.Revocar(ctx, familia, duracionAccess); err != nil {
			slog.ErrorContext(ctx, "no se pudo publicar el sid del reset en la denylist", "sid", familia.String(), "err", err)
			fallidas = append(fallidas, familia.String())
		}
	}
	if len(fallidas) == 0 {
		return
	}
	if err := s.eventos.Registrar(ctx, Evento{
		Tipo:      TipoEventoRecuperacion,
		Resultado: resultadoDenylistFallida,
		Fecha:     time.Now().UTC(),
		Detalle:   map[string]string{"motivo": "sid:" + strings.Join(fallidas, ",")},
	}); err != nil {
		slog.ErrorContext(ctx, "no se pudo registrar el evento denylist_fallida", "err", err)
	}
}

// nuevoTokenDeRestablecimiento genera 32 bytes opacos y devuelve el token en claro con su SHA-256.
func nuevoTokenDeRestablecimiento() (string, []byte, error) {
	bruto := make([]byte, bytesResetToken)
	if _, err := rand.Read(bruto); err != nil {
		return "", nil, fmt.Errorf("auth: generar el token de restablecimiento: %w", err)
	}
	claro := base64.RawURLEncoding.EncodeToString(bruto)
	suma := sha256.Sum256([]byte(claro))
	return claro, suma[:], nil
}

// hashDeResetToken calcula el SHA-256 del token en claro que presenta el cliente.
func hashDeResetToken(claro string) []byte {
	suma := sha256.Sum256([]byte(claro))
	return suma[:]
}

// familiasDePgtype convierte las familias del repositorio al uuid de Go.
func familiasDePgtype(filas []pgtype.UUID) []uuid.UUID {
	familias := make([]uuid.UUID, 0, len(filas))
	for _, fila := range filas {
		familias = append(familias, uuidDePgtype(fila))
	}
	return familias
}

// motivoAviso deja en el evento por que canales salio el aviso de contrasena cambiada.
func motivoAviso(conWhatsApp bool) string {
	if conWhatsApp {
		return "aviso_correo_whatsapp"
	}
	return "aviso_correo"
}
