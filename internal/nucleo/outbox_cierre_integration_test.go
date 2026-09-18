//go:build integration

package nucleo

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// reclamarParaPrueba deja la transferencia despachada con su asiento 3 y devuelve el despacho reclamado.
func reclamarParaPrueba(t *testing.T, ctx context.Context, pool *pgxpool.Pool, outboxID int64) despachoReclamado {
	t.Helper()

	reclamada, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (despachoReclamado, error) {
			fila, err := q.ObtenerOutbox(ctx, outboxID)
			if err != nil {
				return despachoReclamado{}, err
			}
			return reclamarDespacho(ctx, q, fila)
		})
	if err != nil {
		t.Fatalf("reclamar el despacho %d: %v", outboxID, err)
	}
	return reclamada
}

// leerTransferenciaDePrueba devuelve la fila cruda de una transferencia.
func leerTransferenciaDePrueba(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) repositorio.Transferencia {
	t.Helper()

	fila, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (repositorio.Transferencia, error) {
			return q.ObtenerTransferencia(ctx, pgtype.UUID{Bytes: id, Valid: true})
		})
	if err != nil {
		t.Fatalf("leer la transferencia %s: %v", id, err)
	}
	return fila
}

// lineasDelAsiento devuelve el monto por cuenta de un asiento de la transferencia.
func lineasDelAsiento(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, tipo string) map[string]int64 {
	t.Helper()

	lineas := map[string]int64{}
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx,
				`SELECT p.cuenta_id, p.monto FROM nucleo.posting p
				   JOIN nucleo.journal_entry j ON j.id = p.entry_id
				  WHERE j.transferencia_id = $1 AND j.tipo = $2`,
				pgtype.UUID{Bytes: id, Valid: true}, tipo)
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
		t.Fatalf("leer las líneas del asiento %q de %s: %v", tipo, id, err)
	}
	return lineas
}

// sumaDeCuentaDeLaTransferencia suma las líneas de la transferencia en una sola cuenta.
func sumaDeCuentaDeLaTransferencia(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, cuenta string) int64 {
	t.Helper()

	var suma int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT COALESCE(SUM(p.monto), 0)::bigint FROM nucleo.posting p
				   JOIN nucleo.journal_entry j ON j.id = p.entry_id
				  WHERE j.transferencia_id = $1 AND p.cuenta_id = $2`,
				pgtype.UUID{Bytes: id, Valid: true}, cuenta).Scan(&suma)
		}); err != nil {
		t.Fatalf("sumar %s de la transferencia %s: %v", cuenta, id, err)
	}
	return suma
}

// descripcionDelAsiento devuelve el texto conservado en la cabecera de un asiento.
func descripcionDelAsiento(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, tipo string) string {
	t.Helper()

	var descripcion string
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT COALESCE(descripcion, '') FROM nucleo.journal_entry
				  WHERE transferencia_id = $1 AND tipo = $2`,
				pgtype.UUID{Bytes: id, Valid: true}, tipo).Scan(&descripcion)
		}); err != nil {
		t.Fatalf("leer la descripción del asiento %q de %s: %v", tipo, id, err)
	}
	return descripcion
}

// verificarAsientosCuadradosDeLaTransferencia corre el invariante 1 del Apéndice C acotado a una transferencia.
func verificarAsientosCuadradosDeLaTransferencia(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()

	var descuadrados, cortos int64
	_, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			idParam := pgtype.UUID{Bytes: id, Valid: true}
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM (
					SELECT entry_id FROM nucleo.posting
					 WHERE entry_id IN (SELECT id FROM nucleo.journal_entry WHERE transferencia_id = $1)
					 GROUP BY entry_id HAVING SUM(monto) <> 0) x`, idParam).Scan(&descuadrados); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, tx.QueryRow(ctx, `
				SELECT count(*) FROM (
					SELECT entry_id FROM nucleo.posting
					 WHERE entry_id IN (SELECT id FROM nucleo.journal_entry WHERE transferencia_id = $1)
					 GROUP BY entry_id HAVING count(*) < 2) x`, idParam).Scan(&cortos)
		})
	if err != nil {
		t.Fatalf("Apéndice C de %s: %v", id, err)
	}
	if descuadrados != 0 || cortos != 0 {
		t.Errorf("Apéndice C de %s: descuadrados = %d, con menos de dos líneas = %d; quiero 0 y 0", id, descuadrados, cortos)
	}
}

// Criterio 14 y asiento 4a: el riel confirma, el dinero en vuelo sale del custodio y liquidacion_pendiente vuelve a cero.
func TestCierreConfirmadoAsienta4aYSacaElDineroEnVuelo(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, monto)

	riel := &rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoConfirmado, OcurridoEn: time.Now().UTC()}}
	servicio := NewServicio(pool, nil, riel, nil, nil)
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}

	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado = %q, quiero %q", estado, EstadoTransferenciaConfirmada)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada); asientos != 1 {
		t.Errorf("asientos 4a = %d, quiero 1", asientos)
	}
	if fila := leerOutbox(t, ctx, pool, outboxID); !fila.DespachadoEn.Valid {
		t.Errorf("el outbox no quedó despachado")
	}
	if suma := sumaDeCuentaDeLaTransferencia(t, ctx, pool, transferenciaID, CuentaLiquidacionPendienteRiel); suma != 0 {
		t.Errorf("liquidación pendiente de la operación = %d, quiero 0", suma)
	}
	lineas := lineasDelAsiento(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada)
	if lineas[CuentaLiquidacionPendienteRiel] != monto.Int64() {
		t.Errorf("línea de liquidación pendiente = %d, quiero %d", lineas[CuentaLiquidacionPendienteRiel], monto.Int64())
	}
	if lineas[CuentaCustodiaBanco] != -monto.Int64() {
		t.Errorf("línea del custodio = %d, quiero %d", lineas[CuentaCustodiaBanco], -monto.Int64())
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// Criterio 15 y asiento 4b: el riel rechaza, el dinero vuelve al disponible del emisor y el motivo queda conservado.
func TestCierreRechazadoAsienta4bYDevuelveElDineroYConservaElMotivo(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, monto)

	const motivo = "fondos_insuficientes"
	riel := &rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoRechazado, Motivo: motivo, OcurridoEn: time.Now().UTC()}}
	servicio := NewServicio(pool, nil, riel, nil, nil)
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}

	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaCompensada {
		t.Errorf("estado = %q, quiero %q", estado, EstadoTransferenciaCompensada)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoCompensacion); asientos != 1 {
		t.Errorf("asientos 4b = %d, quiero 1", asientos)
	}
	if fila := leerOutbox(t, ctx, pool, outboxID); !fila.DespachadoEn.Valid {
		t.Errorf("el outbox no quedó despachado")
	}
	if suma := sumaDeCuentaDeLaTransferencia(t, ctx, pool, transferenciaID, CuentaLiquidacionPendienteRiel); suma != 0 {
		t.Errorf("liquidación pendiente de la operación = %d, quiero 0", suma)
	}
	lineas := lineasDelAsiento(t, ctx, pool, transferenciaID, TipoAsientoCompensacion)
	if lineas[CuentaLiquidacionPendienteRiel] != monto.Int64() {
		t.Errorf("línea de liquidación pendiente = %d, quiero %d", lineas[CuentaLiquidacionPendienteRiel], monto.Int64())
	}
	if lineas[NombreCuentaDisponible(usuario)] != -monto.Int64() {
		t.Errorf("línea del disponible del emisor = %d, quiero %d", lineas[NombreCuentaDisponible(usuario)], -monto.Int64())
	}
	if saldo := saldoDeCuentaDePrueba(t, servicio, NombreCuentaDisponible(usuario)); saldo != monto {
		t.Errorf("disponible del emisor tras la compensación = %d, quiero %d de vuelta", saldo, monto)
	}
	if saldo := saldoDeCuentaDePrueba(t, servicio, NombreCuentaRetenido(usuario)); saldo != 0 {
		t.Errorf("retenido del emisor tras la compensación = %d, quiero 0", saldo)
	}
	if conservado := descripcionDelAsiento(t, ctx, pool, transferenciaID, TipoAsientoCompensacion); conservado != motivo {
		t.Errorf("motivo conservado en el 4b = %q, quiero %q", conservado, motivo)
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// Criterio 16 y asiento 4c: el plazo vence, no hay asiento nuevo, la transferencia queda indeterminada y el dinero sigue en vuelo.
func TestTiempoVencidoDejaIndeterminadaSinAsientoYConElDineroEnVuelo(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, monto)

	riel := &rielDoble{err: fmt.Errorf("riel: %w", context.DeadlineExceeded)}
	servicio := NewServicio(pool, nil, riel, nil, nil)
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}

	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaIndeterminada {
		t.Errorf("estado = %q, quiero %q", estado, EstadoTransferenciaIndeterminada)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada); asientos != 0 {
		t.Errorf("asientos 4a tras el vencimiento = %d, quiero 0", asientos)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoCompensacion); asientos != 0 {
		t.Errorf("asientos 4b tras el vencimiento = %d, quiero 0", asientos)
	}
	if fila := leerOutbox(t, ctx, pool, outboxID); !fila.DespachadoEn.Valid {
		t.Errorf("el outbox no quedó despachado tras el vencimiento")
	}
	if suma := sumaDeCuentaDeLaTransferencia(t, ctx, pool, transferenciaID, CuentaLiquidacionPendienteRiel); suma != -monto.Int64() {
		t.Errorf("liquidación pendiente de la operación = %d, quiero %d en vuelo", suma, -monto.Int64())
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// Criterio 17: dos cierres concurrentes del mismo despacho dejan exactamente un asiento.
func TestDosCierresConcurrentesDejanUnSoloAsiento(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, monto)
	reclamada := reclamarParaPrueba(t, ctx, pool, outboxID)

	servicio := NewServicio(pool, nil, &rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoConfirmado, OcurridoEn: time.Now().UTC()}}, nil, nil)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { errs[i] = servicio.entregarAlRiel(ctx, reclamada) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("cierre concurrente %d: %v", i, err)
		}
	}

	confirmaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada)
	compensaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoCompensacion)
	if confirmaciones+compensaciones != 1 {
		t.Errorf("asientos de cierre = %d (4a=%d, 4b=%d), quiero exactamente 1", confirmaciones+compensaciones, confirmaciones, compensaciones)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado = %q, quiero %q", estado, EstadoTransferenciaConfirmada)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Errorf("asientos 3 = %d, quiero 1", asientos)
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// Criterio 17: una respuesta tardía sobre una transferencia ya cerrada no escribe un segundo asiento.
func TestRespuestaTardiaTrasElCierreNoDuplicaElAsiento(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, monto)

	primero := &rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoConfirmado, OcurridoEn: time.Now().UTC()}}
	servicio := NewServicio(pool, nil, primero, nil, nil)
	if err := servicio.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaConfirmada {
		t.Fatalf("estado tras el primer cierre = %q, quiero %q", estado, EstadoTransferenciaConfirmada)
	}

	reclamada := despachoReclamado{
		outbox:          leerOutbox(t, ctx, pool, outboxID),
		transferencia:   leerTransferenciaDePrueba(t, ctx, pool, transferenciaID),
		transferenciaID: transferenciaID,
	}
	tardio := &rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoConfirmado, OcurridoEn: time.Now().UTC()}}
	servicioTardio := NewServicio(pool, nil, tardio, nil, nil)
	if err := servicioTardio.entregarAlRiel(ctx, reclamada); err != nil {
		t.Fatalf("respuesta tardía: %v", err)
	}

	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada); asientos != 1 {
		t.Errorf("asientos 4a tras la respuesta tardía = %d, quiero 1", asientos)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoDespachoRiel); asientos != 1 {
		t.Errorf("asientos 3 tras la respuesta tardía = %d, quiero 1", asientos)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado tras la respuesta tardía = %q, quiero %q", estado, EstadoTransferenciaConfirmada)
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// Criterios 13 y 16: el error de transporte reintenta sin cerrar y el vencimiento del plazo sí cierra.
func TestElTransporteReintentaYElVencimientoCierra(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	transferenciaID, outboxID := sembrarReservaConOutbox(t, ctx, pool, usuario, monto)

	caido := NewServicio(pool, nil, &rielDoble{err: errors.New("connection refused")}, nil, nil)
	if err := caido.DespacharOutbox(ctx); err == nil {
		t.Errorf("con el riel caído el barrido debe devolver el error del envío")
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaDespachada {
		t.Errorf("estado tras el error de transporte = %q, quiero %q", estado, EstadoTransferenciaDespachada)
	}
	if asientos := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoCompensacion); asientos != 0 {
		t.Errorf("asientos 4b tras el error de transporte = %d, quiero 0", asientos)
	}
	if fila := leerOutbox(t, ctx, pool, outboxID); fila.DespachadoEn.Valid {
		t.Errorf("una entrega fallida no debe marcar despachado_en")
	}

	vencido := NewServicio(pool, nil, &rielDoble{err: fmt.Errorf("riel: %w", context.DeadlineExceeded)}, nil, nil)
	adelantarVentanaAlPasado(t, ctx, pool, outboxID)
	if err := vencido.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox con el plazo vencido: %v", err)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaIndeterminada {
		t.Errorf("estado tras el vencimiento = %q, quiero %q", estado, EstadoTransferenciaIndeterminada)
	}
	if fila := leerOutbox(t, ctx, pool, outboxID); !fila.DespachadoEn.Valid {
		t.Errorf("el vencimiento debe cerrar la fila del outbox")
	}
	if suma := sumaDeCuentaDeLaTransferencia(t, ctx, pool, transferenciaID, CuentaLiquidacionPendienteRiel); suma != -monto.Int64() {
		t.Errorf("liquidación pendiente tras el vencimiento = %d, quiero %d", suma, -monto.Int64())
	}
}
