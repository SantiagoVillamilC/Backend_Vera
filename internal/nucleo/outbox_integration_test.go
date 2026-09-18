//go:build integration

package nucleo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// nombreVariableHijo es la variable que delata al subproceso del helper de muerte.
const nombreVariableHijo = "CENTINELA_OUTBOX_ID"

// marcaMuereEnElDespacho confirma que el hijo registró el despacho antes de morir.
const marcaMuereEnElDespacho = "despacho registrado; el proceso muere antes de llamar al riel"

// rielDoble es el doble de la interfaz Riel: cuenta llamadas, puede fallar y avisa al llamarse.
type rielDoble struct {
	mu       sync.Mutex
	llamadas int
	err      error
	alLlamar func()
}

// Enviar registra la llamada, avisa al observador y falla solo si se le pidió.
func (r *rielDoble) Enviar(context.Context, OrdenRiel) (RespuestaRiel, error) {
	r.mu.Lock()
	r.llamadas++
	alLlamar := r.alLlamar
	err := r.err
	r.mu.Unlock()
	if alLlamar != nil {
		alLlamar()
	}
	if err != nil {
		return RespuestaRiel{}, err
	}
	return RespuestaRiel{}, nil
}

// Llamadas devuelve cuántas veces se llamó al riel.
func (r *rielDoble) Llamadas() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.llamadas
}

// ConsultarEstado del doble no se usa en estas pruebas y devuelve sin registro.
func (r *rielDoble) ConsultarEstado(context.Context, uuid.UUID) (RespuestaRiel, error) {
	return RespuestaRiel{}, ErrRielSinRegistro
}

// sembrarReservaConOutbox deja una transferencia reservada, su retención y su fila de outbox en una sola transacción.
func sembrarReservaConOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool, usuario string, monto Centavos) (uuid.UUID, int64) {
	t.Helper()

	servicio := NewServicio(pool, nil, nil, nil, nil)
	cuenta := subcuentaDisponibleDePrueba(t, ctx, servicio, usuario)
	fondearDisponibleDePrueba(t, pool, cuenta, monto)

	transferenciaID := uuid.New()
	var outboxID int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.InsertarTransferencia(ctx, repositorio.InsertarTransferenciaParams{
				ID:             pgtype.UUID{Bytes: transferenciaID, Valid: true},
				UsuarioID:      usuario,
				CuentaOrigen:   cuenta,
				LlaveDestino:   "llave-" + uuid.NewString(),
				Monto:          monto.Int64(),
				Moneda:         MonedaCOP,
				IdempotencyKey: "k-" + uuid.NewString(),
				Estado:         EstadoTransferenciaReservada,
			}); err != nil {
				return struct{}{}, err
			}
			if err := retener(ctx, q, SolicitudRetencion{
				TransferenciaID: transferenciaID,
				UsuarioID:       usuario,
				Monto:           monto,
				Moneda:          MonedaCOP,
				OcurridoEn:      time.Now().UTC(),
			}); err != nil {
				return struct{}{}, err
			}
			id, err := encolarDespacho(ctx, q, transferenciaID)
			if err != nil {
				return struct{}{}, err
			}
			outboxID = id
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("sembrar reserva con outbox: %v", err)
	}
	return transferenciaID, outboxID
}

// contarAsientosDeTransferencia cuenta las cabeceras de asiento de un tipo ligadas a una transferencia.
func contarAsientosDeTransferencia(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, tipo string) int {
	t.Helper()

	var filas int
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT count(*) FROM nucleo.journal_entry WHERE transferencia_id = $1 AND tipo = $2",
				pgtype.UUID{Bytes: id, Valid: true}, tipo).Scan(&filas)
		}); err != nil {
		t.Fatalf("contar asientos de %s: %v", id, err)
	}
	return filas
}

// contarOutboxDeTransferencia cuenta las filas de outbox cuyo payload apunta a la transferencia.
func contarOutboxDeTransferencia(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()

	var filas int
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT count(*) FROM nucleo.outbox WHERE payload->>'transferencia_id' = $1",
				id.String()).Scan(&filas)
		}); err != nil {
		t.Fatalf("contar outbox de %s: %v", id, err)
	}
	return filas
}

// leerOutbox devuelve la fila cruda del outbox.
func leerOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) repositorio.Outbox {
	t.Helper()

	fila, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Outbox, error) {
			return q.ObtenerOutbox(ctx, id)
		})
	if err != nil {
		t.Fatalf("leer outbox %d: %v", id, err)
	}
	return fila
}

// leerEstadoTransferencia devuelve el estado persistido de una transferencia.
func leerEstadoTransferencia(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()

	fila, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Transferencia, error) {
			return q.ObtenerTransferencia(ctx, pgtype.UUID{Bytes: id, Valid: true})
		})
	if err != nil {
		t.Fatalf("leer transferencia %s: %v", id, err)
	}
	return fila.Estado
}

// lineasDelDespacho devuelve el monto por cuenta del asiento de despacho de una transferencia.
func lineasDelDespacho(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) map[string]int64 {
	t.Helper()

	lineas := map[string]int64{}
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx,
				`SELECT p.cuenta_id, p.monto FROM nucleo.posting p
				   JOIN nucleo.journal_entry j ON j.id = p.entry_id
				  WHERE j.transferencia_id = $1 AND j.tipo = $2`,
				pgtype.UUID{Bytes: id, Valid: true}, TipoAsientoDespachoRiel)
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var cuenta string
				var monto int64
				if err := rows.Scan(&cuenta, &monto); err != nil {
					return struct{}{}, err
				}
				lineas[cuenta] = monto
			}
			return struct{}{}, rows.Err()
		}); err != nil {
		t.Fatalf("leer las líneas del despacho de %s: %v", id, err)
	}
	return lineas
}

// adelantarVentanaAlPasado deja la fila pendiente de inmediato para simular que la ventana venció.
func adelantarVentanaAlPasado(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) {
	t.Helper()

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			filas, err := q.AvanzarVentanaOutbox(ctx, repositorio.AvanzarVentanaOutboxParams{
				ID:        id,
				Intervalo: pgtype.Interval{Microseconds: -(time.Minute).Microseconds(), Valid: true},
			})
			if err != nil {
				return struct{}{}, err
			}
			if filas != 1 {
				return struct{}{}, fmt.Errorf("el outbox %d no quedó adelantado", id)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("adelantar la ventana del outbox %d: %v", id, err)
	}
}

// ahoraDeLaBase lee el reloj de Postgres, la referencia con que el outbox agenda sus ventanas.
func ahoraDeLaBase(t *testing.T, ctx context.Context, pool *pgxpool.Pool) time.Time {
	t.Helper()

	var ahora time.Time
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, "SELECT now()").Scan(&ahora)
		}); err != nil {
		t.Fatalf("leer now() de la base: %v", err)
	}
	return ahora
}

// neutralizarOutboxDePrueba aleja la fila cien años para que el barrido no la retome en corridas posteriores.
func neutralizarOutboxDePrueba(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) {
	t.Helper()

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			filas, err := q.AvanzarVentanaOutbox(ctx, repositorio.AvanzarVentanaOutboxParams{
				ID:        id,
				Intervalo: pgtype.Interval{Months: 1200, Valid: true},
			})
			if err != nil {
				return struct{}{}, err
			}
			if filas != 1 {
				return struct{}{}, fmt.Errorf("el outbox %d no quedó neutralizado", id)
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("neutralizar el outbox %d: %v", id, err)
	}
}

// TestLaFilaDeOutboxYLaRetencionEntranEnLaMismaTransaccion cubre el Criterio 10 con una falla inyectada.
func TestLaFilaDeOutboxYLaRetencionEntranEnLaMismaTransaccion(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	servicio := NewServicio(pool, nil, nil, nil, nil)

	reservar := func(usuario string, monto Centavos, fallar bool) uuid.UUID {
		t.Helper()
		cuenta := subcuentaDisponibleDePrueba(t, ctx, servicio, usuario)
		fondearDisponibleDePrueba(t, pool, cuenta, monto)
		transferenciaID := uuid.New()
		_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
			func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
				if _, err := q.InsertarTransferencia(ctx, repositorio.InsertarTransferenciaParams{
					ID:             pgtype.UUID{Bytes: transferenciaID, Valid: true},
					UsuarioID:      usuario,
					CuentaOrigen:   cuenta,
					LlaveDestino:   "llave-" + uuid.NewString(),
					Monto:          monto.Int64(),
					Moneda:         MonedaCOP,
					IdempotencyKey: "k-" + uuid.NewString(),
					Estado:         EstadoTransferenciaReservada,
				}); err != nil {
					return struct{}{}, err
				}
				if err := retener(ctx, q, SolicitudRetencion{
					TransferenciaID: transferenciaID,
					UsuarioID:       usuario,
					Monto:           monto,
					Moneda:          MonedaCOP,
					OcurridoEn:      time.Now().UTC(),
				}); err != nil {
					return struct{}{}, err
				}
				if _, err := encolarDespacho(ctx, q, transferenciaID); err != nil {
					return struct{}{}, err
				}
				if fallar {
					return struct{}{}, errors.New("falla inyectada tras las dos escrituras")
				}
				return struct{}{}, nil
			})
		if fallar {
			if err == nil {
				t.Fatalf("la transacción con falla inyectada no falló")
			}
			return transferenciaID
		}
		if err != nil {
			t.Fatalf("reservar: %v", err)
		}
		return transferenciaID
	}

	confirmada := reservar(uuid.NewString(), Centavos(1000000), false)
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, confirmada, TipoAsientoRetencion); asientos != 1 {
		t.Errorf("asientos de la reserva confirmada = %d, quiero 1", asientos)
	}
	if filas := contarOutboxDeTransferencia(t, ctx, pool, confirmada); filas != 1 {
		t.Errorf("filas de outbox de la reserva confirmada = %d, quiero 1", filas)
	}

	descartada := reservar(uuid.NewString(), Centavos(1000000), true)
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, descartada, TipoAsientoRetencion); asientos != 0 {
		t.Errorf("asientos tras la falla inyectada = %d, quiero 0", asientos)
	}
	if filas := contarOutboxDeTransferencia(t, ctx, pool, descartada); filas != 0 {
		t.Errorf("filas de outbox tras la falla inyectada = %d, quiero 0", filas)
	}
	t.Logf("las dos escrituras entraron juntas y la falla inyectada no dejó ninguna")
}

// TestElDespachoSeRegistraAntesDeLlamarAlRiel cubre el Criterio 12: al llamar al riel ya está todo confirmado.
func TestElDespachoSeRegistraAntesDeLlamarAlRiel(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, monto)

	var estadoEnLaLlamada string
	var asientosEnLaLlamada int
	riel := &rielDoble{alLlamar: func() {
		estadoEnLaLlamada = leerEstadoTransferencia(t, ctx, pool, transferenciaID)
		asientosEnLaLlamada = contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel)
	}}
	servicio := NewServicio(pool, nil, riel, nil, nil)
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}

	if estadoEnLaLlamada != EstadoTransferenciaDespachada {
		t.Errorf("estado al llamar al riel = %q, quiero %q", estadoEnLaLlamada, EstadoTransferenciaDespachada)
	}
	if asientosEnLaLlamada != 1 {
		t.Errorf("asientos al llamar al riel = %d, quiero 1", asientosEnLaLlamada)
	}
	if fila := leerOutbox(t, ctx, pool, outboxID); !fila.DespachadoEn.Valid {
		t.Errorf("el outbox no quedó marcado como despachado tras una entrega correcta")
	}
	lineas := lineasDelDespacho(t, ctx, pool, transferenciaID)
	if lineas[NombreCuentaRetenido(usuario)] != monto.Int64() {
		t.Errorf("línea del retenido = %d, quiero %d", lineas[NombreCuentaRetenido(usuario)], monto.Int64())
	}
	if lineas[CuentaLiquidacionPendienteRiel] != -monto.Int64() {
		t.Errorf("línea de liquidación pendiente = %d, quiero %d", lineas[CuentaLiquidacionPendienteRiel], -monto.Int64())
	}
	t.Logf("al llamar al riel la transferencia ya estaba despachada con su asiento 3 escrito")
}

// TestProcesoQueMuereEnElDespacho es el hijo real: reclama el despacho y muere antes de llamar al riel.
func TestProcesoQueMuereEnElDespacho(t *testing.T) {
	outboxID := os.Getenv(nombreVariableHijo)
	if outboxID == "" {
		t.Skip("helper del subproceso: solo corre con " + nombreVariableHijo)
	}
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := context.Background()
	id, err := strconv.ParseInt(outboxID, 10, 64)
	if err != nil {
		t.Fatalf("id del outbox: %v", err)
	}
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			fila, err := q.ObtenerOutbox(ctx, id)
			if err != nil {
				return struct{}{}, err
			}
			_, err = reclamarDespacho(ctx, q, fila)
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("reclamar y morir: %v", err)
	}
	fmt.Println(marcaMuereEnElDespacho)
	os.Exit(1)
}

// TestElWorkerNoDuplicaElAsientoTrasUnaMuerteRegistrada cubre el Criterio 18 con una muerte real del proceso.
func TestElWorkerNoDuplicaElAsientoTrasUnaMuerteRegistrada(t *testing.T) {
	if os.Getenv(nombreVariableHijo) != "" {
		return
	}
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, Centavos(1000000))

	cmd := exec.Command(os.Args[0], "-test.run=^TestProcesoQueMuereEnElDespacho$", "-test.v")
	cmd.Env = append(os.Environ(), nombreVariableHijo+"="+strconv.FormatInt(outboxID, 10))
	salida, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("el subproceso debía morir con código distinto de cero; salida:\n%s", salida)
	}
	if !strings.Contains(string(salida), marcaMuereEnElDespacho) {
		t.Fatalf("el subproceso no llegó al punto de la muerte; salida:\n%s", salida)
	}

	fila := leerOutbox(t, ctx, pool, outboxID)
	if fila.DespachadoEn.Valid {
		t.Fatalf("tras la muerte la fila no debe estar entregada")
	}
	if fila.Intentos != 0 {
		t.Errorf("intentos tras la muerte = %d, quiero 0 (nadie registró un fallo)", fila.Intentos)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Fatalf("asientos tras el registro = %d, quiero 1", asientos)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaDespachada {
		t.Errorf("estado tras el registro = %q, quiero %q", estado, EstadoTransferenciaDespachada)
	}

	riel := &rielDoble{}
	servicio := NewServicio(pool, nil, riel, nil, nil)
	adelantarVentanaAlPasado(t, ctx, pool, outboxID)
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox tras la muerte: %v", err)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Errorf("asientos tras el reintento = %d, quiero 1 sin duplicado", asientos)
	}
	if riel.Llamadas() != 1 {
		t.Errorf("llamadas al riel = %d, quiero 1", riel.Llamadas())
	}
	if fila := leerOutbox(t, ctx, pool, outboxID); !fila.DespachadoEn.Valid {
		t.Errorf("el reintento no marcó la fila como despachada")
	}
	t.Logf("el proceso murió entre el registro y la llamada y el reinicio no duplicó el asiento de despacho")
}

// TestVariosWorkersDespachanCadaFilaUnaSolaVez cubre el barrido con FOR UPDATE SKIP LOCKED bajo -race.
func TestVariosWorkersDespachanCadaFilaUnaSolaVez(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	const filas = 12
	transferencias := make([]uuid.UUID, 0, filas)
	outboxes := make([]int64, 0, filas)
	for range filas {
		transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, uuid.NewString(), Centavos(1000000))
		transferencias = append(transferencias, transferenciaID)
		outboxes = append(outboxes, outboxID)
	}

	riel := &rielDoble{}
	servicio := NewServicio(pool, nil, riel, nil, nil)

	errs := make([]error, 4)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { errs[i] = servicio.DespacharOutbox(ctx) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d: %v", i, err)
		}
	}

	for i, transferenciaID := range transferencias {
		if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
			t.Errorf("transferencia %d: asientos = %d, quiero 1", i, asientos)
		}
		if fila := leerOutbox(t, ctx, pool, outboxes[i]); !fila.DespachadoEn.Valid {
			t.Errorf("outbox %d: no quedó despachado", outboxes[i])
		}
	}
	if llamadas := riel.Llamadas(); llamadas != filas {
		t.Errorf("llamadas al riel = %d, quiero %d (cada fila una sola vez)", llamadas, filas)
	}
	t.Logf("%d workers despacharon %d filas: un asiento y una llamada por fila", len(errs), filas)
}

// TestConElRielCaidoSeReintentaConEsperaCreciente cubre el Criterio 13.1 y la vuelta del riel.
func TestConElRielCaidoSeReintentaConEsperaCreciente(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, Centavos(1000000))

	caido := &rielDoble{err: errors.New("riel caido")}
	servicio := NewServicio(pool, nil, caido, nil, nil)

	antes := time.Now().UTC()
	if err := servicio.DespacharOutbox(ctx); err == nil {
		t.Errorf("con el riel caído el barrido debe devolver el error del envío")
	}
	fila := leerOutbox(t, ctx, pool, outboxID)
	if fila.Intentos != 1 {
		t.Errorf("intentos = %d, quiero 1", fila.Intentos)
	}
	if !fila.UltimoError.Valid || fila.UltimoError.String == "" {
		t.Errorf("ultimo_error no quedó registrado")
	}
	if fila.DespachadoEn.Valid {
		t.Errorf("una entrega fallida no debe marcar despachado_en")
	}
	if demora := fila.DisponibleEn.Time.Sub(antes); demora < 25*time.Second || demora > 40*time.Second {
		t.Errorf("la primera ventana quedó en %v, quiero cerca de 30s", demora)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Errorf("asientos tras el primer fallo = %d, quiero 1", asientos)
	}

	adelantarVentanaAlPasado(t, ctx, pool, outboxID)
	antesSegundo := time.Now().UTC()
	if err := servicio.DespacharOutbox(ctx); err == nil {
		t.Errorf("el segundo fallo también debe devolver error")
	}
	fila = leerOutbox(t, ctx, pool, outboxID)
	if fila.Intentos != 2 {
		t.Errorf("intentos = %d, quiero 2", fila.Intentos)
	}
	if demora := fila.DisponibleEn.Time.Sub(antesSegundo); demora < 55*time.Second || demora > 70*time.Second {
		t.Errorf("la segunda ventana quedó en %v, quiero cerca de 60s", demora)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Errorf("asientos tras el segundo fallo = %d, quiero 1", asientos)
	}

	bueno := &rielDoble{}
	servicio = NewServicio(pool, nil, bueno, nil, nil)
	adelantarVentanaAlPasado(t, ctx, pool, outboxID)
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox al volver el riel: %v", err)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Errorf("asientos al volver el riel = %d, quiero 1", asientos)
	}
	if llamadas := bueno.Llamadas(); llamadas != 1 {
		t.Errorf("llamadas al volver el riel = %d, quiero 1", llamadas)
	}
	fila = leerOutbox(t, ctx, pool, outboxID)
	if !fila.DespachadoEn.Valid {
		t.Errorf("al volver el riel la fila no quedó despachada")
	}
	if fila.Intentos != 2 {
		t.Errorf("intentos finales = %d, quiero 2 (los fallos previos no se borran)", fila.Intentos)
	}
	t.Logf("con el riel caído la ventana creció de 30s a 60s y al volver salió una sola vez")
}

// TestLaFilaIrrecuperableNoBloqueaElBarrido cubre H1: una fila con tipo desconocido no impide despachar la válida.
func TestLaFilaIrrecuperableNoBloqueaElBarrido(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	var idIrrecuperable int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"INSERT INTO nucleo.outbox (tipo, payload) VALUES ('desconocido', '{}'::jsonb) RETURNING id").Scan(&idIrrecuperable)
		}); err != nil {
		t.Fatalf("sembrar la fila irrecuperable: %v", err)
	}

	transferenciaID, outboxValido := sembrarReservaConOutbox(t, ctx, pool, uuid.NewString(), Centavos(1000000))
	if outboxValido <= idIrrecuperable {
		t.Fatalf("la fila válida (%d) debe ir después de la irrecuperable (%d) por id", outboxValido, idIrrecuperable)
	}

	antes := ahoraDeLaBase(t, ctx, pool)
	riel := &rielDoble{}
	servicio := NewServicio(pool, nil, riel, nil, nil)
	if err := servicio.DespacharOutbox(ctx); err == nil {
		t.Errorf("el barrido debe devolver el error de la fila irrecuperable")
	}

	mala := leerOutbox(t, ctx, pool, idIrrecuperable)
	if mala.Intentos != 1 {
		t.Errorf("intentos de la fila irrecuperable = %d, quiero 1", mala.Intentos)
	}
	if !mala.UltimoError.Valid || mala.UltimoError.String == "" {
		t.Errorf("la fila irrecuperable no guardó ultimo_error")
	}
	if mala.DespachadoEn.Valid {
		t.Errorf("la fila irrecuperable no debe quedar despachada")
	}
	if !mala.DisponibleEn.Time.After(antes) {
		t.Errorf("la ventana de la fila irrecuperable no se empujó: %v", mala.DisponibleEn.Time)
	}

	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaDespachada {
		t.Errorf("estado de la transferencia válida = %q, quiero %q", estado, EstadoTransferenciaDespachada)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Errorf("asientos de la transferencia válida = %d, quiero 1", asientos)
	}
	if fila := leerOutbox(t, ctx, pool, outboxValido); !fila.DespachadoEn.Valid {
		t.Errorf("la fila válida no quedó despachada")
	}
	if llamadas := riel.Llamadas(); llamadas != 1 {
		t.Errorf("llamadas al riel = %d, quiero 1", llamadas)
	}
	neutralizarOutboxDePrueba(t, ctx, pool, idIrrecuperable)
	t.Logf("la fila %d con tipo desconocido no bloqueó el despacho de la fila %d", idIrrecuperable, outboxValido)
}

// TestElArriendoTieneMargenYUsaElRelojDeLaBase cubre H2: arriendo con margen y ventanas medidas con now() de la base.
func TestElArriendoTieneMargenYUsaElRelojDeLaBase(t *testing.T) {
	if arriendoEntregaRiel <= plazoEntregaRiel {
		t.Fatalf("el arriendo %v no supera el plazo del riel %v", arriendoEntregaRiel, plazoEntregaRiel)
	}

	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	_, outboxArriendo := sembrarReservaConOutbox(t, ctx, pool, uuid.NewString(), Centavos(1000000))
	antesArriendo := ahoraDeLaBase(t, ctx, pool)
	servicio := NewServicio(pool, nil, &rielDoble{}, nil, nil)
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}
	filaArriendo := leerOutbox(t, ctx, pool, outboxArriendo)
	demoraArriendo := filaArriendo.DisponibleEn.Time.Sub(antesArriendo)
	if demoraArriendo < arriendoEntregaRiel-5*time.Second || demoraArriendo > arriendoEntregaRiel+5*time.Second {
		t.Errorf("arriendo medido desde now() de la base = %v, quiero cerca de %v", demoraArriendo, arriendoEntregaRiel)
	}

	_, outboxReintento := sembrarReservaConOutbox(t, ctx, pool, uuid.NewString(), Centavos(1000000))
	antesReintento := ahoraDeLaBase(t, ctx, pool)
	caido := NewServicio(pool, nil, &rielDoble{err: errors.New("riel caido")}, nil, nil)
	if err := caido.DespacharOutbox(ctx); err == nil {
		t.Errorf("con el riel caído el barrido debe fallar")
	}
	filaReintento := leerOutbox(t, ctx, pool, outboxReintento)
	demoraReintento := filaReintento.DisponibleEn.Time.Sub(antesReintento)
	if demoraReintento < 25*time.Second || demoraReintento > 40*time.Second {
		t.Errorf("ventana de reintento medida desde now() de la base = %v, quiero cerca de 30s", demoraReintento)
	}
	neutralizarOutboxDePrueba(t, ctx, pool, outboxReintento)
	t.Logf("arriendo %v > plazo %v; arriendo observado %v y reintento %v medidos con now() de la base",
		arriendoEntregaRiel, plazoEntregaRiel, demoraArriendo, demoraReintento)
}

// TestElIncrementoDeIntentosEsAtomico cubre H2: los incrementos concurrentes no se pierden.
func TestElIncrementoDeIntentosEsAtomico(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	_, outboxID := sembrarReservaConOutbox(t, ctx, pool, uuid.NewString(), Centavos(1000000))

	servicio := NewServicio(pool, nil, nil, nil, nil)
	const incrementos = 5
	errs := make([]error, incrementos)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() {
			_, _, errs[i] = servicio.registrarFalloOutbox(ctx, outboxID, errors.New("fallo concurrente"))
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("incremento %d: %v", i, err)
		}
	}
	fila := leerOutbox(t, ctx, pool, outboxID)
	if fila.Intentos != incrementos {
		t.Errorf("intentos tras %d incrementos concurrentes = %d, quiero %d", incrementos, fila.Intentos, incrementos)
	}
	neutralizarOutboxDePrueba(t, ctx, pool, outboxID)
	t.Logf("%d incrementos concurrentes dejaron intentos = %d", incrementos, fila.Intentos)
}
