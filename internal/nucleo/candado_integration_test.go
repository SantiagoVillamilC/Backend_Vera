//go:build integration

package nucleo

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errSaldoInsuficienteDePrueba marca la reserva que no cubre el disponible del emisor.
var errSaldoInsuficienteDePrueba = errors.New("prueba: saldo insuficiente")

// abrirPoolConcurrenteDePrueba abre un pool con varias conexiones para las pruebas de concurrencia.
func abrirPoolConcurrenteDePrueba(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		tb.Skip("APP_DATABASE_URL no definida: la prueba de integración necesita un Postgres real")
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

// fondearDisponibleDePrueba acredita el disponible del emisor desde la cuenta ómnibus del banco.
func fondearDisponibleDePrueba(t *testing.T, pool *pgxpool.Pool, cuentaDisponible string, monto Centavos) {
	t.Helper()

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
				Tipo:        "prueba",
				OcurridoEn:  pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				Descripcion: pgtype.Text{String: "fondeo de prueba", Valid: true},
			})
			if err != nil {
				return struct{}{}, err
			}
			lineas := []repositorio.InsertarLineaParams{
				{EntryID: asiento.ID, CuentaID: CuentaCustodiaBanco, Monto: monto.Int64(), Moneda: MonedaCOP},
				{EntryID: asiento.ID, CuentaID: cuentaDisponible, Monto: -monto.Int64(), Moneda: MonedaCOP},
			}
			for _, linea := range lineas {
				if _, err := q.InsertarLinea(ctx, linea); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		})
	if err != nil {
		t.Fatalf("fondear %s: %v", cuentaDisponible, err)
	}
}

// intentarReservaDePrueba reserva con el candado antes del INSERT con FK, que si no asciende el bloqueo y muere en deadlock.
func intentarReservaDePrueba(ctx context.Context, pool *pgxpool.Pool, usuario, cuentaOrigen string, monto Centavos, momento time.Time) error {
	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			if err := bloquearOrdenadas(ctx, q, cuentaOrigen); err != nil {
				return struct{}{}, err
			}
			transferenciaID := uuid.New()
			if _, err := tx.Exec(ctx, `INSERT INTO nucleo.transferencia
				(id, usuario_id, cuenta_origen, llave_destino, monto, moneda, idempotency_key, estado, creada_en)
				VALUES ($1, $2, $3, $4, $5, 'COP', $6, $7, $8)`,
				transferenciaID, usuario, cuentaOrigen, "intra:"+uuid.NewString(), monto.Int64(),
				"k-"+uuid.NewString(), EstadoTransferenciaCreada, momento); err != nil {
				return struct{}{}, err
			}
			saldo, err := q.SaldoDeCuenta(ctx, cuentaOrigen)
			if err != nil {
				return struct{}{}, err
			}
			disponible, err := Centavos(0).Restar(Centavos(saldo))
			if err != nil {
				return struct{}{}, err
			}
			if disponible < monto {
				return struct{}{}, errSaldoInsuficienteDePrueba
			}
			gravamen, err := verificarLimite(ctx, q, usuario, NivelKYCDepositoBajoMonto, monto, momento)
			if err != nil {
				return struct{}{}, err
			}
			if err := retener(ctx, q, SolicitudRetencion{
				TransferenciaID: transferenciaID,
				UsuarioID:       usuario,
				Monto:           monto,
				Gravamen:        gravamen,
				Moneda:          MonedaCOP,
				OcurridoEn:      momento,
			}); err != nil {
				return struct{}{}, err
			}
			_, err = q.AvanzarEstadoTransferencia(ctx, repositorio.AvanzarEstadoTransferenciaParams{
				ID:             pgtype.UUID{Bytes: transferenciaID, Valid: true},
				EstadoEsperado: EstadoTransferenciaCreada,
				EstadoNuevo:    EstadoTransferenciaReservada,
			})
			return struct{}{}, err
		})
	return err
}

// TestDosReservasSimultaneasNoComprometenMasSaldo cubre el capítulo 12: con saldo para una sola, exactamente una pasa.
func TestDosReservasSimultaneasNoComprometenMasSaldo(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	momento := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	const repeticiones = 8
	for ronda := 0; ronda < repeticiones; ronda++ {
		usuario := uuid.NewString()
		cuenta := subcuentaDisponibleDePrueba(t, ctx, s, usuario)
		fondearDisponibleDePrueba(t, pool, cuenta, 1000)

		resultados := make(chan error, 2)
		for intento := 0; intento < 2; intento++ {
			go func() { resultados <- intentarReservaDePrueba(ctx, pool, usuario, cuenta, 1000, momento) }()
		}
		aprobadas, rechazadas := 0, 0
		for intento := 0; intento < 2; intento++ {
			switch err := <-resultados; {
			case err == nil:
				aprobadas++
			case errors.Is(err, errSaldoInsuficienteDePrueba):
				rechazadas++
			default:
				t.Fatalf("reserva %d de la ronda %d: %v", intento, ronda, err)
			}
		}
		if aprobadas != 1 || rechazadas != 1 {
			t.Fatalf("ronda %d: aprobadas = %d, rechazadas por saldo = %d, quiero 1 y 1", ronda, aprobadas, rechazadas)
		}
	}
	t.Logf("%d rondas de dos reservas simultáneas: siempre una aprobada y una rechazada por saldo", repeticiones)
}

// TestCandadoOrdenadoEvitaElInterbloqueoEnOrdenInverso compara el candado ordenado contra el orden de llegada.
func TestCandadoOrdenadoEvitaElInterbloqueoEnOrdenInverso(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	a := subcuentaDisponibleDePrueba(t, ctx, s, uuid.NewString())
	b := subcuentaDisponibleDePrueba(t, ctx, s, uuid.NewString())

	control := candadoPorOrdenDeLlegadaEnEspejo(ctx, pool, a, b)
	if muertos := contarDeadlocks(control); muertos != 1 {
		t.Fatalf("el control en espejo produjo %d deadlock(s) (%v), quiero exactamente 1", muertos, control)
	}

	ordenado := candadoOrdenadoEnPar(ctx, pool, a, b, b, a)
	if muertos := contarDeadlocks(ordenado); muertos != 0 {
		t.Fatalf("el candado ordenado produjo %d deadlock(s): %v", muertos, ordenado)
	}
	for i, err := range ordenado {
		if err != nil {
			t.Fatalf("transacción %d del candado ordenado: %v, quiero nil", i, err)
		}
	}
	t.Logf("el control en espejo murió por deadlock y el candado ordenado pasó las dos transacciones")
}

// contarDeadlocks cuenta los errores que Postgres marcó como deadlock detectado.
func contarDeadlocks(errs []error) int {
	muertos := 0
	for _, err := range errs {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.DeadlockDetected {
			muertos++
		}
	}
	return muertos
}

// candadoPorOrdenDeLlegadaEnEspejo toma la primera cuenta, espera a la otra transacción y luego toma la segunda.
func candadoPorOrdenDeLlegadaEnEspejo(ctx context.Context, pool *pgxpool.Pool, a, b string) []error {
	tomada := make(chan struct{}, 2)
	continuar := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	tomar := func(i int, primera, segunda string) {
		_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
			func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
				errPrimera := bloquearEnOrdenDeLlegada(ctx, tx, primera)
				tomada <- struct{}{}
				if errPrimera != nil {
					return struct{}{}, errPrimera
				}
				<-continuar
				return struct{}{}, bloquearEnOrdenDeLlegada(ctx, tx, segunda)
			})
		errs[i] = err
	}
	wg.Go(func() { tomar(0, a, b) })
	wg.Go(func() { tomar(1, b, a) })
	<-tomada
	<-tomada
	close(continuar)
	wg.Wait()
	return errs
}

// bloquearEnOrdenDeLlegada bloquea una sola cuenta con FOR UPDATE, sin ordenar.
func bloquearEnOrdenDeLlegada(ctx context.Context, tx transaccionDeNucleo, cuenta string) error {
	var id string
	return tx.QueryRow(ctx, "SELECT id FROM nucleo.cuenta WHERE id = $1 FOR UPDATE", cuenta).Scan(&id)
}

// candadoOrdenadoEnPar corre dos transacciones que llaman bloquearOrdenadas con las cuentas invertidas.
func candadoOrdenadoEnPar(ctx context.Context, pool *pgxpool.Pool, a, b, c, d string) []error {
	listo := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	correr := func(i int, cuentas ...string) {
		_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
			func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
				<-listo
				return struct{}{}, bloquearOrdenadas(ctx, q, cuentas...)
			})
		errs[i] = err
	}
	wg.Go(func() { correr(0, a, b) })
	wg.Go(func() { correr(1, c, d) })
	close(listo)
	wg.Wait()
	return errs
}

// TestCandadoAgotaLaEsperaConErrorControlado cubre el límite de 5 segundos: la que espera falla, no se cuelga.
func TestCandadoAgotaLaEsperaConErrorControlado(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	s := NewServicio(pool, nil, nil, nil, nil)
	cuenta := subcuentaDisponibleDePrueba(t, ctx, s, uuid.NewString())

	tomado := make(chan struct{})
	liberar := make(chan struct{})
	retenedora := make(chan error, 1)
	go func() {
		_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
			func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
				if err := bloquearOrdenadas(ctx, q, cuenta); err != nil {
					return struct{}{}, err
				}
				close(tomado)
				<-liberar
				return struct{}{}, nil
			})
		retenedora <- err
	}()

	<-tomado
	inicio := time.Now()
	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, bloquearOrdenadas(ctx, q, cuenta)
		})
	transcurrido := time.Since(inicio)
	close(liberar)
	if errRetenedora := <-retenedora; errRetenedora != nil {
		t.Fatalf("la transacción que retenía el candado: %v", errRetenedora)
	}

	if !errors.Is(err, ErrEsperaDeBloqueoAgotada) {
		t.Fatalf("la espera agotada = %v, quiero ErrEsperaDeBloqueoAgotada", err)
	}
	if transcurrido < esperaDeBloqueo || transcurrido > 10*time.Second {
		t.Errorf("la espera falló en %v, quiero entre %v y 10s", transcurrido, esperaDeBloqueo)
	}
	t.Logf("error literal tras %v: %v", transcurrido.Round(time.Millisecond), err)
}
