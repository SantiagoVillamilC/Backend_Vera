//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDispositivosRevocacionYContextosDeLaParte(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := servicioDeSesionDePrueba(t, pool, derivadorDeLogin(t))
	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivadorDeLogin(t), "contrasena-correcta", EstadoActivo, true)
	empresa, razonSocial := crearRolDeEmpresaDeInicioSesion(t, pool, parteID)

	primera := entrarYVerificar(t, m, celular, "contrasena-correcta", "")
	segunda := entrarYVerificar(t, m, celular, "contrasena-correcta", "")
	familiaPrimera := familiaDeAccess(t, m, primera.AccessToken)
	familiaSegunda := familiaDeAccess(t, m, segunda.AccessToken)

	sesiones, err := m.sesion.ListarDispositivos(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarDispositivos: %v", err)
	}
	if len(sesiones) != 2 {
		t.Fatalf("sesiones activas = %d, quiero 2", len(sesiones))
	}

	if err := m.sesion.RevocarDispositivo(t.Context(), parteID, familiaPrimera); err != nil {
		t.Fatalf("RevocarDispositivo: %v", err)
	}
	sesiones, err = m.sesion.ListarDispositivos(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarDispositivos tras revocar: %v", err)
	}
	if len(sesiones) != 1 || sesiones[0].FamiliaID != familiaSegunda {
		t.Fatalf("sesiones tras revocar una = %+v, quiero solo la segunda %s", sesiones, familiaSegunda)
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familiaPrimera); err != nil || !revocado {
		t.Fatalf("el sid revocado no se publicó: (%v, %v)", revocado, err)
	}
	if _, err := m.sesion.RotarRefresh(t.Context(), primera.RefreshToken); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh de la sesión revocada = %v, quiero ErrRefreshInvalido", err)
	}
	if err := m.sesion.RevocarDispositivo(t.Context(), parteID, familiaPrimera); !errors.Is(err, ErrSesionNoEncontrada) {
		t.Fatalf("segunda revocación = %v, quiero ErrSesionNoEncontrada", err)
	}

	contextos, err := m.sesion.ListarContextos(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarContextos: %v", err)
	}
	if len(contextos) != 2 {
		t.Fatalf("contextos = %+v, quiero personal y empresa", contextos)
	}
	if contextos[0].ID != ContextoUsuario || contextos[0].Tipo != ContextoUsuario || contextos[0].Rol != "" {
		t.Errorf("contexto personal = %+v, quiero usuario con rol vacío", contextos[0])
	}
	if contextos[1].ID != ContextoDeEmpresa(empresa) || contextos[1].Nombre != razonSocial || contextos[1].NIT != "nit-"+empresa.String() || contextos[1].Rol != "representante_legal" {
		t.Errorf("contexto de empresa = %+v, quiero %s con nit y rol", contextos[1], empresa)
	}

	if _, err := m.sesion.ActivarContexto(t.Context(), parteID, familiaSegunda, ContextoDeEmpresa(uuid.New())); !errors.Is(err, ErrContextoInvalido) {
		t.Fatalf("contexto ajeno = %v, quiero ErrContextoInvalido", err)
	}

	activo, err := m.sesion.ActivarContexto(t.Context(), parteID, familiaSegunda, ContextoDeEmpresa(empresa))
	if err != nil {
		t.Fatalf("ActivarContexto: %v", err)
	}
	claims := claimsDeAccess(t, m, activo.AccessToken)
	if claims.Contexto != ContextoDeEmpresa(empresa) || claims.Rol != "representante_legal" || claims.ParteID != parteID {
		t.Fatalf("claims del access nuevo = %+v, quiero la empresa %s con rol", claims, empresa)
	}
	if activo.Contexto.NIT != "nit-"+empresa.String() || activo.Contexto.Nombre != razonSocial {
		t.Fatalf("contexto activo = %+v, quiero la empresa %s", activo.Contexto, empresa)
	}

	rotados, err := m.sesion.RotarRefresh(t.Context(), segunda.RefreshToken)
	if err != nil {
		t.Fatalf("RotarRefresh: %v", err)
	}
	claimsRotados := claimsDeAccess(t, m, rotados.AccessToken)
	if claimsRotados.Contexto != ContextoDeEmpresa(empresa) || claimsRotados.Rol != "representante_legal" {
		t.Fatalf("el refresh no conservó el contexto: %+v", claimsRotados)
	}

	if err := m.sesion.CerrarTodasLasSesiones(t.Context(), parteID); err != nil {
		t.Fatalf("CerrarTodasLasSesiones: %v", err)
	}
	sesiones, err = m.sesion.ListarDispositivos(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarDispositivos tras logout-all: %v", err)
	}
	if len(sesiones) != 0 {
		t.Fatalf("sesiones tras logout-all = %d, quiero 0", len(sesiones))
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familiaPrimera); err != nil || !revocado {
		t.Fatalf("logout-all no publicó la primera familia: (%v, %v)", revocado, err)
	}
	if _, err := m.sesion.RotarRefresh(t.Context(), rotados.RefreshToken); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh tras logout-all = %v, quiero ErrRefreshInvalido", err)
	}
}

func TestCerrarSesionPorRefreshRevocaLaFamiliaYPublicaElSid(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := servicioDeSesionDePrueba(t, pool, derivadorDeLogin(t))
	_, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivadorDeLogin(t), "contrasena-correcta", EstadoActivo, true)

	emitida := entrarYVerificar(t, m, celular, "contrasena-correcta", "")
	familia := familiaDeAccess(t, m, emitida.AccessToken)

	servicio, err := NuevoServicioDeSesion(pool, NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool)), m.tokens, m.denylist)
	if err != nil {
		t.Fatalf("NuevoServicioDeSesion: %v", err)
	}
	if err := servicio.CerrarSesionPorRefresh(t.Context(), parteID, emitida.RefreshToken); err != nil {
		t.Fatalf("CerrarSesionPorRefresh: %v", err)
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familia); err != nil || !revocado {
		t.Fatalf("logout no publicó el sid: (%v, %v)", revocado, err)
	}
	if fila := leerFilaDeRefresh(t, pool, emitida.RefreshToken); !fila.RevocadaEn.Valid {
		t.Fatalf("logout no revocó la familia %s en la base", familia)
	}
	if total := contarEnAuth(t, pool,
		`SELECT count(*) FROM auth.eventos_autenticacion WHERE tipo = $1 AND sesion_id = $2`,
		TipoEventoRevocacion, pgUUID(familia)); total == 0 {
		t.Fatalf("logout no registró el evento de revocación de la familia %s", familia)
	}
	if _, err := m.sesion.RotarRefresh(t.Context(), emitida.RefreshToken); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh tras logout = %v, quiero ErrRefreshInvalido", err)
	}
}

func TestCerrarSesionPorRefreshAjenoNoRevocaLaFamilia(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := servicioDeSesionDePrueba(t, pool, derivadorDeLogin(t))
	_, _, _, celularA := sembrarCredencialDeInicioSesion(t, pool, derivadorDeLogin(t), "contrasena-correcta", EstadoActivo, true)
	_, parteB, _, _ := sembrarCredencialDeInicioSesion(t, pool, derivadorDeLogin(t), "contrasena-correcta", EstadoActivo, true)

	emitida := entrarYVerificar(t, m, celularA, "contrasena-correcta", "")
	familia := familiaDeAccess(t, m, emitida.AccessToken)

	if err := m.sesion.CerrarSesionPorRefresh(t.Context(), parteB, emitida.RefreshToken); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh ajeno = %v, quiero ErrRefreshInvalido sin revocar", err)
	}
	if revocado, err := m.denylist.Revocado(t.Context(), familia); err != nil || revocado {
		t.Fatalf("la familia ajena quedó publicada en la denylist: (%v, %v)", revocado, err)
	}
	if fila := leerFilaDeRefresh(t, pool, emitida.RefreshToken); fila.RevocadaEn.Valid {
		t.Fatalf("la familia ajena quedó revocada en la base")
	}
	if _, err := m.sesion.RotarRefresh(t.Context(), emitida.RefreshToken); err != nil {
		t.Fatalf("el refresh propio debía seguir vigente: %v", err)
	}
}

func TestCerrarSesionPorRefreshSinRefreshRespondeInvalido(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := servicioDeSesionDePrueba(t, pool, derivadorDeLogin(t))
	_, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, derivadorDeLogin(t), "contrasena-correcta", EstadoActivo, true)

	if err := m.sesion.CerrarSesionPorRefresh(t.Context(), parteID, "refresh-inexistente"); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh inexistente = %v, quiero ErrRefreshInvalido", err)
	}
}

func TestCerrarSesionPorRefreshConDenylistQueFallaDejaLaFamiliaRevocada(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	_, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	m := servicioDeSesionDePrueba(t, pool, derivador)

	refresh, _, err := m.gestor.EmitirSesion(t.Context(), parteID, ContextoUsuario, "prueba")
	if err != nil {
		t.Fatalf("EmitirSesion: %v", err)
	}
	servicio, err := NuevoServicioDeSesion(pool, m.gestor, m.tokens, denylistQueFalla{})
	if err != nil {
		t.Fatalf("NuevoServicioDeSesion: %v", err)
	}

	if err := servicio.CerrarSesionPorRefresh(t.Context(), parteID, refresh); !errors.Is(err, ErrAuthNoDisponible) {
		t.Fatalf("denylist caída = %v, quiero ErrAuthNoDisponible", err)
	}
	if fila := leerFilaDeRefresh(t, pool, refresh); !fila.RevocadaEn.Valid {
		t.Fatalf("la familia debe quedar revocada aunque la denylist falle")
	}
	if _, err := m.sesion.RotarRefresh(t.Context(), refresh); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh tras el 503 = %v, quiero ErrRefreshInvalido", err)
	}
}

func TestCerrarSesionPorRefreshDejaLaFamiliaRevocadaEnBaseAlPublicarElSid(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	_, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	m := servicioDeSesionDePrueba(t, pool, derivador)
	refresh, _, err := m.gestor.EmitirSesion(t.Context(), parteID, ContextoUsuario, "prueba")
	if err != nil {
		t.Fatalf("EmitirSesion: %v", err)
	}
	espia := &denylistQueLeeLaBase{pool: pool}
	servicio, err := NuevoServicioDeSesion(pool, m.gestor, m.tokens, espia)
	if err != nil {
		t.Fatalf("NuevoServicioDeSesion: %v", err)
	}

	if err := servicio.CerrarSesionPorRefresh(t.Context(), parteID, refresh); err != nil {
		t.Fatalf("CerrarSesionPorRefresh: %v", err)
	}
	if len(espia.sinRevocarAlPublicar) != 1 || espia.sinRevocarAlPublicar[0] != 0 {
		t.Fatalf("sesiones sin revocar al publicar = %v, quiero una publicación con 0", espia.sinRevocarAlPublicar)
	}
	fila := leerFilaDeRefresh(t, pool, refresh)
	if !fila.RevocadaEn.Valid {
		t.Fatalf("la familia quedó sin revocada_en")
	}
	t.Logf("logout: revocada_en=%s, sesiones sin revocar al publicar el sid=%v", fila.RevocadaEn.Time.UTC(), espia.sinRevocarAlPublicar)
}

func TestCerrarTodasLasSesionesDejaTodoRevocadoEnBaseAlPublicarCadaSid(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	_, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	m := servicioDeSesionDePrueba(t, pool, derivador)
	refreshes := make([]string, 0, 2)
	for range 2 {
		refresh, _, err := m.gestor.EmitirSesion(t.Context(), parteID, ContextoUsuario, "prueba")
		if err != nil {
			t.Fatalf("EmitirSesion: %v", err)
		}
		refreshes = append(refreshes, refresh)
	}
	espia := &denylistQueLeeLaBase{pool: pool}
	servicio, err := NuevoServicioDeSesion(pool, m.gestor, m.tokens, espia)
	if err != nil {
		t.Fatalf("NuevoServicioDeSesion: %v", err)
	}

	if err := servicio.CerrarTodasLasSesiones(t.Context(), parteID); err != nil {
		t.Fatalf("CerrarTodasLasSesiones: %v", err)
	}
	if len(espia.sinRevocarAlPublicar) != 2 {
		t.Fatalf("publicaciones = %d, quiero una por familia", len(espia.sinRevocarAlPublicar))
	}
	for i, pendientes := range espia.sinRevocarAlPublicar {
		if pendientes != 0 {
			t.Errorf("publicación %d con %d sesiones sin revocar en base", i, pendientes)
		}
	}
	for _, refresh := range refreshes {
		if fila := leerFilaDeRefresh(t, pool, refresh); !fila.RevocadaEn.Valid {
			t.Errorf("una familia quedó sin revocada_en")
		}
	}
}

func TestCerrarTodasLasSesionesConDenylistQueFallaDejaTodasLasFamiliasRevocadas(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	_, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	m := servicioDeSesionDePrueba(t, pool, derivador)
	refreshes := make([]string, 0, 2)
	for range 2 {
		refresh, _, err := m.gestor.EmitirSesion(t.Context(), parteID, ContextoUsuario, "prueba")
		if err != nil {
			t.Fatalf("EmitirSesion: %v", err)
		}
		refreshes = append(refreshes, refresh)
	}
	servicio, err := NuevoServicioDeSesion(pool, m.gestor, m.tokens, denylistQueFalla{})
	if err != nil {
		t.Fatalf("NuevoServicioDeSesion: %v", err)
	}

	if err := servicio.CerrarTodasLasSesiones(t.Context(), parteID); !errors.Is(err, ErrAuthNoDisponible) {
		t.Fatalf("denylist caída = %v, quiero ErrAuthNoDisponible", err)
	}
	for _, refresh := range refreshes {
		if fila := leerFilaDeRefresh(t, pool, refresh); !fila.RevocadaEn.Valid {
			t.Errorf("una familia debe quedar revocada aunque la denylist falle")
		}
		if _, err := m.sesion.RotarRefresh(t.Context(), refresh); !errors.Is(err, ErrRefreshInvalido) {
			t.Errorf("refresh tras el 503 = %v, quiero ErrRefreshInvalido", err)
		}
	}
}

// denylistQueLeeLaBase cuenta, al publicar cada sid, las sesiones de esa familia que siguen sin revocar en base.
type denylistQueLeeLaBase struct {
	pool                 *pgxpool.Pool
	sinRevocarAlPublicar []int
}

// Revocar consulta la base con otra conexión y anota cuántas sesiones de la familia siguen vigentes.
func (d *denylistQueLeeLaBase) Revocar(ctx context.Context, sesionID uuid.UUID, _ time.Duration) error {
	pendientes, err := enTransaccion(ctx, poolDeAuth{pool: d.pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (int, error) {
			var n int
			err := tx.QueryRow(ctx, `SELECT count(*) FROM auth.sesiones WHERE familia_id = $1 AND revocada_en IS NULL`, pgUUID(sesionID)).Scan(&n)
			return n, err
		})
	if err != nil {
		return err
	}
	d.sinRevocarAlPublicar = append(d.sinRevocarAlPublicar, pendientes)
	return nil
}

// Revocado nunca se consulta en estos casos.
func (d *denylistQueLeeLaBase) Revocado(context.Context, uuid.UUID) (bool, error) { return false, nil }

var _ RevocadorDeSesiones = (*denylistQueLeeLaBase)(nil)

func TestRotacionCapaLaFamiliaA45DiasDelPrimerUso(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := servicioDeSesionDePrueba(t, pool, derivadorDeLogin(t))
	_, _, _, celular := sembrarCredencialDeInicioSesion(t, pool, derivadorDeLogin(t), "contrasena-correcta", EstadoActivo, true)

	emitida := entrarYVerificar(t, m, celular, "contrasena-correcta", "")
	familia := familiaDeAccess(t, m, emitida.AccessToken)
	primera := leerFilaDeRefresh(t, pool, emitida.RefreshToken)
	if got := primera.VenceEn.Time.Sub(primera.CreadaEn.Time); got < vigenciaInactividad-time.Minute || got > vigenciaInactividad+time.Minute {
		t.Fatalf("vence_en inicial = %s, quiero 30 días de inactividad", got)
	}

	envejecerPrimeraSesionDeFamilia(t, familia, 20*24*time.Hour)
	primera = leerFilaDeRefresh(t, pool, emitida.RefreshToken)
	rotados, err := m.sesion.RotarRefresh(t.Context(), emitida.RefreshToken)
	if err != nil {
		t.Fatalf("RotarRefresh: %v", err)
	}
	sucesora := leerFilaDeRefresh(t, pool, rotados.RefreshToken)
	tope := primera.CreadaEn.Time.Add(vigenciaAbsolutaFamilia)
	t.Logf("familia=%s primera_creada_en=%s sucesora_vence_en=%s delta_dias=%.4f",
		familia, primera.CreadaEn.Time.UTC(), sucesora.VenceEn.Time.UTC(),
		sucesora.VenceEn.Time.Sub(primera.CreadaEn.Time).Hours()/24)
	if delta := sucesora.VenceEn.Time.Sub(tope); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("vence_en rotado = %s, quiero el tope absoluto %s (45 días desde %s)", sucesora.VenceEn.Time, tope, primera.CreadaEn.Time)
	}
	if !sucesora.VenceEn.Time.Before(time.Now().UTC().Add(vigenciaInactividad)) {
		t.Fatalf("el tope absoluto debería ganarle a la inactividad")
	}
}

// denylistQueFalla falla toda publicación para probar el fail closed.
type denylistQueFalla struct{}

// Revocar devuelve el error de la denylist marcado como fail closed.
func (denylistQueFalla) Revocar(context.Context, uuid.UUID, time.Duration) error {
	return ConFailClosed(errors.New("denylist caída"))
}

// Revocado también falla, cerrada.
func (denylistQueFalla) Revocado(context.Context, uuid.UUID) (bool, error) {
	return false, ConFailClosed(errors.New("denylist caída"))
}

var _ RevocadorDeSesiones = denylistQueFalla{}

// leerFilaDeRefresh lee la fila de la sesión del refresh bajo rol_auth.
func leerFilaDeRefresh(tb testing.TB, pool *pgxpool.Pool, refresh string) repositorio.Sesion {
	tb.Helper()
	fila, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.Sesion, error) {
			return q.SesionPorRefreshHash(ctx, hashDeRefresh(refresh))
		})
	if err != nil {
		tb.Fatalf("leer la fila del refresh: %v", err)
	}
	return fila
}

// envejecerPrimeraSesionDeFamilia mueve hacia atrás la primera sesión como dueño del esquema.
func envejecerPrimeraSesionDeFamilia(tb testing.TB, familia uuid.UUID, edad time.Duration) {
	tb.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		tb.Fatalf("DATABASE_URL es obligatoria para envejecer la familia como dueño del esquema")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parsear DATABASE_URL: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		tb.Fatalf("abrir pool dueño: %v", err)
	}
	tb.Cleanup(pool.Close)
	if _, err := pool.Exec(tb.Context(),
		"UPDATE auth.sesiones SET creada_en = $2, ultimo_uso_en = $2 WHERE familia_id = $1",
		pgUUID(familia), time.Now().Add(-edad)); err != nil {
		tb.Fatalf("envejecer la familia como dueño: %v", err)
	}
}

// familiaDeAccess resuelve el sid de familia del access emitido.
func familiaDeAccess(tb testing.TB, m montajeDeSesion, access string) uuid.UUID {
	tb.Helper()
	return claimsDeAccess(tb, m, access).SesionID
}

// claimsDeAccess verifica el access con el firmador del montaje.
func claimsDeAccess(tb testing.TB, m montajeDeSesion, access string) ClaimsAccess {
	tb.Helper()
	claims, err := m.tokens.Verificar(access)
	if err != nil {
		tb.Fatalf("Verificar access: %v", err)
	}
	return claims
}
