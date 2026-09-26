//go:build integration

package nucleo

import (
	"context"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// bloquearDepositoDePrueba toma el candado de fila de una intención sin tocar su estado.
func bloquearDepositoDePrueba(ctx context.Context, tx transaccionDeNucleo, id uuid.UUID) error {
	var encontrado pgtype.UUID
	return tx.QueryRow(ctx, "SELECT id FROM nucleo.deposito WHERE id = $1 FOR UPDATE",
		pgtype.UUID{Bytes: id, Valid: true}).Scan(&encontrado)
}

// contarEsperandoCandado cuenta los backends de la base de prueba bloqueados esperando un candado.
func contarEsperandoCandado(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var total int
	err := pool.QueryRow(ctx,
		"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'").
		Scan(&total)
	return total, err
}

// esperarDosCandadosEnEspera sondea hasta ver dos backends esperando el mismo candado, o falla si tarda más de 2s.
func esperarDosCandadosEnEspera(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	tope := time.Now().Add(2 * time.Second)
	for time.Now().Before(tope) {
		n, err := contarEsperandoCandado(context.Background(), pool)
		if err != nil {
			t.Fatalf("contar backends esperando candado: %v", err)
		}
		if n >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tras 2s no hay dos corridas del job esperando el mismo candado: la prueba no generó contención real")
}

// retenerCandadoDeDeposito abre una transacción que toma FOR UPDATE sobre la intención y no la suelta
// hasta que el liberador devuelto se invoque, o hasta que ctx se cancele.
func retenerCandadoDeDeposito(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) func() {
	t.Helper()

	tomado := make(chan struct{})
	liberar := make(chan struct{})
	retenedora := make(chan error, 1)
	go func() {
		_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
			func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
				if err := bloquearDepositoDePrueba(ctx, tx, id); err != nil {
					return struct{}{}, err
				}
				close(tomado)
				select {
				case <-liberar:
				case <-ctx.Done():
				}
				return struct{}{}, nil
			})
		retenedora <- err
	}()

	<-tomado
	return func() {
		close(liberar)
		if err := <-retenedora; err != nil {
			t.Fatalf("la transacción que retenía el candado: %v", err)
		}
	}
}

// resultadoExpiracion es lo que cada corrida concurrente del job devuelve.
type resultadoExpiracion struct {
	expiradas int
	err       error
}

// sumarExpiracionesConcurrentes lanza n corridas reales de ExpirarIntenciones, espera a que al menos
// dos queden bloqueadas en el mismo candado, libera y devuelve la suma de lo que cada una expiró.
func sumarExpiracionesConcurrentes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, servicio *Servicio, ttl time.Duration, liberarCandado func(), n int) int {
	t.Helper()

	resultados := make(chan resultadoExpiracion, n)
	for i := 0; i < n; i++ {
		go func() {
			expiradas, err := servicio.ExpirarIntenciones(ctx, ttl)
			resultados <- resultadoExpiracion{expiradas: expiradas, err: err}
		}()
	}

	esperarDosCandadosEnEspera(t, pool)
	liberarCandado()

	total := 0
	for i := 0; i < n; i++ {
		r := <-resultados
		if r.err != nil {
			t.Fatalf("corrida concurrente %d: %v", i, r.err)
		}
		total += r.expiradas
	}
	return total
}

// HU-NF-07 Criterio 18: dos corridas simultáneas del barrido no expiran la misma intención dos veces.
func TestHU_NF_07_Criterio18_DosCorridasConcurrentesNoExpiranLaMismaFilaDosVeces(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	servicio := NewServicio(pool, nil, nil, nil, nil)
	usuarioID := uuid.NewString()
	const ttl = time.Hour

	// Drena, con el mismo plazo de la carrera, cualquier pendiente vencida que haya quedado de una
	// corrida anterior contra esta base.
	if _, err := servicio.ExpirarIntenciones(ctx, ttl); err != nil {
		t.Fatalf("drenar deuda previa: %v", err)
	}

	vencida := sembrarIntencionEnvejecida(t, pool, usuarioID, EstadoDepositoPendiente, 48*time.Hour)
	esperadas := contarPendientesVencidas(t, pool, ttl.Seconds())
	if esperadas != 1 {
		t.Fatalf("pendientes vencidas antes de la carrera = %d, quiero exactamente 1", esperadas)
	}

	liberarCandado := retenerCandadoDeDeposito(t, ctx, pool, vencida)
	total := sumarExpiracionesConcurrentes(t, ctx, pool, servicio, ttl, liberarCandado, 2)

	if int64(total) != esperadas {
		t.Fatalf("suma de expiradas entre las dos corridas = %d, quiero %d (la fila no se procesa dos veces)", total, esperadas)
	}
	if estado := estadoDeIntencion(t, pool, vencida); estado != EstadoDepositoExpirada {
		t.Errorf("intención tras la carrera = %q, quiero %q", estado, EstadoDepositoExpirada)
	}
	if restantes := contarPendientesVencidas(t, pool, ttl.Seconds()); restantes != 0 {
		t.Errorf("pendientes vencidas tras la carrera = %d, quiero 0", restantes)
	}
}
