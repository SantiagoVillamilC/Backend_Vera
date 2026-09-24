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

// HU-NF-08, criterio 7: dos Conciliar() concurrentes sobre la misma diferencia de monto distinto no
// duplican la fila ni tumban la corrida perdedora, igual que el criterio 10.3 ya protege extracto_sin_libro.
func TestHU_NF_08_Criterio7DosConciliarConcurrentesSobreElMismoMontoDistintoNoDuplican(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	for i := 0; i < iteracionesDeCarrera; i++ {
		correrIteracionDeMontoDistintoConcurrente(t, ctx, pool, i)
	}
}

// correrIteracionDeMontoDistintoConcurrente siembra una diferencia de monto y lanza dos Conciliar() a la vez.
func correrIteracionDeMontoDistintoConcurrente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, i int) {
	t.Helper()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(300000 + int64(i))

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaDespachada, monto, base.Add(time.Minute), false)
	transferenciaID := uuid.UUID(id.Bytes)
	extracto := ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: transferenciaID, Estado: estadoExtractoConfirmada, Monto: monto + 1, OcurridoEn: base.Add(2 * time.Minute),
	}}}
	fijarCursor(t, pool, desde)

	servicioA := nuevoServicioDeCarreraDeResolucion(t, ctx, pool, hasta, extracto)
	servicioB := nuevoServicioDeCarreraDeResolucion(t, ctx, pool, hasta, extracto)
	errA, errB, diferencias := competirDosConciliar(ctx, servicioA, servicioB)

	if errA != nil {
		t.Fatalf("iteración %d: Conciliar (A) no debe fallar ante la carrera del monto distinto de %s: %v", i, transferenciaID, errA)
	}
	if errB != nil {
		t.Fatalf("iteración %d: Conciliar (B) no debe fallar ante la carrera del monto distinto de %s: %v", i, transferenciaID, errB)
	}
	if diferencias != 1 {
		t.Errorf("iteración %d: diferencias sumadas = %d, quiero exactamente 1", i, diferencias)
	}
	if filas := contarDescuadres(t, ctx, pool, TipoDescuadreMontoDistinto, transferenciaID.String()); filas != 1 {
		t.Errorf("iteración %d: descuadres de monto distinto de %s = %d, quiero exactamente 1, nunca duplicado", i, transferenciaID, filas)
	}
	if transferencia := leerTransferenciaDeResolucion(t, ctx, pool, id); transferencia.Estado != EstadoTransferenciaDespachada {
		t.Errorf("iteración %d: estado = %q, quiero despachada sin automatizar", i, transferencia.Estado)
	}
	if len(asientosDeTransferencia(t, ctx, pool, id)) != 0 {
		t.Errorf("iteración %d: el monto distinto no debe producir asiento", i)
	}
}

// HU-NF-08, criterios 22 y 8.3: dos Conciliar() concurrentes sobre el mismo estado final contradicho
// no duplican la fila ni revierten el cierre ya escrito.
func TestHU_NF_08_Criterio22DosConciliarConcurrentesSobreElMismoEstadoFinalContradichoNoDuplican(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	for i := 0; i < iteracionesDeCarrera; i++ {
		correrIteracionDeEstadoFinalContradichoConcurrente(t, ctx, pool, i)
	}
}

// correrIteracionDeEstadoFinalContradichoConcurrente siembra un cierre ya asentado y lo contradice desde el extracto.
func correrIteracionDeEstadoFinalContradichoConcurrente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, i int) {
	t.Helper()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(310000 + int64(i))

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaConfirmada, monto, base.Add(time.Minute), false)
	sembrarAsientoDeConfirmacionDeResolucion(t, ctx, pool, id, base.Add(time.Minute))
	antes := len(asientosDeTransferencia(t, ctx, pool, id))
	transferenciaID := uuid.UUID(id.Bytes)

	extracto := ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: transferenciaID, Estado: estadoExtractoRechazada, Monto: monto, OcurridoEn: base.Add(2 * time.Minute),
	}}}
	fijarCursor(t, pool, desde)

	servicioA := nuevoServicioDeCarreraDeResolucion(t, ctx, pool, hasta, extracto)
	servicioB := nuevoServicioDeCarreraDeResolucion(t, ctx, pool, hasta, extracto)
	errA, errB, diferencias := competirDosConciliar(ctx, servicioA, servicioB)

	if errA != nil {
		t.Fatalf("iteración %d: Conciliar (A) no debe fallar ante la carrera del estado final contradicho de %s: %v", i, transferenciaID, errA)
	}
	if errB != nil {
		t.Fatalf("iteración %d: Conciliar (B) no debe fallar ante la carrera del estado final contradicho de %s: %v", i, transferenciaID, errB)
	}
	if diferencias != 1 {
		t.Errorf("iteración %d: diferencias sumadas = %d, quiero exactamente 1", i, diferencias)
	}
	if filas := contarDescuadres(t, ctx, pool, TipoDescuadreEstadoFinalContradicho, transferenciaID.String()); filas != 1 {
		t.Errorf("iteración %d: descuadres de estado final contradicho de %s = %d, quiero exactamente 1, nunca duplicado", i, transferenciaID, filas)
	}
	if transferencia := leerTransferenciaDeResolucion(t, ctx, pool, id); transferencia.Estado != EstadoTransferenciaConfirmada {
		t.Errorf("iteración %d: estado = %q, quiero confirmada sin revertir", i, transferencia.Estado)
	}
	if despues := len(asientosDeTransferencia(t, ctx, pool, id)); despues != antes {
		t.Errorf("iteración %d: asientos = %d, quiero %d: no se revierte el cierre ya escrito", i, despues, antes)
	}
}

// HU-NF-08, criterio 23: dos Conciliar() concurrentes sobre la misma diferencia de custodia no la duplican.
func TestHU_NF_08_Criterio23DosConciliarConcurrentesSobreLaMismaDiferenciaDeCustodiaNoDuplican(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	for i := 0; i < iteracionesDeCarrera; i++ {
		correrIteracionDeCustodiaConcurrente(t, ctx, pool, i)
	}
}

// correrIteracionDeCustodiaConcurrente desfasa el extracto de custodia y corre dos Conciliar() a la vez,
// cada una sobre su propio corte (hasta1 y hasta2), para que el choque sea por la identidad fija de
// referenciaCustodia y no quede enmascarado por la llave primaria de extracto_custodio (mismo corte).
func correrIteracionDeCustodiaConcurrente(t *testing.T, ctx context.Context, pool *pgxpool.Pool, i int) {
	t.Helper()
	resolverDescuadresDeCustodiaAbiertos(t, ctx, pool)
	base := baseUnicaDeResolucion()
	desde := base
	hasta1, hasta2 := base.Add(ventanaDeResolucion), base.Add(2*ventanaDeResolucion)
	fijarCursor(t, pool, desde)

	interno := saldoDeCustodiaHasta(t, ctx, pool, hasta1)
	externo, err := interno.Sumar(Centavos(70000 + int64(i)))
	if err != nil {
		t.Fatalf("iteración %d: sumar el desfase de custodia: %v", i, err)
	}
	custodioA := &custodioFalso{extracto: ExtractoCustodio{Saldo: externo, Origen: "prueba"}}
	custodioB := &custodioFalso{extracto: ExtractoCustodio{Saldo: externo, Origen: "prueba"}}

	servicioA := NewServicio(pool, nil, nil, custodioA, &rastreadorFalso{}, ConLectorExtractoRiel(&rielFalso{}))
	servicioB := NewServicio(pool, nil, nil, custodioB, &rastreadorFalso{}, ConLectorExtractoRiel(&rielFalso{}))
	servicioA.ahora = func() time.Time { return hasta1.Add(ventanaDeResolucion) }
	servicioB.ahora = func() time.Time { return hasta2.Add(ventanaDeResolucion) }
	errA, errB, diferencias := competirDosConciliar(ctx, servicioA, servicioB)

	if errA != nil {
		t.Fatalf("iteración %d: Conciliar (A) no debe fallar ante la carrera de custodia: %v", i, errA)
	}
	if errB != nil {
		t.Fatalf("iteración %d: Conciliar (B) no debe fallar ante la carrera de custodia: %v", i, errB)
	}
	if diferencias != 1 {
		t.Errorf("iteración %d: diferencias sumadas = %d, quiero exactamente 1", i, diferencias)
	}
	if filas := contarDescuadresAbiertos(t, ctx, pool, TipoDescuadreCustodiaSaldoDistinto, referenciaCustodia); filas != 1 {
		t.Errorf("iteración %d: descuadres abiertos de custodia = %d, quiero exactamente 1, nunca duplicado", i, filas)
	}
}

// nuevoServicioDeCarreraDeResolucion arma un Servicio que cuadra la custodia, para aislar la diferencia bajo prueba.
func nuevoServicioDeCarreraDeResolucion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hasta time.Time, extracto ExtractoRiel) *Servicio {
	t.Helper()
	servicio := NewServicio(pool, nil, nil, custodioQueCuadra(t, ctx, pool, hasta), &rastreadorFalso{}, ConLectorExtractoRiel(&rielFalso{extracto: extracto}))
	servicio.ahora = func() time.Time { return hasta.Add(ventanaDeResolucion) }
	return servicio
}

// sembrarAsientoDeConfirmacionDeResolucion escribe el 4a que un estado final contradicho no debe revertir.
func sembrarAsientoDeConfirmacionDeResolucion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID, ocurridoEn time.Time) {
	t.Helper()
	cierre := leerTransferenciaDeResolucion(t, ctx, pool, id)
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, escribirAsientoDeConfirmacion(ctx, q, cierre, ocurridoEn)
		}); err != nil {
		t.Fatalf("sembrar el 4a: %v", err)
	}
}

// competirDosConciliar suelta dos Conciliar() a la vez con una barrera real y devuelve sus errores y la suma de diferencias.
func competirDosConciliar(ctx context.Context, servicioA, servicioB *Servicio) (errA, errB error, diferencias int) {
	inicio := make(chan struct{})
	var wg sync.WaitGroup
	var nA, nB int
	wg.Add(2)
	go func() { defer wg.Done(); <-inicio; nA, errA = servicioA.Conciliar(ctx, ventanaDeResolucion) }()
	go func() { defer wg.Done(); <-inicio; nB, errB = servicioB.Conciliar(ctx, ventanaDeResolucion) }()
	close(inicio)
	wg.Wait()
	return errA, errB, nA + nB
}
