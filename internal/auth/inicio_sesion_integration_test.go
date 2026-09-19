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

// servicioDeInicioSesionDePrueba arma el servicio sobre Postgres y Redis reales con un canal falso.
func servicioDeInicioSesionDePrueba(tb testing.TB, pool *pgxpool.Pool, derivador Derivador) (*ServicioDeLogin, *canalOTPFalso) {
	tb.Helper()
	eventos := &registradorEventosDeLogin{}
	verificador, claves := verificadorDeLogin(tb, pool, derivador, eventos)
	canal := &canalOTPFalso{}
	gestor, err := NuevoGestorDesafios(pool, canal, eventos, SiempreOptIn{}, pepperDeDesafio,
		ConRacionamiento(contadoresDeLogin(tb), claves))
	if err != nil {
		tb.Fatalf("gestor de desafíos: %v", err)
	}
	servicio, err := NuevoServicioDeLogin(verificador, gestor, NuevoGestorSesiones(pool, eventos))
	if err != nil {
		tb.Fatalf("servicio de login: %v", err)
	}
	return servicio, canal
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

// crearRolDeEmpresaDeInicioSesion crea una empresa y el rol del representante sobre ella.
func crearRolDeEmpresaDeInicioSesion(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) uuid.UUID {
	tb.Helper()
	empresa := uuid.New()
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(empresa), Tipo: "empresa"}); err != nil {
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
	return empresa
}

// leerDesafioDeInicioSesion lee la fila persistida del desafío.
func leerDesafioDeInicioSesion(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) repositorio.DesafioPorIDRow {
	tb.Helper()
	fila, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.DesafioPorIDRow, error) {
			return q.DesafioPorID(ctx, pgUUID(id))
		})
	if err != nil {
		tb.Fatalf("leer el desafío: %v", err)
	}
	return fila
}

func TestIniciarSesionActivaPersisteElDesafioDeLoginYEntregaElCodigo(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	servicio, canal := servicioDeInicioSesionDePrueba(t, pool, derivador)

	_, _, correo, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)

	resultado, err := servicio.IniciarSesion(t.Context(), correo, "contrasena-correcta", "", "203.0.113.20")
	if err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	if resultado.Proposito != PropositoLogin || !resultado.RequiereSegundoFactor || resultado.ExpiraEnSegundos <= 0 {
		t.Fatalf("resultado = %+v, quiero proposito login con vigencia", resultado)
	}
	fila := leerDesafioDeInicioSesion(t, pool, resultado.ChallengeID)
	if fila.Proposito != string(PropositoLogin) || fila.CelularDestino != celular {
		t.Errorf("desafío = %+v, quiero proposito login al celular verificado", fila)
	}
	if !fila.ContextoSolicitado.Valid || fila.ContextoSolicitado.String != ContextoUsuario {
		t.Errorf("contexto_solicitado = %+v, quiero %q", fila.ContextoSolicitado, ContextoUsuario)
	}
	if canal.ultimoCelular != celular || len(canal.ultimoTexto) != longitudCodigo {
		t.Errorf("el canal entregó (%q, %q), quiero el celular %q con un código de %d dígitos", canal.ultimoCelular, canal.ultimoTexto, celular, longitudCodigo)
	}
}

func TestIniciarSesionPendienteReanudaConDesafioDeRegistro(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	servicio, canal := servicioDeInicioSesionDePrueba(t, pool, derivador)

	_, _, correo, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoPendienteVerificacion, false)

	resultado, err := servicio.IniciarSesion(t.Context(), correo, "contrasena-correcta", "", "203.0.113.21")
	if err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	if resultado.Proposito != PropositoRegistro || resultado.Mensaje != MensajeDesafioRegistro {
		t.Fatalf("resultado = %+v, quiero proposito registro para reanudar", resultado)
	}
	fila := leerDesafioDeInicioSesion(t, pool, resultado.ChallengeID)
	if fila.Proposito != string(PropositoRegistro) || fila.CelularDestino != celular {
		t.Errorf("desafío = %+v, quiero proposito registro al celular registrado", fila)
	}
	if canal.ultimoCelular != celular || len(canal.ultimoTexto) != longitudCodigo {
		t.Errorf("el canal entregó (%q, %q), quiero el celular %q con un código de %d dígitos", canal.ultimoCelular, canal.ultimoTexto, celular, longitudCodigo)
	}
}

func TestIniciarSesionEmpresaConRolGuardaElContextoPedido(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	servicio, _ := servicioDeInicioSesionDePrueba(t, pool, derivador)

	_, parteID, correo, _ := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	empresa := crearRolDeEmpresaDeInicioSesion(t, pool, parteID)

	resultado, err := servicio.IniciarSesion(t.Context(), correo, "contrasena-correcta", ContextoEmpresa, "203.0.113.22")
	if err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	fila := leerDesafioDeInicioSesion(t, pool, resultado.ChallengeID)
	if !fila.ContextoSolicitado.Valid || fila.ContextoSolicitado.String != ContextoEmpresa {
		t.Errorf("contexto_solicitado = %+v, quiero %q para %s", fila.ContextoSolicitado, ContextoEmpresa, empresa)
	}
}

func TestIniciarSesionEmpresaSinRolNoCreaDesafio(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	servicio, _ := servicioDeInicioSesionDePrueba(t, pool, derivador)

	_, _, correo, _ := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)

	_, err := servicio.IniciarSesion(t.Context(), correo, "contrasena-correcta", ContextoEmpresa, "203.0.113.23")
	if !errors.Is(err, ErrSinRolDeEmpresa) {
		t.Fatalf("error = %v, quiero ErrSinRolDeEmpresa", err)
	}
}
