package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// motivoCelularVerificado es el motivo de la transicion de activacion.
const motivoCelularVerificado = "celular_verificado"

// Activacion es la cuenta ya activa con los codigos de respaldo que se muestran una sola vez.
type Activacion struct {
	ParteID         uuid.UUID
	Celular         string
	CodigosRespaldo []string
}

// ActivadorDeCuenta cierra el registro: verifica el OTP, activa la credencial y emite los codigos de respaldo.
type ActivadorDeCuenta struct {
	pool     *pgxpool.Pool
	desafios *GestorDesafios
	estados  *GestorDeEstados
	pepper   []byte
}

// NuevoActivadorDeCuenta exige pool, gestor de desafios, gestor de estados y OTP_PEPPER.
func NuevoActivadorDeCuenta(pool *pgxpool.Pool, desafios *GestorDesafios, estados *GestorDeEstados, pepper []byte) (*ActivadorDeCuenta, error) {
	if pool == nil || desafios == nil || estados == nil || len(pepper) < 32 {
		return nil, errors.New("auth: el activador necesita pool, desafios, estados y OTP_PEPPER")
	}
	return &ActivadorDeCuenta{pool: pool, desafios: desafios, estados: estados, pepper: pepper}, nil
}

// ActivarCuenta resuelve el desafio de registro por celular y activa con compare-and-set en una sola transaccion.
func (s *ActivadorDeCuenta) ActivarCuenta(ctx context.Context, celular, codigo string) (Activacion, error) {
	celular = strings.TrimSpace(celular)
	var activacion Activacion
	var dominio error
	if _, err := enTransaccion(ctx, poolDeAuth{pool: s.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			fila, err := q.UltimoDesafioDeRegistroPorCelular(ctx, celular)
			if errors.Is(err, pgx.ErrNoRows) {
				dominio = ErrDesafioInvalido
				return struct{}{}, nil
			}
			if err != nil {
				return struct{}{}, fmt.Errorf("auth: resolver el desafio de registro por celular: %w", err)
			}
			resultado, err := s.desafios.VerificarDesafioEn(ctx, q, uuidDePgtype(fila.ID), codigo, PropositoRegistro)
			if err != nil {
				dominio = err
				return struct{}{}, nil
			}
			return s.activarConCodigos(ctx, q, resultado, &activacion)
		}); err != nil {
		return Activacion{}, err
	}
	return activacion, dominio
}

// activarConCodigos aplica el compare-and-set, sella el celular y emite los codigos en la misma transaccion.
func (s *ActivadorDeCuenta) activarConCodigos(ctx context.Context, q *repositorio.Queries, resultado ResultadoDesafio, activacion *Activacion) (struct{}, error) {
	if err := s.estados.ActivarCredencial(ctx, q, resultado.CredencialID, motivoCelularVerificado); err != nil {
		return struct{}{}, err
	}
	credencial, err := q.CredencialPorID(ctx, uuidOPgtype(resultado.CredencialID))
	if err != nil {
		return struct{}{}, fmt.Errorf("auth: leer la credencial activada: %w", err)
	}
	codigos, err := s.emitirCodigosRespaldo(ctx, q, resultado.CredencialID)
	if err != nil {
		return struct{}{}, err
	}
	*activacion = Activacion{ParteID: resultado.ParteID, Celular: credencial.Celular, CodigosRespaldo: codigos}
	return struct{}{}, nil
}

// emitirCodigosRespaldo genera y persiste el HMAC de cada codigo sin guardarlo en claro.
func (s *ActivadorDeCuenta) emitirCodigosRespaldo(ctx context.Context, q *repositorio.Queries, credencialID uuid.UUID) ([]string, error) {
	codigos, err := generarCodigosRespaldo(cantidadCodigosRespaldo)
	if err != nil {
		return nil, err
	}
	for _, codigo := range codigos {
		if err := q.CrearCodigoRespaldo(ctx, repositorio.CrearCodigoRespaldoParams{
			ID:           uuidOPgtype(uuid.New()),
			CredencialID: uuidOPgtype(credencialID),
			CodigoHmac:   hmacDeCodigoRespaldo(s.pepper, codigo),
		}); err != nil {
			return nil, fmt.Errorf("auth: insertar el codigo de respaldo: %w", err)
		}
	}
	return codigos, nil
}
