package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// resultadoRecuperacionEnviada marca la solicitud con un desafío real entregado.
	resultadoRecuperacionEnviada = "solicitud_enviada"
	// resultadoRecuperacionRelleno marca la solicitud sin cuenta elegible.
	resultadoRecuperacionRelleno = "solicitud_relleno"
	// resultadoRecuperacionEntregaFallida marca el desafío real que el canal no entregó.
	resultadoRecuperacionEntregaFallida = "entrega_fallida"
	// celularDeRelleno es el destino inerte de la fila de relleno, que nunca recibe envío.
	celularDeRelleno = "sin-destino"
)

// RecuperadorDeDesafios crea el desafío real de recuperación con el racionamiento ya consumido.
type RecuperadorDeDesafios interface {
	CrearDesafio(ctx context.Context, solicitud Solicitud) (Desafio, error)
}

// InsertadorDeRelleno persiste la fila de relleno y su evento sin enviar nada.
type InsertadorDeRelleno interface {
	InsertarRelleno(ctx context.Context, correoNormalizado, origen string) (Desafio, error)
}

// ServicioDeRecuperacion raciona, busca la cuenta y responde igual exista o no.
type ServicioDeRecuperacion struct {
	repo       RepositorioCredenciales
	desafios   RecuperadorDeDesafios
	relleno    InsertadorDeRelleno
	eventos    RegistradorEventos
	racionador *RacionadorOTP
	claves     *Claves
}

// NuevoServicioDeRecuperacion construye el servicio sobre el pool real, el gestor y Redis.
func NuevoServicioDeRecuperacion(pool *pgxpool.Pool, desafios RecuperadorDeDesafios, eventos RegistradorEventos, contadores Contadores, claves *Claves, pepper []byte) (*ServicioDeRecuperacion, error) {
	if pool == nil {
		return nil, errors.New("auth: el servicio de recuperación necesita el pool")
	}
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	return nuevoServicioDeRecuperacion(
		&repositorioDeCredenciales{pool: poolDeAuth{pool: pool}},
		&rellenoPostgres{abridor: poolDeAuth{pool: pool}, eventos: eventos, claves: claves, pepper: pepper},
		desafios, eventos, contadores, claves, pepper,
	)
}

// nuevoServicioDeRecuperacion valida los puertos y compone el racionador de A2-03.
func nuevoServicioDeRecuperacion(repo RepositorioCredenciales, relleno InsertadorDeRelleno, desafios RecuperadorDeDesafios, eventos RegistradorEventos, contadores Contadores, claves *Claves, pepper []byte) (*ServicioDeRecuperacion, error) {
	if repo == nil || desafios == nil || relleno == nil || contadores == nil || claves == nil {
		return nil, errors.New("auth: la recuperación necesita repositorio, desafíos, relleno, contadores y claves")
	}
	if len(pepper) < 32 {
		return nil, fmt.Errorf("auth: OTP_PEPPER debe tener al menos 32 bytes, tiene %d", len(pepper))
	}
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	racionador, err := NuevoRacionadorOTP(contadores, claves)
	if err != nil {
		return nil, err
	}
	return &ServicioDeRecuperacion{repo: repo, desafios: desafios, relleno: relleno, eventos: eventos, racionador: racionador, claves: claves}, nil
}

// SolicitarRecuperacion raciona antes de leer la base y responde con un desafío real o de relleno.
func (s *ServicioDeRecuperacion) SolicitarRecuperacion(ctx context.Context, correo, origen string) (Desafio, error) {
	correoNormalizado := NormalizarCorreo(correo)
	if err := s.racionador.Exigir(ctx, correoNormalizado, origen); err != nil {
		return Desafio{}, err
	}
	credencial, existe, err := s.repo.PorCorreo(ctx, correoNormalizado)
	if err != nil {
		return Desafio{}, fmt.Errorf("auth: buscar la credencial de la recuperación: %w", err)
	}
	if !existe || !elegibleParaRecuperacion(credencial) {
		return s.relleno.InsertarRelleno(ctx, correoNormalizado, origen)
	}
	desafio, err := s.crearDesafioReal(ctx, credencial, correoNormalizado, origen)
	if errors.Is(err, ErrConsentimientoRequerido) {
		return s.relleno.InsertarRelleno(ctx, correoNormalizado, origen)
	}
	if errors.Is(err, ErrEntregaFallida) {
		if fallo := s.registrarRecuperacion(ctx, credencial, correoNormalizado, origen, resultadoRecuperacionEntregaFallida); fallo != nil {
			return Desafio{}, fallo
		}
		return desafio, nil
	}
	if err != nil {
		return Desafio{}, err
	}
	if err := s.registrarRecuperacion(ctx, credencial, correoNormalizado, origen, resultadoRecuperacionEnviada); err != nil {
		return Desafio{}, err
	}
	return desafio, nil
}

// elegibleParaRecuperacion exige cuenta activa y celular verificado, el ancla de H3.
func elegibleParaRecuperacion(c repositorio.Credencial) bool {
	return EstadoCredencial(c.Estado) == EstadoActivo && c.CelularVerificadoEn.Valid
}

// crearDesafioReal arma la solicitud con la credencial elegible y delega en A2-06.1.
func (s *ServicioDeRecuperacion) crearDesafioReal(ctx context.Context, c repositorio.Credencial, correoNormalizado, origen string) (Desafio, error) {
	return s.desafios.CrearDesafio(ctx, Solicitud{
		ParteID:           uuidDePgtype(c.ParteID),
		CredencialID:      uuidDePgtype(c.ID),
		Proposito:         PropositoRecuperacion,
		Celular:           c.Celular,
		Origen:            origen,
		CorreoNormalizado: correoNormalizado,
	})
}

// registrarRecuperacion publica el evento recuperación con el HMAC del correo en el contexto.
func (s *ServicioDeRecuperacion) registrarRecuperacion(ctx context.Context, c repositorio.Credencial, correoNormalizado, origen, resultado string) error {
	evento := Evento{
		Tipo:         TipoEventoRecuperacion,
		Actor:        uuidDePgtype(c.ParteID),
		CredencialID: uuidDePgtype(c.ID),
		Resultado:    resultado,
		Fecha:        time.Now().UTC(),
		Origen:       origen,
		Contexto:     s.claves.ClaveDeCorreo(correoNormalizado),
	}
	if err := s.eventos.Registrar(ctx, evento); err != nil {
		return fmt.Errorf("auth: registrar la solicitud de recuperación: %w", err)
	}
	return nil
}

// rellenoPostgres inserta la fila de relleno en auth.desafios con parte y credencial nulas.
type rellenoPostgres struct {
	abridor abridorDeTransaccionesAuth
	eventos RegistradorEventos
	claves  *Claves
	pepper  []byte
}

// InsertarRelleno persiste la fila y el evento en una sola transacción, sin tocar el canal.
func (r *rellenoPostgres) InsertarRelleno(ctx context.Context, correoNormalizado, origen string) (Desafio, error) {
	ahora := time.Now().UTC()
	var fila repositorio.AuthDesafio
	_, err := enTransaccion(ctx, r.abridor, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
		hmac, err := hmacDeRelleno(r.pepper)
		if err != nil {
			return struct{}{}, err
		}
		fila, err = q.InsertarDesafio(ctx, repositorio.InsertarDesafioParams{
			ID:                 pgtype.UUID{Bytes: uuid.New(), Valid: true},
			Proposito:          string(PropositoRecuperacion),
			CelularDestino:     celularDeRelleno,
			CodigoHmac:         hmac,
			CreadoEn:           pgtype.Timestamptz{Time: ahora, Valid: true},
			ExpiraEn:           pgtype.Timestamptz{Time: ahora.Add(vigenciaDesafio), Valid: true},
			UltimoEnvioEn:      pgtype.Timestamptz{Time: ahora, Valid: true},
			ContextoSolicitado: pgtype.Text{},
		})
		if err != nil {
			return struct{}{}, fmt.Errorf("auth: insertar el relleno del desafío: %w", err)
		}
		evento := Evento{
			Tipo:      TipoEventoRecuperacion,
			Resultado: resultadoRecuperacionRelleno,
			Fecha:     ahora,
			Origen:    origen,
			Contexto:  r.claves.ClaveDeCorreo(correoNormalizado),
		}
		return struct{}{}, registrarEvento(ctx, r.eventos, q, evento)
	})
	if err != nil {
		return Desafio{}, err
	}
	return Desafio{ID: uuidDePgtype(fila.ID), ExpiraEnSegundos: int(vigenciaDesafio.Seconds()), Proposito: PropositoRecuperacion}, nil
}

// hmacDeRelleno deriva 32 bytes de crypto/rand: ningún código de seis dígitos lo satisface.
func hmacDeRelleno(pepper []byte) ([]byte, error) {
	semilla := make([]byte, 32)
	if _, err := rand.Read(semilla); err != nil {
		return nil, fmt.Errorf("auth: generar el relleno del desafío: %w", err)
	}
	return hmacDeCodigo(pepper, string(semilla)), nil
}
