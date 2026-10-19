//go:build integration

package nucleo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sembrarIntencionEnvejecida inserta una intención con el estado y la antigüedad pedidos.
func sembrarIntencionEnvejecida(t *testing.T, pool *pgxpool.Pool, usuarioID, estado string, edad time.Duration) uuid.UUID {
	t.Helper()

	id := uuid.New()
	creada := pgtype.Timestamptz{Time: time.Now().UTC().Add(-edad), Valid: true}
	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `INSERT INTO nucleo.deposito
					(id, usuario_id, monto, moneda, referencia, idempotency_key, estado, creada_en, actualizada_en)
					VALUES ($1, $2, 100000, 'COP', $3, $4, $5, $6, $6)`,
				pgtype.UUID{Bytes: id, Valid: true}, usuarioID, "dep-"+id.String(), "k-"+id.String(), estado, creada)
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("sembrar intención: %v", err)
	}
	return id
}

// estadoDeIntencion lee el estado actual de una intención sembrada.
func estadoDeIntencion(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()

	var estado string
	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, "SELECT estado FROM nucleo.deposito WHERE id = $1", pgtype.UUID{Bytes: id, Valid: true}).Scan(&estado)
		})
	if err != nil {
		t.Fatalf("leer estado: %v", err)
	}
	return estado
}

// contarPendientesVencidas cuenta las pendientes más viejas que el corte, con el reloj de Postgres.
func contarPendientesVencidas(t *testing.T, pool *pgxpool.Pool, segundos float64) int64 {
	t.Helper()

	var total int64
	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT count(*) FROM nucleo.deposito WHERE estado = 'pendiente' AND creada_en < now() - make_interval(secs => $1)",
				segundos).Scan(&total)
		})
	if err != nil {
		t.Fatalf("contar pendientes vencidas: %v", err)
	}
	return total
}

// marcarEstadoDeIntencion mueve el estado sin pasar por el servicio, para preparar el caso tardío.
func marcarEstadoDeIntencion(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, estado string) {
	t.Helper()

	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE nucleo.deposito SET estado = $2, actualizada_en = now() WHERE id = $1",
				pgtype.UUID{Bytes: id, Valid: true}, estado)
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("marcar estado: %v", err)
	}
}

// contarAsientosDeIntencion cuenta los asientos del diario ligados a una intención.
func contarAsientosDeIntencion(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int64 {
	t.Helper()

	var total int64
	_, err := enTransaccion(t.Context(), poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT count(*) FROM nucleo.journal_entry WHERE deposito_id = $1",
				pgtype.UUID{Bytes: id, Valid: true}).Scan(&total)
		})
	if err != nil {
		t.Fatalf("contar asientos: %v", err)
	}
	return total
}

// HU-NF-07 Criterio 18: el barrido expira las pendientes vencidas y respeta las recientes y las confirmadas.
func TestHU_NF_07_Criterio18_ExpiraSoloLasPendientesVencidas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NewServicio(pool, nil, nil, nil, nil)
	usuarioID := uuid.NewString()
	const ttl = 5 * time.Minute

	vencida := sembrarIntencionEnvejecida(t, pool, usuarioID, EstadoDepositoPendiente, time.Hour)
	reciente := sembrarIntencionEnvejecida(t, pool, usuarioID, EstadoDepositoPendiente, time.Second)
	confirmada := sembrarIntencionEnvejecida(t, pool, usuarioID, EstadoDepositoConfirmada, time.Hour)

	esperadas := contarPendientesVencidas(t, pool, ttl.Seconds())
	expiradas, err := servicio.ExpirarIntenciones(t.Context(), ttl)
	if err != nil {
		t.Fatalf("ExpirarIntenciones: %v", err)
	}
	if int64(expiradas) != esperadas {
		t.Errorf("expiradas = %d, quiero %d (las pendientes vencidas del momento)", expiradas, esperadas)
	}

	if estado := estadoDeIntencion(t, pool, vencida); estado != EstadoDepositoExpirada {
		t.Errorf("intención vencida = %q, quiero %q", estado, EstadoDepositoExpirada)
	}
	if estado := estadoDeIntencion(t, pool, reciente); estado != EstadoDepositoPendiente {
		t.Errorf("intención reciente = %q, quiero %q", estado, EstadoDepositoPendiente)
	}
	if estado := estadoDeIntencion(t, pool, confirmada); estado != EstadoDepositoConfirmada {
		t.Errorf("intención confirmada = %q, quiero %q (el job no la toca)", estado, EstadoDepositoConfirmada)
	}
}

// HU-NF-07 Criterio 18: con el plazo apagado el barrido no expira ninguna intención vencida.
func TestHU_NF_07_Criterio18_ConPlazoApagadoNadaExpira(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	servicio := NewServicio(pool, nil, nil, nil, nil)
	usuarioID := uuid.NewString()

	vencida := sembrarIntencionEnvejecida(t, pool, usuarioID, EstadoDepositoPendiente, time.Hour)

	expiradas, err := servicio.ExpirarIntenciones(t.Context(), 0)
	if err != nil {
		t.Fatalf("ExpirarIntenciones(0): %v", err)
	}
	if expiradas != 0 {
		t.Errorf("expiradas = %d con el proceso apagado, quiero 0", expiradas)
	}
	if estado := estadoDeIntencion(t, pool, vencida); estado != EstadoDepositoPendiente {
		t.Errorf("estado = %q con el proceso apagado, quiero %q", estado, EstadoDepositoPendiente)
	}
}

// HU-NF-07 Criterio 19: una confirmación tardía se registra y escala por el puerto y no escribe asiento.
func TestHU_NF_07_Criterio19_ConfirmacionTardiaEscalaSinAsiento(t *testing.T) {
	for _, estado := range []string{EstadoDepositoRechazada, EstadoDepositoExpirada} {
		t.Run(estado, func(t *testing.T) {
			pool := abrirPoolDePrueba(t)
			falso := &rastreadorFalso{}
			servicio := NewServicio(pool, nil, nil, nil, falso)
			usuarioID := uuid.NewString()
			intencion, _, err := servicio.CrearIntencionDeposito(t.Context(), usuarioID, 100000, "COP", "k-"+uuid.NewString())
			if err != nil {
				t.Fatalf("crear la intención: %v", err)
			}
			marcarEstadoDeIntencion(t, pool, intencion.ID, estado)

			_, err = servicio.ConfirmarDeposito(t.Context(), ConfirmacionDeposito{
				Referencia: intencion.Referencia,
				Monto:      100000,
				Moneda:     "COP",
				OcurridoEn: time.Now().UTC(),
			})
			if !errors.Is(err, ErrConfirmacionTardia) {
				t.Fatalf("ConfirmarDeposito = %v, quiero ErrConfirmacionTardia", err)
			}

			if len(falso.capturas) != 1 {
				t.Fatalf("capturas = %d, quiero 1 (registrar y escalar)", len(falso.capturas))
			}
			captura := falso.capturas[0]
			if captura.contexto.Operacion != "confirmacion_tardia" {
				t.Errorf("operacion = %q, quiero confirmacion_tardia", captura.contexto.Operacion)
			}
			if !tieneCampo(captura.contexto.Campos, "referencia", intencion.Referencia) {
				t.Errorf("campos = %v, quiero la referencia %q", captura.contexto.Campos, intencion.Referencia)
			}
			if asientos := contarAsientosDeIntencion(t, pool, intencion.ID); asientos != 0 {
				t.Errorf("asientos = %d, quiero 0 (una confirmación tardía no acredita)", asientos)
			}
			if actual := estadoDeIntencion(t, pool, intencion.ID); actual != estado {
				t.Errorf("estado = %q, quiero %q", actual, estado)
			}
		})
	}
}
