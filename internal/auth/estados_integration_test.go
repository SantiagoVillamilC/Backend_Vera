//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// abrirPoolDeAuthConcurrente abre el pool con varias conexiones para los tests de carrera.
func abrirPoolDeAuthConcurrente(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		tb.Fatalf("APP_DATABASE_URL es obligatoria bajo el tag integration: sin Postgres real el test es un hallazgo, no un skip")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parsear DSN: %v", err)
	}
	cfg.MaxConns = 4
	cfg.MinIdleConns = 0

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		tb.Fatalf("abrir pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		tb.Fatalf("ping: %v", err)
	}
	return pool
}

// sembrarPendiente crea una parte y su credencial pendiente, con la antigüedad pedida.
func sembrarPendiente(tb testing.TB, pool *pgxpool.Pool, creadaHace time.Duration) (uuid.UUID, string, string) {
	tb.Helper()

	id := uuid.New()
	parte := uuid.New()
	correo := id.String() + "@centinela.test"
	celular := "+57" + strings.ReplaceAll(id.String(), "-", "")

	_, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(parte), Tipo: "persona"}); err != nil {
				return struct{}{}, err
			}
			if _, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
				ID:            pgUUID(id),
				ParteID:       pgUUID(parte),
				Correo:        correo,
				Celular:       celular,
				ValorDerivado: pgtype.Text{String: "$argon2id$hash", Valid: true},
				Estado:        string(EstadoPendienteVerificacion),
			}); err != nil {
				return struct{}{}, err
			}
			if creadaHace > 0 {
				if _, err := tx.Exec(ctx, "UPDATE auth.credenciales SET creada_en = now() - ($1::double precision * interval '1 second') WHERE id = $2", creadaHace.Seconds(), pgUUID(id)); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		})
	if err != nil {
		tb.Fatalf("sembrar pendiente: %v", err)
	}
	return id, correo, celular
}

// leerEstado lee el estado de una credencial como rol_auth y libera la conexión del pool.
func leerEstado(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) string {
	tb.Helper()

	tx, _ := abrirTransaccionDePrueba(tb, pool)
	var estado string
	err := tx.QueryRow(tb.Context(), "SELECT estado FROM auth.credenciales WHERE id = $1", pgUUID(id)).Scan(&estado)
	_ = tx.Rollback(tb.Context())
	if err != nil {
		tb.Fatalf("leer estado: %v", err)
	}
	return estado
}

// contarTransiciones cuenta las filas de historial de una credencial y libera la conexión del pool.
func contarTransiciones(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) int {
	tb.Helper()

	tx, _ := abrirTransaccionDePrueba(tb, pool)
	var total int
	err := tx.QueryRow(tb.Context(), "SELECT count(*) FROM auth.transiciones_credencial WHERE credencial_id = $1", pgUUID(id)).Scan(&total)
	_ = tx.Rollback(tb.Context())
	if err != nil {
		tb.Fatalf("contar transiciones: %v", err)
	}
	return total
}

// leerUltimaTransicion lee (anterior, nuevo, motivo) de la última fila de historial y libera la conexión.
func leerUltimaTransicion(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) (string, string, string) {
	tb.Helper()

	tx, _ := abrirTransaccionDePrueba(tb, pool)
	var anterior, nuevo, motivo string
	err := tx.QueryRow(tb.Context(),
		"SELECT coalesce(estado_anterior, ''), estado_nuevo, coalesce(motivo, '') FROM auth.transiciones_credencial WHERE credencial_id = $1 ORDER BY id DESC LIMIT 1",
		pgUUID(id)).Scan(&anterior, &nuevo, &motivo)
	_ = tx.Rollback(tb.Context())
	if err != nil {
		tb.Fatalf("leer transición: %v", err)
	}
	return anterior, nuevo, motivo
}

func TestTransicionPermitidaEscribeElHistorial(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	id, _, _ := sembrarPendiente(t, pool, 0)
	gestor := NuevoGestorDeEstados(RegistradorNulo{})

	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, gestor.TransicionarCredencial(ctx, q, id, EstadoPendienteVerificacion, EstadoActivo, "celular_verificado")
		}); err != nil {
		t.Fatalf("transicionar: %v", err)
	}

	if got := leerEstado(t, pool, id); got != string(EstadoActivo) {
		t.Errorf("estado = %q, quiero %q", got, EstadoActivo)
	}
	if total := contarTransiciones(t, pool, id); total != 1 {
		t.Errorf("transiciones = %d, quiero 1", total)
	}
	anterior, nuevo, motivo := leerUltimaTransicion(t, pool, id)
	if anterior != string(EstadoPendienteVerificacion) || nuevo != string(EstadoActivo) || motivo != "celular_verificado" {
		t.Errorf("transición = (%q, %q, %q), quiero (%q, %q, celular_verificado)", anterior, nuevo, motivo, EstadoPendienteVerificacion, EstadoActivo)
	}
}

func TestTransicionNoPermitidaNoTocaNada(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	id, _, _ := sembrarPendiente(t, pool, 0)
	gestor := NuevoGestorDeEstados(RegistradorNulo{})

	_, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, gestor.TransicionarCredencial(ctx, q, id, EstadoPendienteVerificacion, EstadoSuspendido, "no_permitida")
		})
	if !errors.Is(err, ErrTransicionNoPermitida) {
		t.Fatalf("error = %v, quiero %v", err, ErrTransicionNoPermitida)
	}
	if got := leerEstado(t, pool, id); got != string(EstadoPendienteVerificacion) {
		t.Errorf("estado = %q, quiero %q", got, EstadoPendienteVerificacion)
	}
	if total := contarTransiciones(t, pool, id); total != 0 {
		t.Errorf("transiciones = %d, quiero 0", total)
	}
}

func TestCompareAndSetSoloUnaActivacionGana(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	id, _, _ := sembrarPendiente(t, pool, 0)
	gestor := NuevoGestorDeEstados(RegistradorNulo{})

	resultados := make([]error, 2)
	var wg sync.WaitGroup
	for i := range resultados {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := enTransaccion(context.Background(), poolDeAuth{pool: pool},
				func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
					return struct{}{}, gestor.TransicionarCredencial(ctx, q, id, EstadoPendienteVerificacion, EstadoActivo, "celular_verificado")
				})
			resultados[i] = err
		}(i)
	}
	wg.Wait()

	ganadas := 0
	for _, err := range resultados {
		switch {
		case err == nil:
			ganadas++
		case errors.Is(err, ErrConflictoDeEstado):
		default:
			t.Errorf("error inesperado: %v", err)
		}
	}
	if ganadas != 1 {
		t.Errorf("activaciones ganadoras = %d, quiero 1", ganadas)
	}
	if got := leerEstado(t, pool, id); got != string(EstadoActivo) {
		t.Errorf("estado = %q, quiero %q", got, EstadoActivo)
	}
	if total := contarTransiciones(t, pool, id); total != 1 {
		t.Errorf("transiciones = %d, quiero 1", total)
	}
}

func TestJobCierraLaVencidaRespetaLaRecienteYLiberaLosIdentificadores(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	vencida, correo, celular := sembrarPendiente(t, pool, 2*time.Hour)
	reciente, _, _ := sembrarPendiente(t, pool, 0)
	gestor := NuevoGestorDeEstados(RegistradorNulo{})

	if _, err := gestor.CerrarPendientesVencidas(t.Context(), pool, time.Hour); err != nil {
		t.Fatalf("cerrar pendientes: %v", err)
	}

	if got := leerEstado(t, pool, vencida); got != string(EstadoCerrado) {
		t.Errorf("estado de la vencida = %q, quiero %q", got, EstadoCerrado)
	}
	if got := leerEstado(t, pool, reciente); got != string(EstadoPendienteVerificacion) {
		t.Errorf("estado de la reciente = %q, quiero %q", got, EstadoPendienteVerificacion)
	}
	if total := contarTransiciones(t, pool, vencida); total != 1 {
		t.Errorf("transiciones de la vencida = %d, quiero 1", total)
	}
	_, nuevo, motivo := leerUltimaTransicion(t, pool, vencida)
	if nuevo != string(EstadoCerrado) || motivo != MotivoPendienteVencida {
		t.Errorf("transición de la vencida = (%q, %q), quiero (%q, %q)", nuevo, motivo, EstadoCerrado, MotivoPendienteVencida)
	}

	_, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			id := uuid.New()
			parte := uuid.New()
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(parte), Tipo: "persona"}); err != nil {
				return struct{}{}, err
			}
			_, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
				ID:            pgUUID(id),
				ParteID:       pgUUID(parte),
				Correo:        correo,
				Celular:       celular,
				ValorDerivado: pgtype.Text{String: "$argon2id$hash", Valid: true},
				Estado:        string(EstadoPendienteVerificacion),
			})
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("no se pudo registrar con el correo y el celular liberados: %v", err)
	}
}

func TestCarreraJobContraActivacionDejaUnaSolaTransicion(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	id, _, _ := sembrarPendiente(t, pool, 2*time.Hour)
	gestor := NuevoGestorDeEstados(RegistradorNulo{})

	var wg sync.WaitGroup
	var errActivacion, errJob error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errActivacion = enTransaccion(context.Background(), poolDeAuth{pool: pool},
			func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
				return struct{}{}, gestor.TransicionarCredencial(ctx, q, id, EstadoPendienteVerificacion, EstadoActivo, "celular_verificado")
			})
	}()
	go func() {
		defer wg.Done()
		_, errJob = gestor.CerrarPendientesVencidas(context.Background(), pool, time.Hour)
	}()
	wg.Wait()

	if errJob != nil {
		t.Errorf("error del job = %v", errJob)
	}
	if errActivacion != nil && !errors.Is(errActivacion, ErrConflictoDeEstado) {
		t.Errorf("error de la activación = %v", errActivacion)
	}
	estado := leerEstado(t, pool, id)
	if estado != string(EstadoActivo) && estado != string(EstadoCerrado) {
		t.Errorf("estado final = %q, quiero activo o cerrado", estado)
	}
	if total := contarTransiciones(t, pool, id); total != 1 {
		t.Errorf("transiciones = %d, quiero 1", total)
	}
}

func TestRegistrarTransicionInicialDejaElAnteriorNulo(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	id, _, _ := sembrarPendiente(t, pool, 0)

	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, RegistrarTransicion(ctx, q, id, "", EstadoPendienteVerificacion, "alta")
		}); err != nil {
		t.Fatalf("registrar transición inicial: %v", err)
	}

	anterior, nuevo, motivo := leerUltimaTransicion(t, pool, id)
	if anterior != "" || nuevo != string(EstadoPendienteVerificacion) || motivo != "alta" {
		t.Errorf("transición = (%q, %q, %q), quiero vacío, %q y alta", anterior, nuevo, motivo, EstadoPendienteVerificacion)
	}
}
