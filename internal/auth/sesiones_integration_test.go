//go:build integration

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// abrirPoolConcurrenteDeAuth abre un pool con varias conexiones para las pruebas de dos goroutines.
func abrirPoolConcurrenteDeAuth(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		tb.Fatalf("APP_DATABASE_URL es obligatoria bajo el tag integration")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parsear DSN: %v", err)
	}
	cfg.MaxConns = 4
	cfg.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		tb.Fatalf("abrir pool concurrente: %v", err)
	}
	tb.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		tb.Fatalf("ping: %v", err)
	}
	return pool
}

// sembrarParteSesion inserta una parte del tipo pedido, sin credencial, para las pruebas de sesión.
func sembrarParteSesion(tb testing.TB, pool *pgxpool.Pool, tipo string) uuid.UUID {
	tb.Helper()

	id := uuid.New()
	_, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			_, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(id), Tipo: tipo})
			return struct{}{}, err
		})
	if err != nil {
		tb.Fatalf("sembrar parte %s: %v", tipo, err)
	}
	return id
}

// estadoDeSesion es lo que los asserts leen directo de la fila persistida.
type estadoDeSesion struct {
	UsadoEn     pgtype.Timestamptz
	RevocadaEn  pgtype.Timestamptz
	RefreshHash []byte
	FamiliaID   pgtype.UUID
	VenceEn     pgtype.Timestamptz
}

// leerSesion lee el estado persistido de una sesión como rol_auth.
func leerSesion(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) estadoDeSesion {
	tb.Helper()

	estado, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (estadoDeSesion, error) {
			var e estadoDeSesion
			err := tx.QueryRow(ctx,
				"SELECT usado_en, revocada_en, refresh_hash, familia_id, vence_en FROM auth.sesiones WHERE id = $1",
				pgUUID(id)).Scan(&e.UsadoEn, &e.RevocadaEn, &e.RefreshHash, &e.FamiliaID, &e.VenceEn)
			return e, err
		})
	if err != nil {
		tb.Fatalf("leer la sesión %s: %v", id, err)
	}
	return estado
}

// retrasarUsadoEn mueve usado_en al pasado para simular un uso viejo fuera de la ventana de gracia.
func retrasarUsadoEn(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID, atraso time.Duration) {
	tb.Helper()

	_, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE auth.sesiones SET usado_en = now() - make_interval(secs => $2) WHERE id = $1", pgUUID(id), atraso.Seconds())
			return struct{}{}, err
		})
	if err != nil {
		tb.Fatalf("retrasar usado_en de %s: %v", id, err)
	}
}

// contarFamilia devuelve el total de filas de la familia y cuántas están revocadas.
func contarFamilia(tb testing.TB, pool *pgxpool.Pool, familiaID uuid.UUID) (int, int) {
	tb.Helper()

	var total, revocadas int
	_, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			err := tx.QueryRow(ctx,
				"SELECT count(*), count(*) FILTER (WHERE revocada_en IS NOT NULL) FROM auth.sesiones WHERE familia_id = $1",
				pgUUID(familiaID)).Scan(&total, &revocadas)
			return struct{}{}, err
		})
	if err != nil {
		tb.Fatalf("contar la familia %s: %v", familiaID, err)
	}
	return total, revocadas
}

// verificarHash exige que la fila guarde el SHA-256 de 32 bytes y nunca el refresh en claro.
func verificarHash(tb testing.TB, guardado []byte, refreshEnClaro string) {
	tb.Helper()

	if len(guardado) != sha256.Size {
		tb.Errorf("refresh_hash = %d bytes, quiero %d", len(guardado), sha256.Size)
	}
	if bytes.Equal(guardado, []byte(refreshEnClaro)) {
		tb.Errorf("refresh_hash guardó el refresh en claro")
	}
	esperado := sha256.Sum256([]byte(refreshEnClaro))
	if !bytes.Equal(guardado, esperado[:]) {
		tb.Errorf("refresh_hash no es el SHA-256 del refresh presentado")
	}
}

func TestEmitirYRotarRefreshConservaLaFamilia(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	parteID := sembrarParteSesion(t, pool, "persona")
	g := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))

	refresh1, sesion1, err := g.EmitirSesion(ctx, parteID, "personal", "web")
	if err != nil {
		t.Fatalf("emitir la sesión: %v", err)
	}
	if refresh1 == "" || sesion1.ID == uuid.Nil || sesion1.FamiliaID == uuid.Nil {
		t.Fatalf("sesión emitida incompleta: refresh=%q sesion=%+v", refresh1, sesion1)
	}
	if sesion1.ParteID != parteID || sesion1.ContextoActivo != "personal" || sesion1.Dispositivo != "web" {
		t.Fatalf("sesión emitida no conserva parte/contexto/dispositivo: %+v", sesion1)
	}
	if restante := time.Until(sesion1.VenceEn); restante < 29*24*time.Hour || restante > 31*24*time.Hour {
		t.Errorf("vence_en a %s de ahora, quiero cerca de 30 días", restante)
	}

	refresh2, sesion2, err := g.RotarRefresh(ctx, refresh1)
	if err != nil {
		t.Fatalf("rotar el refresh: %v", err)
	}
	if refresh2 == refresh1 {
		t.Errorf("la rotación devolvió el mismo refresh en claro")
	}
	if sesion2.ID == sesion1.ID {
		t.Errorf("la rotación reutilizó el id de la sesión")
	}
	if sesion2.FamiliaID != sesion1.FamiliaID {
		t.Errorf("familia = %s, quiero la misma %s", sesion2.FamiliaID, sesion1.FamiliaID)
	}

	if !leerSesion(t, pool, sesion1.ID).UsadoEn.Valid {
		t.Errorf("la sesión anterior no quedó con usado_en")
	}
	if leerSesion(t, pool, sesion2.ID).UsadoEn.Valid {
		t.Errorf("el sucesor nació con usado_en")
	}
	if total, revocadas := contarFamilia(t, pool, sesion1.FamiliaID); total != 2 || revocadas != 0 {
		t.Errorf("familia = %d filas/%d revocadas, quiero 2/0", total, revocadas)
	}

	verificarHash(t, leerSesion(t, pool, sesion1.ID).RefreshHash, refresh1)
	verificarHash(t, leerSesion(t, pool, sesion2.ID).RefreshHash, refresh2)
}

func TestLaSesionGuardaSoloElHashYNoElRol(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	parteID := sembrarParteSesion(t, pool, "persona")
	g := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))

	refresh, sesion, err := g.EmitirSesion(ctx, parteID, "personal", "web")
	if err != nil {
		t.Fatalf("emitir la sesión: %v", err)
	}
	verificarHash(t, leerSesion(t, pool, sesion.ID).RefreshHash, refresh)

	columnas := map[string]bool{}
	_, err = enTransaccion(ctx, poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx, "SELECT column_name FROM information_schema.columns WHERE table_schema = 'auth' AND table_name = 'sesiones'")
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var columna string
				if err := rows.Scan(&columna); err != nil {
					return struct{}{}, err
				}
				columnas[columna] = true
			}
			return struct{}{}, rows.Err()
		})
	if err != nil {
		t.Fatalf("listar columnas de auth.sesiones: %v", err)
	}
	for _, exigida := range []string{"id", "familia_id", "parte_id", "contexto_activo", "dispositivo", "refresh_hash", "creada_en", "ultimo_uso_en", "usado_en", "revocada_en", "vence_en"} {
		if !columnas[exigida] {
			t.Errorf("falta la columna %q en auth.sesiones", exigida)
		}
	}
	if columnas["rol"] {
		t.Errorf("auth.sesiones guardó una columna de rol: el rol se resuelve de roles_de_parte en cada emisión")
	}
}

func TestRotacionConcurrenteRotaUnaYSoloUna(t *testing.T) {
	pool := abrirPoolConcurrenteDeAuth(t)
	ctx := t.Context()
	parteID := sembrarParteSesion(t, pool, "persona")
	g := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))

	refresh1, sesion1, err := g.EmitirSesion(ctx, parteID, "personal", "web")
	if err != nil {
		t.Fatalf("emitir la sesión: %v", err)
	}

	type intento struct {
		refresh string
		err     error
	}
	resultados := make(chan intento, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			refresh, _, err := g.RotarRefresh(ctx, refresh1)
			resultados <- intento{refresh: refresh, err: err}
		})
	}
	wg.Wait()
	close(resultados)

	var exitos, invalidos int
	for res := range resultados {
		switch {
		case res.err == nil:
			exitos++
			if res.refresh == "" {
				t.Errorf("una rotación exitosa devolvió refresh vacío")
			}
		case errors.Is(res.err, ErrRefreshInvalido):
			invalidos++
		default:
			t.Errorf("error inesperado en la carrera: %v", res.err)
		}
	}
	if exitos != 1 || invalidos != 1 {
		t.Fatalf("carrera = %d exitos/%d invalidos, quiero exactamente 1/1", exitos, invalidos)
	}
	if total, revocadas := contarFamilia(t, pool, sesion1.FamiliaID); total != 2 || revocadas != 0 {
		t.Errorf("la familia quedó %d filas/%d revocadas, quiero 2/0 (viva)", total, revocadas)
	}
}

func TestReusoFueraDeLaVentanaRevocaLaFamilia(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	parteID := sembrarParteSesion(t, pool, "persona")
	g := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))

	refresh1, sesion1, err := g.EmitirSesion(ctx, parteID, "personal", "web")
	if err != nil {
		t.Fatalf("emitir la sesión: %v", err)
	}
	refresh2, sesion2, err := g.RotarRefresh(ctx, refresh1)
	if err != nil {
		t.Fatalf("rotar la sesión: %v", err)
	}
	retrasarUsadoEn(t, pool, sesion1.ID, 20*time.Second)

	if _, _, err := g.RotarRefresh(ctx, refresh1); !errors.Is(err, ErrRefreshReusado) {
		t.Fatalf("reuso = %v, quiero ErrRefreshReusado", err)
	}

	if !leerSesion(t, pool, sesion1.ID).RevocadaEn.Valid {
		t.Errorf("la sesión reusada no quedó revocada")
	}
	if !leerSesion(t, pool, sesion2.ID).RevocadaEn.Valid {
		t.Errorf("el sucesor no quedó revocado junto con la familia")
	}
	if total, revocadas := contarFamilia(t, pool, sesion1.FamiliaID); total != 2 || revocadas != 2 {
		t.Errorf("familia = %d filas/%d revocadas, quiero 2/2", total, revocadas)
	}
	if _, _, err := g.RotarRefresh(ctx, refresh2); !errors.Is(err, ErrRefreshInvalido) {
		t.Errorf("el sucesor revocado = %v, quiero ErrRefreshInvalido", err)
	}
}

func TestRefreshDesconocidoEsInvalidoSinRevocar(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	g := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))

	if _, _, err := g.RotarRefresh(ctx, "refresh-que-no-existe"); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("refresh desconocido = %v, quiero ErrRefreshInvalido", err)
	}
}

func TestListarActivasExcluyeUsadasYRevocadas(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	parteID := sembrarParteSesion(t, pool, "persona")
	g := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))

	refresh1, sesion1, err := g.EmitirSesion(ctx, parteID, "personal", "web")
	if err != nil {
		t.Fatalf("emitir la primera sesión: %v", err)
	}
	_, sesion2, err := g.EmitirSesion(ctx, parteID, "empresa", "movil")
	if err != nil {
		t.Fatalf("emitir la segunda sesión: %v", err)
	}
	_, sesion3, err := g.RotarRefresh(ctx, refresh1)
	if err != nil {
		t.Fatalf("rotar la primera sesión: %v", err)
	}
	if err := g.RevocarSesion(ctx, parteID, sesion2.ID); err != nil {
		t.Fatalf("revocar la segunda sesión: %v", err)
	}

	activas, err := g.ListarActivas(ctx, parteID)
	if err != nil {
		t.Fatalf("listar activas: %v", err)
	}
	if len(activas) != 1 || activas[0].ID != sesion3.ID {
		t.Fatalf("activas = %+v, quiero solo el sucesor %s", activas, sesion3.ID)
	}
	for _, activa := range activas {
		if activa.ID == sesion1.ID || activa.ID == sesion2.ID {
			t.Errorf("ListarActivas devolvió una sesión usada o revocada")
		}
	}
}

func TestRevocarTodasYFamiliaPublicanRevocacion(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	parteID := sembrarParteSesion(t, pool, "persona")
	g := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))

	_, sesion1, err := g.EmitirSesion(ctx, parteID, "personal", "web")
	if err != nil {
		t.Fatalf("emitir la primera sesión: %v", err)
	}
	_, sesion2, err := g.EmitirSesion(ctx, parteID, "empresa", "movil")
	if err != nil {
		t.Fatalf("emitir la segunda sesión: %v", err)
	}

	if err := g.RevocarFamilia(ctx, sesion1.FamiliaID); err != nil {
		t.Fatalf("revocar la familia: %v", err)
	}
	if !leerSesion(t, pool, sesion1.ID).RevocadaEn.Valid {
		t.Errorf("RevocarFamilia no revocó la sesión de la familia")
	}
	if leerSesion(t, pool, sesion2.ID).RevocadaEn.Valid {
		t.Errorf("RevocarFamilia revocó una sesión de otra familia")
	}

	if err := g.RevocarTodas(ctx, parteID); err != nil {
		t.Fatalf("revocar todas: %v", err)
	}
	if !leerSesion(t, pool, sesion2.ID).RevocadaEn.Valid {
		t.Errorf("RevocarTodas no revocó la segunda sesión")
	}
}

func TestRolesVigentesDeParteResuelveYExcluyeRevocados(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := t.Context()
	parteID := sembrarParteSesion(t, pool, "persona")
	empresaA := sembrarParteSesion(t, pool, "empresa")
	empresaB := sembrarParteSesion(t, pool, "empresa")
	g := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))

	var revocado int64
	_, err := enTransaccion(ctx, poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearRolDeParte(ctx, repositorio.CrearRolDeParteParams{
				ParteID:        pgUUID(parteID),
				Rol:            "representante_legal",
				RepresentadaID: pgUUID(empresaA),
			}); err != nil {
				return struct{}{}, err
			}
			fila, err := q.CrearRolDeParte(ctx, repositorio.CrearRolDeParteParams{
				ParteID:        pgUUID(parteID),
				Rol:            "administrador",
				RepresentadaID: pgUUID(empresaB),
			})
			if err != nil {
				return struct{}{}, err
			}
			revocado = fila.ID
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatalf("sembrar roles: %v", err)
	}

	if _, err := enTransaccion(ctx, poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE auth.roles_de_parte SET revocado_en = now() WHERE id = $1", revocado)
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("revocar el rol: %v", err)
	}

	roles, err := g.RolesVigentesDeParte(ctx, parteID)
	if err != nil {
		t.Fatalf("roles vigentes: %v", err)
	}
	if len(roles) != 1 || roles[0].Rol != "representante_legal" || roles[0].RepresentadaID != empresaA {
		t.Fatalf("roles vigentes = %+v, quiero solo representante_legal de %s", roles, empresaA)
	}
}

func TestDeleteSesionesComoRolAuthFallaPorPermiso(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		t.Fatalf("SET LOCAL ROLE %s: %v", RolAuth, err)
	}

	_, errDelete := tx.Exec(ctx, "DELETE FROM auth.sesiones")
	t.Logf("error literal de DELETE como rol_auth: %v", errDelete)
	if errDelete == nil || !strings.Contains(errDelete.Error(), "permission denied for table sesiones") {
		t.Fatalf("DELETE no falló por permiso: %v", errDelete)
	}
	assertSQLSTATE(t, errDelete, pgerrcode.InsufficientPrivilege)
}

func TestRolAuthNoReescribeColumnasFueraDelGrant(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		t.Fatalf("SET LOCAL ROLE %s: %v", RolAuth, err)
	}

	_, errUpdate := tx.Exec(ctx, "UPDATE auth.sesiones SET creada_en = now()")
	t.Logf("error literal de UPDATE de creada_en como rol_auth: %v", errUpdate)
	if errUpdate == nil || !strings.Contains(errUpdate.Error(), "permission denied for table sesiones") {
		t.Fatalf("UPDATE de creada_en no falló por permiso: %v", errUpdate)
	}
	assertSQLSTATE(t, errUpdate, pgerrcode.InsufficientPrivilege)
}
