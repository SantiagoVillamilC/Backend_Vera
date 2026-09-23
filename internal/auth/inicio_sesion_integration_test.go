//go:build integration

package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// servicioDeInicioSesionDePrueba arma el servicio de login sobre Postgres real con el firmador.
func servicioDeInicioSesionDePrueba(tb testing.TB, pool *pgxpool.Pool, derivador Derivador) *ServicioDeLogin {
	tb.Helper()
	eventos := &registradorEventosDeLogin{}
	verificador, _ := verificadorDeLogin(tb, pool, derivador, eventos)
	tokens, err := NuevoTokensDeAcceso([]byte("clave-de-firma-de-al-menos-32-bytes"), "centinela-auth", "centinela-api")
	if err != nil {
		tb.Fatalf("tokens de acceso: %v", err)
	}
	servicio, err := NuevoServicioDeLogin(verificador, pool, NuevoGestorSesiones(pool, eventos), tokens)
	if err != nil {
		tb.Fatalf("servicio de login: %v", err)
	}
	return servicio
}

// sembrarCredencialDeInicioSesion crea la parte y su credencial y marca el celular si se pide.
func sembrarCredencialDeInicioSesion(tb testing.TB, pool *pgxpool.Pool, derivador Derivador, contrasena string, estado EstadoCredencial, verificado bool) (uuid.UUID, uuid.UUID, string, string) {
	tb.Helper()
	credencialID, correo := sembrarCredencialDeLogin(tb, pool, derivador, contrasena, estado)
	if verificado {
		marcarCelularVerificado(tb, pool, credencialID)
	}
	celular := "+57" + credencialID.String()
	return credencialID, parteDeCredencialDeInicioSesion(tb, pool, correo), correo, celular
}

// parteDeCredencialDeInicioSesion lee la parte dueña de la credencial del correo.
func parteDeCredencialDeInicioSesion(tb testing.TB, pool *pgxpool.Pool, correo string) uuid.UUID {
	tb.Helper()
	parte, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (uuid.UUID, error) {
			credencial, err := q.CredencialPorCorreo(ctx, correo)
			if err != nil {
				return uuid.Nil, err
			}
			return uuidDePgtype(credencial.ParteID), nil
		})
	if err != nil {
		tb.Fatalf("leer la parte de la credencial: %v", err)
	}
	return parte
}

// crearRolDeEmpresaDeInicioSesion crea la parte empresa, su fila en auth.empresas y el rol del representante.
func crearRolDeEmpresaDeInicioSesion(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) (uuid.UUID, string) {
	tb.Helper()
	empresa := uuid.New()
	razonSocial := "empresa-" + empresa.String()
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(empresa), Tipo: "empresa"}); err != nil {
				return struct{}{}, err
			}
			if _, err := tx.Exec(ctx,
				"INSERT INTO auth.empresas (parte_id, nit, razon_social) VALUES ($1, $2, $3)",
				pgUUID(empresa), "nit-"+empresa.String(), razonSocial); err != nil {
				return struct{}{}, err
			}
			_, err := q.CrearRolDeParte(ctx, repositorio.CrearRolDeParteParams{
				ParteID:        pgUUID(parteID),
				Rol:            "representante_legal",
				RepresentadaID: pgUUID(empresa),
			})
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("crear el rol de empresa: %v", err)
	}
	return empresa, razonSocial
}

// marcarParteComoEmpresaDeInicioSesion convierte una parte con credencial en empresa con razón social.
func marcarParteComoEmpresaDeInicioSesion(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) {
	tb.Helper()
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			if _, err := tx.Exec(ctx, "UPDATE auth.partes SET tipo = 'empresa' WHERE id = $1", pgUUID(parteID)); err != nil {
				return struct{}{}, err
			}
			_, err := tx.Exec(ctx,
				"INSERT INTO auth.empresas (parte_id, nit, razon_social) VALUES ($1, $2, $3)",
				pgUUID(parteID), "nit-"+parteID.String(), "empresa-directa-"+parteID.String())
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("marcar la parte como empresa: %v", err)
	}
}

// contarSesionesDeParte cuenta las filas de auth.sesiones de la parte bajo rol_auth.
func contarSesionesDeParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) int {
	tb.Helper()
	total, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (int, error) {
			var n int
			err := tx.QueryRow(ctx, "SELECT count(*) FROM auth.sesiones WHERE parte_id = $1", pgUUID(parteID)).Scan(&n)
			return n, err
		})
	if err != nil {
		tb.Fatalf("contar las sesiones de la parte: %v", err)
	}
	return total
}

func TestIniciarSesionActivaEmiteElParYLaFamilia(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	servicio := servicioDeInicioSesionDePrueba(t, pool, derivador)

	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)

	resultado, err := servicio.IniciarSesion(t.Context(), celular, "contrasena-correcta", "", "203.0.113.20", "dispositivo-de-prueba")
	if err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	if resultado.AccessToken == "" || resultado.RefreshToken == "" {
		t.Fatalf("par vacío: %+v", resultado)
	}
	if resultado.Usuario.ID != parteID || resultado.Usuario.ContextoActivo != ContextoUsuario {
		t.Errorf("usuario = %+v, quiero la persona %s en contexto usuario", resultado.Usuario, parteID)
	}
	if total := contarSesionesDeParte(t, pool, parteID); total != 1 {
		t.Fatalf("sesiones de la parte = %d, quiero 1", total)
	}
}

func TestIniciarSesionPendienteRespondeCelularSinVerificarSinSesion(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	servicio := servicioDeInicioSesionDePrueba(t, pool, derivador)

	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoPendienteVerificacion, false)

	_, err := servicio.IniciarSesion(t.Context(), celular, "contrasena-correcta", "", "203.0.113.21", "dispositivo-de-prueba")
	if !errors.Is(err, ErrCelularSinVerificar) {
		t.Fatalf("error = %v, quiero ErrCelularSinVerificar", err)
	}
	if total := contarSesionesDeParte(t, pool, parteID); total != 0 {
		t.Fatalf("la cuenta pendiente creó %d sesiones, quiero 0", total)
	}
}

func TestIniciarSesionEmpresaConRolEmiteElContextoPedido(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	servicio := servicioDeInicioSesionDePrueba(t, pool, derivador)

	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	empresa, _ := crearRolDeEmpresaDeInicioSesion(t, pool, parteID)

	resultado, err := servicio.IniciarSesion(t.Context(), celular, "contrasena-correcta", ContextoEmpresa, "203.0.113.22", "dispositivo-de-prueba")
	if err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	if resultado.Usuario.ContextoActivo != ContextoDeEmpresa(empresa) || resultado.Usuario.RolActivo != "representante_legal" {
		t.Errorf("usuario = %+v, quiero contexto %s con rol representante_legal", resultado.Usuario, ContextoDeEmpresa(empresa))
	}
}

func TestIniciarSesionEmpresaSinRolRespondeSinRolDeEmpresa(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	servicio := servicioDeInicioSesionDePrueba(t, pool, derivador)

	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)

	_, err := servicio.IniciarSesion(t.Context(), celular, "contrasena-correcta", ContextoEmpresa, "203.0.113.23", "dispositivo-de-prueba")
	if !errors.Is(err, ErrSinRolDeEmpresa) {
		t.Fatalf("error = %v, quiero ErrSinRolDeEmpresa", err)
	}
	if total := contarSesionesDeParte(t, pool, parteID); total != 0 {
		t.Fatalf("el login de empresa sin rol creó %d sesiones, quiero 0", total)
	}
}
