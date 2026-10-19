//go:build integration

package nucleo

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Escenario C, HU-NF-10 criterios 16 y 17 y HU-NF-08 criterio 9.1: una indeterminada que compite
// entre una respuesta tardía del riel (el camino del outbox) y la conciliación cierra una sola vez.
func TestCierreCruzadoOutboxYConciliacionSobreIndeterminadaCierranUnaVez(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	for i := 0; i < iteracionesDeCarrera; i++ {
		correrIteracionDeCierreCruzado(t, ctx, pool, i)
	}
}

// correrIteracionDeCierreCruzado siembra una indeterminada y lanza el trabajador y la conciliación a la vez.
func correrIteracionDeCierreCruzado(t *testing.T, ctx context.Context, pool *pgxpool.Pool, i int) {
	t.Helper()
	resolverDescuadresDeCustodiaAbiertos(t, ctx, pool)
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(150000 + int64(i))
	evento := base.Add(2 * time.Minute)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
	transferenciaID := uuid.UUID(id.Bytes)
	outboxID := sembrarOutboxDespachado(t, ctx, pool, id)

	reclamada := despachoReclamado{
		outbox:          leerOutbox(t, ctx, pool, outboxID),
		transferencia:   leerTransferenciaDeResolucion(t, ctx, pool, id),
		transferenciaID: transferenciaID,
	}
	// La respuesta tardía concuerda en el desenlace con el extracto, pero con otra marca de tiempo,
	// para poder saber cuál de los dos caminos escribió el asiento si algún día empatan.
	tardia := &rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoConfirmado, OcurridoEn: evento.Add(-time.Minute)}}
	trabajador := NewServicio(pool, nil, tardia, nil, &rastreadorFalso{})

	extracto := extractoDeCierre(id, estadoExtractoConfirmada, monto, evento)
	// El 4a que la conciliación va a escribir en esta misma corrida saca el monto de la custodia:
	// el extracto externo se fija ya restándolo, como en el criterio 9.1 (custodioConDesfase).
	custodio := custodioConDesfase(t, ctx, pool, hasta, -monto)
	fijarCursor(t, pool, desde)
	conciliador := NewServicio(pool, nil, nil, custodio, &rastreadorFalso{}, ConLectorExtractoRiel(&rielFalso{extracto: extracto}))
	conciliador.ahora = func() time.Time { return hasta.Add(ventanaDeResolucion) }

	diferencias := competirCierreCruzado(t, ctx, i, trabajador, conciliador, reclamada)
	if diferencias != 1 {
		t.Fatalf("iteración %d: Conciliar registró %d diferencias, quiero exactamente 1 (la de estado distinto)", i, diferencias)
	}
	verificarCierreUnicoDeIndeterminada(t, ctx, pool, id, transferenciaID, evento)
}

// competirCierreCruzado suelta a la vez el cierre tardío del trabajador y la conciliación, con una barrera real.
func competirCierreCruzado(t *testing.T, ctx context.Context, i int, trabajador, conciliador *Servicio, reclamada despachoReclamado) int {
	t.Helper()
	inicio := make(chan struct{})
	var wg sync.WaitGroup
	var errTrabajador, errConciliador error
	var diferencias int
	wg.Add(2)
	go func() { defer wg.Done(); <-inicio; errTrabajador = trabajador.entregarAlRiel(ctx, reclamada) }()
	go func() {
		defer wg.Done()
		<-inicio
		diferencias, errConciliador = conciliador.Conciliar(ctx, ventanaDeResolucion)
	}()
	close(inicio)
	wg.Wait()
	if errTrabajador != nil {
		t.Fatalf("iteración %d, cierre del trabajador: %v", i, errTrabajador)
	}
	if errConciliador != nil {
		t.Fatalf("iteración %d, Conciliar: %v", i, errConciliador)
	}
	return diferencias
}

// verificarCierreUnicoDeIndeterminada exige un solo asiento de cierre y la liberación completa de la operación.
func verificarCierreUnicoDeIndeterminada(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID, transferenciaID uuid.UUID, evento time.Time) {
	t.Helper()
	confirmaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada)
	compensaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoCompensacion)
	if confirmaciones+compensaciones != 1 {
		t.Fatalf("cierres de %s = %d (4a=%d, 4b=%d), quiero exactamente 1", transferenciaID, confirmaciones+compensaciones, confirmaciones, compensaciones)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado de %s = %q, quiero confirmada", transferenciaID, estado)
	}
	if suma := sumaDeCuentaDeLaTransferencia(t, ctx, pool, transferenciaID, CuentaLiquidacionPendienteRiel); suma != 0 {
		t.Errorf("liquidación pendiente de %s = %d, quiero 0: se libera una sola vez", transferenciaID, suma)
	}
	if filas := contarDescuadresAbiertosDeTransferencia(t, ctx, pool, transferenciaID); filas != 0 {
		t.Errorf("descuadres abiertos de %s = %d, quiero 0: la conciliación resolvió su hallazgo", transferenciaID, filas)
	}
	cierre := asientoDeTipo(t, asientosDeTransferencia(t, ctx, pool, id), TipoAsientoLiquidacionConfirmada)
	if !cierre.OcurridoEn.Valid || !cierre.OcurridoEn.Time.UTC().Equal(evento) {
		t.Errorf("ocurrido_en del cierre de %s = %v, quiero el de la conciliación %v: hoy siempre gana ese camino", transferenciaID, cierre.OcurridoEn.Time, evento)
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// Escenario D2, HU-NF-08 criterio 10.3: dos corridas de Conciliar() sobre la misma ventana no
// duplican ni el cierre ni la diferencia que compite por el índice único de descuadre, sin importar
// el orden en que cada corrida encuentra la diferencia protegida por CAS y la escalada sin CAS.
func TestHU_NF_08_Criterio10_3DosConciliarConcurrentesSobreLaMismaVentanaNoDuplican(t *testing.T) {
	casos := []struct {
		nombre          string
		huerfanoPrimero bool
	}{
		{"huerfano_primero", true},
		{"indeterminada_primero", false},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			pool := abrirPoolConcurrenteDePrueba(t)
			ctx := t.Context()
			for i := 0; i < iteracionesDeCarrera; i++ {
				correrIteracionDeConciliarConcurrente(t, ctx, pool, i, caso.huerfanoPrimero)
			}
		})
	}
}

// correrIteracionDeConciliarConcurrente arma una indeterminada más un huérfano del extracto y corre Conciliar() dos veces a la vez.
func correrIteracionDeConciliarConcurrente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, i int, huerfanoPrimero bool) {
	t.Helper()
	resolverDescuadresDeCustodiaAbiertos(t, ctx, pool)
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(200000 + int64(i))
	evento := base.Add(2 * time.Minute)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
	transferenciaID := uuid.UUID(id.Bytes)

	huerfano := uuid.New()
	huerfanoOp := OperacionDeExtractoRiel{TransferenciaID: huerfano, Estado: estadoExtractoConfirmada, Monto: Centavos(999), OcurridoEn: evento}
	indeterminadaOp := OperacionDeExtractoRiel{TransferenciaID: transferenciaID, Estado: estadoExtractoRechazada, Monto: monto, OcurridoEn: evento}
	operaciones := []OperacionDeExtractoRiel{indeterminadaOp, huerfanoOp}
	if huerfanoPrimero {
		operaciones = []OperacionDeExtractoRiel{huerfanoOp, indeterminadaOp}
	}
	extracto := ExtractoRiel{Operaciones: operaciones}
	fijarCursor(t, pool, desde)

	servicioA := NewServicio(pool, nil, nil, custodioQueCuadra(t, ctx, pool, hasta), &rastreadorFalso{}, ConLectorExtractoRiel(&rielFalso{extracto: extracto}))
	servicioB := NewServicio(pool, nil, nil, custodioQueCuadra(t, ctx, pool, hasta), &rastreadorFalso{}, ConLectorExtractoRiel(&rielFalso{extracto: extracto}))
	servicioA.ahora = func() time.Time { return hasta.Add(ventanaDeResolucion) }
	servicioB.ahora = func() time.Time { return hasta.Add(ventanaDeResolucion) }

	inicio := make(chan struct{})
	var wg sync.WaitGroup
	var errA, errB error
	var nA, nB int
	wg.Add(2)
	go func() { defer wg.Done(); <-inicio; nA, errA = servicioA.Conciliar(ctx, ventanaDeResolucion) }()
	go func() { defer wg.Done(); <-inicio; nB, errB = servicioB.Conciliar(ctx, ventanaDeResolucion) }()
	close(inicio)
	wg.Wait()

	verificarResultadoDeConciliarConcurrente(t, ctx, pool, i, transferenciaID, huerfano, hasta, errA, errB, nA+nB)
}

// verificarResultadoDeConciliarConcurrente exige el mismo resultado final que una sola corrida, sin importar quién ganó.
func verificarResultadoDeConciliarConcurrente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, i int, transferenciaID, huerfano uuid.UUID, hasta time.Time, errA, errB error, diferencias int) {
	t.Helper()
	if errA != nil {
		t.Fatalf("iteración %d: Conciliar (A) no debe fallar ante la carrera del huérfano %s: %v", i, huerfano, errA)
	}
	if errB != nil {
		t.Fatalf("iteración %d: Conciliar (B) no debe fallar ante la carrera del huérfano %s: %v", i, huerfano, errB)
	}
	if diferencias != 2 {
		t.Errorf("iteración %d: diferencias sumadas de las dos corridas = %d, quiero exactamente 2 (una por hallazgo real), como una sola corrida", i, diferencias)
	}

	confirmaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada)
	compensaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoCompensacion)
	if confirmaciones+compensaciones != 1 {
		t.Fatalf("iteración %d: cierres = %d (4a=%d, 4b=%d), quiero exactamente 1", i, confirmaciones+compensaciones, confirmaciones, compensaciones)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaCompensada {
		t.Errorf("iteración %d: estado = %q, quiero compensada", i, estado)
	}
	if filas := contarDescuadres(t, ctx, pool, TipoDescuadreEstadoDistinto, transferenciaID.String()); filas != 1 {
		t.Errorf("iteración %d: descuadres de estado distinto = %d, quiero exactamente 1", i, filas)
	}
	if filas := contarDescuadres(t, ctx, pool, TipoDescuadreExtractoSinLibro, huerfano.String()); filas != 1 {
		t.Errorf("iteración %d: descuadres del huérfano %s = %d, quiero exactamente 1, nunca duplicado", i, huerfano, filas)
	}
	if cursor := ultimaVentanaHastaDePrueba(t, ctx, pool); !cursor.Equal(hasta) {
		t.Errorf("iteración %d: cursor tras la carrera = %v, quiero %v", i, cursor, hasta)
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// contarDescuadresAbiertosDeTransferencia cuenta las filas sin resolver de cualquier tipo ligadas a la transferencia.
func contarDescuadresAbiertosDeTransferencia(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) int64 {
	t.Helper()
	var filas int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT count(*) FROM descuadre WHERE transferencia_id = $1 AND resuelto_en IS NULL",
				pgtype.UUID{Bytes: id, Valid: true}).Scan(&filas)
		}); err != nil {
		t.Fatalf("contar descuadres abiertos de %s: %v", id, err)
	}
	return filas
}

// ultimaVentanaHastaDePrueba lee el cursor de conciliación tal como lo hace el propio Servicio.
func ultimaVentanaHastaDePrueba(t *testing.T, ctx context.Context, pool *pgxpool.Pool) time.Time {
	t.Helper()
	ultima, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (time.Time, error) {
			v, err := q.UltimaVentanaConciliada(ctx)
			return v.Time.UTC(), err
		})
	if err != nil {
		t.Fatalf("leer el cursor de conciliación: %v", err)
	}
	return ultima
}
