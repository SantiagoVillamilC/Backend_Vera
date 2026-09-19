//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// denylistRedisDeSesion implementa el puerto sin importar auth/denylist, que crearía un ciclo.
type denylistRedisDeSesion struct{ cliente *redis.Client }

// Revocar publica el sid con el ttl del access restante, fail closed.
func (d *denylistRedisDeSesion) Revocar(ctx context.Context, sid uuid.UUID, ttl time.Duration) error {
	if err := d.cliente.Set(ctx, "denylist:sid:"+sid.String(), 1, ttl).Err(); err != nil {
		return ConFailClosed(err)
	}
	return nil
}

// Revocado consulta el sid en Redis, fail closed.
func (d *denylistRedisDeSesion) Revocado(ctx context.Context, sid uuid.UUID) (bool, error) {
	n, err := d.cliente.Exists(ctx, "denylist:sid:"+sid.String()).Result()
	if err != nil {
		return false, ConFailClosed(err)
	}
	return n > 0, nil
}

var _ RevocadorDeSesiones = (*denylistRedisDeSesion)(nil)

// denylistDeSesionDePrueba monta la denylist sobre el Redis real del carril.
func denylistDeSesionDePrueba(tb testing.TB) *denylistRedisDeSesion {
	tb.Helper()
	raw := os.Getenv("REDIS_URL")
	if raw == "" {
		tb.Fatalf("REDIS_URL es obligatoria bajo el tag integration")
	}
	cliente, err := platformredis.NuevoCliente(raw)
	if err != nil {
		tb.Fatalf("cliente Redis: %v", err)
	}
	tb.Cleanup(func() { _ = cliente.Close() })
	if err := cliente.Ping(context.Background()).Err(); err != nil {
		tb.Fatalf("ping al Redis: %v", err)
	}
	return &denylistRedisDeSesion{cliente: cliente}
}

// montajeDeSesion agrupa el login y la sesión con sus colaboradores para el flujo de la unidad.
type montajeDeSesion struct {
	login    *ServicioDeLogin
	sesion   *ServicioDeSesion
	tokens   *TokensDeAcceso
	denylist *denylistRedisDeSesion
}

// servicioDeSesionDePrueba arma el flujo completo (login y rotación) sobre Postgres y Redis reales.
func servicioDeSesionDePrueba(tb testing.TB, pool *pgxpool.Pool, derivador Derivador) montajeDeSesion {
	tb.Helper()
	eventos := &registradorEventosDeLogin{}
	verificador, _ := verificadorDeLogin(tb, pool, derivador, eventos)
	sesiones := NuevoGestorSesiones(pool, eventos)
	tokens, err := NuevoTokensDeAcceso([]byte("clave-de-firma-de-al-menos-32-bytes"), "centinela-auth", "centinela-api")
	if err != nil {
		tb.Fatalf("tokens de acceso: %v", err)
	}
	login, err := NuevoServicioDeLogin(verificador, pool, sesiones, tokens)
	if err != nil {
		tb.Fatalf("servicio de login: %v", err)
	}
	denylist := denylistDeSesionDePrueba(tb)
	sesion, err := NuevoServicioDeSesion(pool, sesiones, tokens, denylist)
	if err != nil {
		tb.Fatalf("servicio de sesión: %v", err)
	}
	return montajeDeSesion{login: login, sesion: sesion, tokens: tokens, denylist: denylist}
}

// envejecerRefresh mueve el uso del refresh fuera de la ventana de gracia.
func envejecerRefresh(tb testing.TB, pool *pgxpool.Pool, refresh string) {
	tb.Helper()
	hash := hashDeRefresh(refresh)
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE auth.sesiones SET usado_en = now() - interval '1 minute' WHERE refresh_hash = $1", hash)
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("envejecer el refresh: %v", err)
	}
}

// entrarEmite corre el login directo y devuelve la sesión emitida.
func entrarEmite(tb testing.TB, m montajeDeSesion, celular, contrasena, contexto string) SesionEmitida {
	tb.Helper()
	emitida, err := m.login.IniciarSesion(tb.Context(), celular, contrasena, contexto, "203.0.113.30", "dispositivo-de-prueba")
	if err != nil {
		tb.Fatalf("IniciarSesion: %v", err)
	}
	return emitida
}

// entrarYVerificar conserva el nombre histórico: el login directo ya emite la sesión.
func entrarYVerificar(tb testing.TB, m montajeDeSesion, celular, contrasena, contexto string) SesionEmitida {
	tb.Helper()
	return entrarEmite(tb, m, celular, contrasena, contexto)
}

func TestLoginEmiteElParYElAccessConTodosLosClaims(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	m := servicioDeSesionDePrueba(t, pool, derivador)
	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)

	emitida := entrarEmite(t, m, celular, "contrasena-correcta", "")
	if emitida.AccessToken == "" || emitida.RefreshToken == "" {
		t.Fatalf("par vacío: %+v", emitida)
	}
	if emitida.Usuario.ID != parteID || emitida.Usuario.TipoCuenta != "persona" {
		t.Errorf("usuario = %+v, quiero la persona %s", emitida.Usuario, parteID)
	}
	if emitida.Usuario.ContextoActivo != ContextoUsuario || emitida.Usuario.RolActivo != "" {
		t.Errorf("contexto/rol = %q/%q, quiero usuario con rol vacío", emitida.Usuario.ContextoActivo, emitida.Usuario.RolActivo)
	}

	claims, err := m.tokens.Verificar(emitida.AccessToken)
	if err != nil {
		t.Fatalf("Verificar access: %v", err)
	}
	if claims.ParteID != parteID || claims.Contexto != ContextoUsuario || claims.Rol != "" {
		t.Errorf("claims = %+v, quiero parte %s en contexto usuario", claims, parteID)
	}
	if claims.ExpiraEn.Sub(claims.EmitidoEn) != duracionAccess {
		t.Errorf("vida del access = %s, quiero %s", claims.ExpiraEn.Sub(claims.EmitidoEn), duracionAccess)
	}
}

func TestRotaElRefreshConVentanaDeGraciaYReusoRevocaLaFamilia(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	m := servicioDeSesionDePrueba(t, pool, derivador)
	_, _, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)

	emitida := entrarEmite(t, m, celular, "contrasena-correcta", "")
	claims, err := m.tokens.Verificar(emitida.AccessToken)
	if err != nil {
		t.Fatalf("Verificar access: %v", err)
	}
	familia := claims.SesionID

	rotados, err := m.sesion.RotarRefresh(t.Context(), emitida.RefreshToken)
	if err != nil {
		t.Fatalf("RotarRefresh: %v", err)
	}
	if rotados.RefreshToken == emitida.RefreshToken || rotados.AccessToken == "" {
		t.Fatalf("rotación no cambió el refresh: %+v", rotados)
	}

	if _, err := m.sesion.RotarRefresh(t.Context(), emitida.RefreshToken); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("segundo uso en la ventana de gracia = %v, quiero ErrRefreshInvalido sin revocar", err)
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familia); err != nil || revocado {
		t.Fatalf("la ventana de gracia publicó el sid: (%v, %v)", revocado, err)
	}

	envejecerRefresh(t, pool, emitida.RefreshToken)
	if _, err := m.sesion.RotarRefresh(t.Context(), emitida.RefreshToken); !errors.Is(err, ErrRefreshReusado) {
		t.Fatalf("reuso fuera de la ventana = %v, quiero ErrRefreshReusado", err)
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familia); err != nil || !revocado {
		t.Fatalf("el reuso no publicó el sid en la denylist: (%v, %v)", revocado, err)
	}
}

func TestContextoEmpresaEmiteCtxConRolYElUuidDeLaEmpresa(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	m := servicioDeSesionDePrueba(t, pool, derivador)
	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	empresa, razonSocial := crearRolDeEmpresaDeInicioSesion(t, pool, parteID)

	emitida := entrarEmite(t, m, celular, "contrasena-correcta", ContextoEmpresa)
	if emitida.Usuario.ID != empresa || emitida.Usuario.TipoCuenta != "empresa" {
		t.Errorf("usuario = %+v, quiero la empresa %s", emitida.Usuario, empresa)
	}
	if emitida.Usuario.RazonSocial != razonSocial {
		t.Errorf("razón social = %q, quiero %q", emitida.Usuario.RazonSocial, razonSocial)
	}
	if emitida.Usuario.ContextoActivo != ContextoDeEmpresa(empresa) || emitida.Usuario.RolActivo != "representante_legal" {
		t.Errorf("contexto/rol = %q/%q, quiero empresa:%s/representante_legal",
			emitida.Usuario.ContextoActivo, emitida.Usuario.RolActivo, empresa)
	}

	claims, err := m.tokens.Verificar(emitida.AccessToken)
	if err != nil {
		t.Fatalf("Verificar access: %v", err)
	}
	if claims.Contexto != ContextoDeEmpresa(empresa) || claims.Rol != "representante_legal" {
		t.Errorf("claims = %+v, quiero ctx empresa:%s con rol", claims, empresa)
	}
	// El refresh conserva el contexto de empresa y resuelve el rol en cada emisión.
	rotados, err := m.sesion.RotarRefresh(t.Context(), emitida.RefreshToken)
	if err != nil {
		t.Fatalf("RotarRefresh de empresa: %v", err)
	}
	claimsNuevos, err := m.tokens.Verificar(rotados.AccessToken)
	if err != nil {
		t.Fatalf("Verificar access rotado: %v", err)
	}
	if claimsNuevos.Contexto != ContextoDeEmpresa(empresa) || claimsNuevos.Rol != "representante_legal" {
		t.Errorf("claims rotados = %+v, quiero el contexto de empresa conservado", claimsNuevos)
	}
}

func TestParteEmpresaConLoginDirectoEmiteTipoCuentaEmpresa(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	m := servicioDeSesionDePrueba(t, pool, derivador)
	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	marcarParteComoEmpresaDeInicioSesion(t, pool, parteID)

	emitida := entrarEmite(t, m, celular, "contrasena-correcta", "")
	if emitida.Usuario.TipoCuenta != "empresa" {
		t.Errorf("tipo de cuenta = %q, quiero empresa", emitida.Usuario.TipoCuenta)
	}
}
