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

const (
	// resultadoLoginOk marca el evento del inicio de sesión autenticado.
	resultadoLoginOk = "ok"
	// resultadoLoginFallido marca el evento del intento rechazado.
	resultadoLoginFallido = "fallido"
	// rellenoContrasena es la contraseña que se verifica contra el PHC de relleno.
	rellenoContrasena = "centinela-relleno-login"
)

// CredencialAutenticada es la credencial validada que A2-07.2 convierte en sesión o desafío.
type CredencialAutenticada struct {
	CredencialID      uuid.UUID
	ParteID           uuid.UUID
	Correo            string
	Celular           string
	Estado            EstadoCredencial
	CelularVerificado bool
}

// RepositorioCredenciales es lo único que el login necesita del almacén: buscar y rehashear.
type RepositorioCredenciales interface {
	PorCorreo(ctx context.Context, correoNormalizado string) (repositorio.Credencial, bool, error)
	PorCelular(ctx context.Context, celular string) (repositorio.Credencial, bool, error)
	Rehash(ctx context.Context, id pgtype.UUID, viejo pgtype.Text, nuevo string) (int64, error)
}

// VerificadorDeCredenciales valida celular y contraseña sin revelar si la cuenta existe.
type VerificadorDeCredenciales struct {
	repo       RepositorioCredenciales
	derivador  Derivador
	contadores Contadores
	claves     *Claves
	eventos    RegistradorEventos
	relleno    string
	parametros ParametrosArgon2
}

// NuevoVerificadorDeCredenciales precomputa el PHC de relleno y exige el derivador y los contadores.
func NuevoVerificadorDeCredenciales(repo RepositorioCredenciales, derivador Derivador, contadores Contadores, claves *Claves, eventos RegistradorEventos, parametros ParametrosArgon2) (*VerificadorDeCredenciales, error) {
	if repo == nil || derivador == nil || contadores == nil || claves == nil {
		return nil, errors.New("auth: el verificador de credenciales necesita repositorio, derivador, contadores y claves")
	}
	if eventos == nil {
		eventos = RegistradorNulo{}
	}
	relleno, err := derivador.Derivar(context.Background(), rellenoContrasena)
	if err != nil {
		return nil, fmt.Errorf("auth: precomputar el PHC de relleno: %w", err)
	}
	return &VerificadorDeCredenciales{
		repo:       repo,
		derivador:  derivador,
		contadores: contadores,
		claves:     claves,
		eventos:    eventos,
		relleno:    relleno,
		parametros: parametros,
	}, nil
}

// NuevoVerificadorDeCredencialesPostgres arma el repositorio de credenciales sobre el pool real.
func NuevoVerificadorDeCredencialesPostgres(pool *pgxpool.Pool, derivador Derivador, contadores Contadores, claves *Claves, eventos RegistradorEventos, parametros ParametrosArgon2) (*VerificadorDeCredenciales, error) {
	if pool == nil {
		return nil, errors.New("auth: el verificador de credenciales necesita el pool")
	}
	return NuevoVerificadorDeCredenciales(&repositorioDeCredenciales{pool: poolDeAuth{pool: pool}}, derivador, contadores, claves, eventos, parametros)
}

// Autenticar valida la credencial sin enumeración y corta los intentos repetidos antes de Argon2.
func (v *VerificadorDeCredenciales) Autenticar(ctx context.Context, celular, contrasena, origen string) (CredencialAutenticada, error) {
	celularNormalizado := strings.TrimSpace(celular)
	claveCuenta := PrefijoFallosCuenta + v.claves.ClaveDeCelular(celularNormalizado)
	claveOrigen := ""
	if origen != "" {
		claveOrigen = PrefijoLoginOrigen + v.claves.ClaveDeOrigen(origen)
	}
	if err := v.exigirSinBloqueo(ctx, claveCuenta, claveOrigen); err != nil {
		return CredencialAutenticada{}, err
	}

	credencial, existe, err := v.buscarCredencial(ctx, celularNormalizado)
	if err != nil {
		return CredencialAutenticada{}, err
	}
	phc := v.relleno
	if existe && credencial.ValorDerivado.Valid {
		phc = credencial.ValorDerivado.String
	}

	coincide, err := v.derivador.Verificar(ctx, contrasena, phc)
	if err != nil {
		return CredencialAutenticada{}, fmt.Errorf("auth: verificar la contraseña: %w", err)
	}
	if !existe || !coincide {
		return CredencialAutenticada{}, v.fallar(ctx, celularNormalizado, claveCuenta, claveOrigen, origen)
	}

	estado := EstadoCredencial(credencial.Estado)
	if err := v.exigirEstado(estado); err != nil {
		return CredencialAutenticada{}, err
	}
	v.rehashear(ctx, credencial, contrasena)
	resultado := resultadoLoginOk
	if estado == EstadoPendienteVerificacion {
		resultado = string(estado)
	}
	if err := v.registrar(ctx, credencial, origen, resultado); err != nil {
		return CredencialAutenticada{}, err
	}
	return v.resultado(credencial, estado), nil
}

// exigirSinBloqueo rechaza las claves ya bloqueadas sin tocar Argon2 y hace fail closed ante Redis.
func (v *VerificadorDeCredenciales) exigirSinBloqueo(ctx context.Context, claves ...string) error {
	for _, clave := range claves {
		if clave == "" {
			continue
		}
		bloqueada, err := v.contadores.Bloqueada(ctx, clave)
		if err != nil {
			return ConFailClosed(err)
		}
		if bloqueada {
			return ErrDemasiadosEnvios
		}
	}
	return nil
}

// buscarCredencial lee la credencial por celular; la ausencia no es error sino existe falso.
func (v *VerificadorDeCredenciales) buscarCredencial(ctx context.Context, celular string) (repositorio.Credencial, bool, error) {
	credencial, existe, err := v.repo.PorCelular(ctx, celular)
	if err != nil {
		return repositorio.Credencial{}, false, fmt.Errorf("auth: buscar la credencial por celular: %w", err)
	}
	return credencial, existe, nil
}

// exigirEstado resuelve cerrado y sin_login como credenciales inválidas y suspendido como cuenta suspendida.
func (v *VerificadorDeCredenciales) exigirEstado(estado EstadoCredencial) error {
	switch estado {
	case EstadoCerrado, EstadoSinLogin:
		return ErrCredencialesInvalidas
	case EstadoSuspendido:
		return ErrCuentaSuspendida
	default:
		return nil
	}
}

// fallar acumula el fallo de la cuenta y del origen, bloquea con espera creciente al pasar el tope y responde genérico.
func (v *VerificadorDeCredenciales) fallar(ctx context.Context, celularNormalizado, claveCuenta, claveOrigen, origen string) error {
	valor, err := v.contadores.Incrementar(ctx, claveCuenta, ventanaFallos)
	if err != nil {
		return ConFailClosed(err)
	}
	if valor > maxFallosPorCuenta {
		if err := v.contadores.Bloquear(ctx, claveCuenta, esperaCreciente(valor-maxFallosPorCuenta)); err != nil {
			return ConFailClosed(err)
		}
		return ErrDemasiadosEnvios
	}
	if claveOrigen != "" {
		valorOrigen, err := v.contadores.Incrementar(ctx, claveOrigen, ventanaEnvios)
		if err != nil {
			return ConFailClosed(err)
		}
		if valorOrigen > maxEnviosPorOrigen {
			if err := v.contadores.Bloquear(ctx, claveOrigen, esperaCreciente(valorOrigen-maxEnviosPorOrigen)); err != nil {
				return ConFailClosed(err)
			}
			return ErrDemasiadosEnvios
		}
	}
	if err := v.registrarIntento(ctx, celularNormalizado, origen, resultadoLoginFallido); err != nil {
		return err
	}
	return ErrCredencialesInvalidas
}

// registrarIntento publica el evento del intento sin credencial conocida, sin celular en claro ni contraseña.
func (v *VerificadorDeCredenciales) registrarIntento(ctx context.Context, celularNormalizado, origen, resultado string) error {
	if err := v.eventos.Registrar(ctx, Evento{
		Tipo:      TipoEventoInicioSesion,
		Resultado: resultado,
		Fecha:     time.Now().UTC(),
		Origen:    origen,
		Contexto:  v.claves.ClaveDeCelular(celularNormalizado),
	}); err != nil {
		return fmt.Errorf("auth: registrar el intento de inicio de sesión: %w", err)
	}
	return nil
}

// rehashear reemplaza el PHC con los parametros vigentes sin abortar el login ya verificado.
func (v *VerificadorDeCredenciales) rehashear(ctx context.Context, credencial repositorio.Credencial, contrasena string) {
	actual, _, _, err := parsearPHC(credencial.ValorDerivado.String)
	if err != nil {
		return
	}
	if actual.iguales(v.parametros) {
		return
	}
	nuevo, err := v.derivador.Derivar(ctx, contrasena)
	if err != nil {
		return
	}
	_, _ = v.repo.Rehash(ctx, credencial.ID, credencial.ValorDerivado, nuevo)
}

// registrar publica el evento del inicio de sesión sin contraseña ni celular en claro.
func (v *VerificadorDeCredenciales) registrar(ctx context.Context, credencial repositorio.Credencial, origen, resultado string) error {
	if err := v.eventos.Registrar(ctx, Evento{
		Tipo:         TipoEventoInicioSesion,
		Actor:        uuidDePgtype(credencial.ParteID),
		CredencialID: uuidDePgtype(credencial.ID),
		Resultado:    resultado,
		Fecha:        time.Now().UTC(),
		Origen:       origen,
		Contexto:     v.claves.ClaveDeCelular(credencial.Celular),
	}); err != nil {
		return fmt.Errorf("auth: registrar el inicio de sesión: %w", err)
	}
	return nil
}

// resultado arma la credencial autenticada con su estado y la verificación del celular.
func (v *VerificadorDeCredenciales) resultado(credencial repositorio.Credencial, estado EstadoCredencial) CredencialAutenticada {
	return CredencialAutenticada{
		CredencialID:      uuidDePgtype(credencial.ID),
		ParteID:           uuidDePgtype(credencial.ParteID),
		Correo:            credencial.Correo,
		Celular:           credencial.Celular,
		Estado:            estado,
		CelularVerificado: credencial.CelularVerificadoEn.Valid,
	}
}

// iguales compara los parametros de costo que el PHC embebe para decidir el rehash.
func (p ParametrosArgon2) iguales(otro ParametrosArgon2) bool {
	return p.MemoriaKiB == otro.MemoriaKiB &&
		p.Iteraciones == otro.Iteraciones &&
		p.Paralelismo == otro.Paralelismo
}

// repositorioDeCredenciales implementa el puerto del login sobre el pool real de auth.
type repositorioDeCredenciales struct{ pool abridorDeTransaccionesAuth }

// PorCorreo envuelve la consulta sqlc y convierte la ausencia en existe falso.
func (r *repositorioDeCredenciales) PorCorreo(ctx context.Context, correoNormalizado string) (repositorio.Credencial, bool, error) {
	credencial, err := enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.Credencial, error) {
		return q.CredencialPorCorreo(ctx, correoNormalizado)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return repositorio.Credencial{}, false, nil
	}
	if err != nil {
		return repositorio.Credencial{}, false, err
	}
	return credencial, true, nil
}

// PorCelular envuelve la consulta sqlc y convierte la ausencia en existe falso.
func (r *repositorioDeCredenciales) PorCelular(ctx context.Context, celular string) (repositorio.Credencial, bool, error) {
	credencial, err := enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.Credencial, error) {
		return q.CredencialPorCelular(ctx, celular)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return repositorio.Credencial{}, false, nil
	}
	if err != nil {
		return repositorio.Credencial{}, false, err
	}
	return credencial, true, nil
}

// Rehash ejecuta el compare-and-set del valor derivado y devuelve las filas afectadas.
func (r *repositorioDeCredenciales) Rehash(ctx context.Context, id pgtype.UUID, viejo pgtype.Text, nuevo string) (int64, error) {
	return enTransaccion(ctx, r.pool, func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (int64, error) {
		return q.RehashCredencial(ctx, repositorio.RehashCredencialParams{
			ValorDerivado:   pgtype.Text{String: nuevo, Valid: true},
			ID:              id,
			ValorDerivado_2: viejo,
		})
	})
}

var _ RepositorioCredenciales = (*repositorioDeCredenciales)(nil)
