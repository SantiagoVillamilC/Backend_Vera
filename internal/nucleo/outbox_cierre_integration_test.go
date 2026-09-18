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

// fondearComoDeposito acredita el disponible con un asiento tipado como depósito, para que el historial lo lea.
func fondearComoDeposito(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cuenta string, monto Centavos) {
	t.Helper()

	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
				Tipo:        TipoAsientoDeposito,
				OcurridoEn:  pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
				Descripcion: pgtype.Text{String: "fondeo de prueba", Valid: true},
			})
			if err != nil {
				return struct{}{}, err
			}
			lineas := []repositorio.InsertarLineaParams{
				{EntryID: asiento.ID, CuentaID: CuentaCustodiaBanco, Monto: monto.Int64(), Moneda: MonedaCOP},
				{EntryID: asiento.ID, CuentaID: cuenta, Monto: -monto.Int64(), Moneda: MonedaCOP},
			}
			for _, linea := range lineas {
				if _, err := q.InsertarLinea(ctx, linea); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("fondear %s como depósito: %v", cuenta, err)
	}
}

// reservarConGMFConOutbox deja la retención de cuatro líneas y su outbox sobre un disponible ya fondeado.
func reservarConGMFConOutbox(t *testing.T, ctx context.Context, pool *pgxpool.Pool, usuario string, monto, gravamen Centavos) (uuid.UUID, int64) {
	t.Helper()

	cuenta := NombreCuentaDisponible(usuario)
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
				Gravamen:        gravamen,
				Moneda:          MonedaCOP,
				OcurridoEn:      time.Now().UTC(),
			}); err != nil {
				return struct{}{}, err
			}
			id, err := encolarDespacho(ctx, q, transferenciaID, resolucionDePrueba())
			if err != nil {
				return struct{}{}, err
			}
			outboxID = id
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("reservar con GMF y outbox: %v", err)
	}
	return transferenciaID, outboxID
}

// lineasDelAsientoEnOrden devuelve las líneas de un asiento como slice, para no colapsar una cuenta repetida.
func lineasDelAsientoEnOrden(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, tipo string) []lineaDeAsiento {
	t.Helper()

	var lineas []lineaDeAsiento
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx,
				`SELECT p.cuenta_id, p.monto FROM nucleo.posting p
				   JOIN nucleo.journal_entry j ON j.id = p.entry_id
				  WHERE j.transferencia_id = $1 AND j.tipo = $2
				  ORDER BY p.id`,
				pgtype.UUID{Bytes: id, Valid: true}, tipo)
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var linea lineaDeAsiento
				if err := rows.Scan(&linea.cuenta, &linea.monto); err != nil {
					return struct{}{}, err
				}
				lineas = append(lineas, linea)
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
	if len(lineas) != 2 {
		t.Errorf("líneas del 4b sin GMF = %d, quiero 2", len(lineas))
	}
	if _, hayGravamen := lineas[CuentaGMFPorPagar]; hayGravamen {
		t.Errorf("el 4b sin GMF no debe traer línea de gravamen")
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// HU-NF-10, criterio 15.2: con GMF retenido, el rechazo devuelve el monto y el gravamen y deja el disponible donde empezó.
func TestCierreRechazadoConGMFDevuelveElMontoYElGravamen(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()
	usuario := uuid.NewString()
	monto := Centavos(1000000)
	gravamen := Centavos(4000)

	servicio := NewServicio(pool, nil, nil, nil, nil)
	cuenta := subcuentaDisponibleDePrueba(t, ctx, servicio, usuario)
	fondeo, err := monto.Sumar(gravamen)
	if err != nil {
		t.Fatalf("sumar el monto y el gravamen: %v", err)
	}
	fondearComoDeposito(t, ctx, pool, cuenta, fondeo)
	inicial := saldoDeCuentaDePrueba(t, servicio, cuenta)

	transferenciaID, outboxID := reservarConGMFConOutbox(t, ctx, pool, usuario, monto, gravamen)
	riel := &rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoRechazado, Motivo: "fondos_insuficientes", OcurridoEn: time.Now().UTC()}}
	rechazador := NewServicio(pool, nil, riel, nil, nil)
	if err := rechazador.DespacharOutbox(ctx); err != nil {
		t.Fatalf("DespacharOutbox: %v", err)
	}

	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaCompensada {
		t.Errorf("estado = %q, quiero %q", estado, EstadoTransferenciaCompensada)
	}
	if fila := leerOutbox(t, ctx, pool, outboxID); !fila.DespachadoEn.Valid {
		t.Errorf("el outbox no quedó despachado")
	}
	if saldo := saldoDeCuentaDePrueba(t, servicio, cuenta); saldo != inicial {
		t.Errorf("disponible del emisor tras la compensación = %d, quiero %d de vuelta", saldo, inicial)
	}
	if saldo := saldoDeCuentaDePrueba(t, servicio, NombreCuentaRetenido(usuario)); saldo != 0 {
		t.Errorf("retenido del emisor tras la compensación = %d, quiero 0", saldo)
	}
	if suma := sumaDeCuentaDeLaTransferencia(t, ctx, pool, transferenciaID, CuentaLiquidacionPendienteRiel); suma != 0 {
		t.Errorf("liquidación pendiente de la operación = %d, quiero 0", suma)
	}
	if suma := sumaDeCuentaDeLaTransferencia(t, ctx, pool, transferenciaID, CuentaGMFPorPagar); suma != 0 {
		t.Errorf("gravamen por pagar de la operación = %d, quiero 0", suma)
	}

	lineas := lineasDelAsientoEnOrden(t, ctx, pool, transferenciaID, TipoAsientoCompensacion)
	querer := []lineaDeAsiento{
		{CuentaLiquidacionPendienteRiel, monto.Int64()},
		{NombreCuentaDisponible(usuario), -monto.Int64()},
		{CuentaGMFPorPagar, gravamen.Int64()},
		{NombreCuentaDisponible(usuario), -gravamen.Int64()},
	}
	if len(lineas) != len(querer) {
		t.Fatalf("líneas del 4b con GMF = %d, quiero %d", len(lineas), len(querer))
	}
	var suma int64
	for i, linea := range lineas {
		if linea != querer[i] {
			t.Errorf("línea %d = %+v, quiero %+v", i, linea, querer[i])
		}
		suma += linea.monto
	}
	if suma != 0 {
		t.Errorf("suma del asiento 4b con GMF = %d, quiero 0", suma)
	}
	verificarHistorialDelGravamenDevuelto(t, servicio, usuario, monto, gravamen)
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
	verificarApendiceCSinFilas(t, pool)
}

// verificarHistorialDelGravamenDevuelto exige que el historial muestre el monto y el gravamen como dos devoluciones distintas.
func verificarHistorialDelGravamenDevuelto(t *testing.T, servicio *Servicio, usuario string, monto, gravamen Centavos) {
	t.Helper()

	pagina, err := servicio.Historial(t.Context(), usuario, nil, 50)
	if err != nil {
		t.Fatalf("Historial: %v", err)
	}
	var hayMonto, hayGravamen bool
	for _, movimiento := range pagina.Movimientos {
		for _, linea := range movimiento.Lineas {
			if linea.Motivo == MotivoDevolucion && linea.Monto == monto {
				hayMonto = true
			}
			if linea.Motivo == MotivoDevolucionGravamen && linea.Monto == gravamen {
				hayGravamen = true
			}
		}
	}
	if !hayMonto {
		t.Errorf("el historial no muestra la devolución del monto %d", monto)
	}
	if !hayGravamen {
		t.Errorf("el historial no muestra el gravamen devuelto %d como concepto propio", gravamen)
	}
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
