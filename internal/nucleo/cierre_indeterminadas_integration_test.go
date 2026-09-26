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

const iteracionesDeCarrera = 25

// asientoDeTipo devuelve el asiento del tipo pedido entre los de una transferencia.
func asientoDeTipo(t *testing.T, asientos []repositorio.JournalEntry, tipo string) repositorio.JournalEntry {
	t.Helper()
	for _, asiento := range asientos {
		if asiento.Tipo == tipo {
			return asiento
		}
	}
	t.Fatalf("no hay asiento %q entre %d asientos", tipo, len(asientos))
	return repositorio.JournalEntry{}
}

// corridaEnDeLaConciliacion devuelve la marca del servidor de la última corrida del cursor.
func corridaEnDeLaConciliacion(t *testing.T, ctx context.Context, pool *pgxpool.Pool) time.Time {
	t.Helper()
	var corridaEn time.Time
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, "SELECT corrida_en FROM conciliacion_corrida ORDER BY id DESC LIMIT 1").Scan(&corridaEn)
		}); err != nil {
		t.Fatalf("leer corrida_en: %v", err)
	}
	return corridaEn.UTC()
}

// sembrarOutboxDespachado inserta la fila de despacho ya entregada y devuelve su id.
func sembrarOutboxDespachado(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id pgtype.UUID) int64 {
	t.Helper()
	var outboxID int64
	if _, err := enTransaccion(ctx, poolDeNucleo{pool: pool},
		func(ctx context.Context, tx transaccionDeNucleo, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`INSERT INTO outbox (tipo, payload, despachado_en)
				 VALUES ('despacho_transferencia', jsonb_build_object('transferencia_id', $1::text), now())
				 RETURNING id`, id.String()).Scan(&outboxID)
		}); err != nil {
		t.Fatalf("sembrar el outbox despachado: %v", err)
	}
	return outboxID
}

// extractoDeCierre arma el extracto del riel con una sola operación para la transferencia.
func extractoDeCierre(id pgtype.UUID, estado string, monto Centavos, ocurridoEn time.Time) ExtractoRiel {
	return ExtractoRiel{Operaciones: []OperacionDeExtractoRiel{{
		TransferenciaID: uuid.UUID(id.Bytes), Estado: estado, Monto: monto, OcurridoEn: ocurridoEn,
	}}}
}

// custodioDelCierre devuelve el extracto del custodio que cuadra con la custodia interna al corte.
func custodioDelCierre(t *testing.T, ctx context.Context, pool *pgxpool.Pool, hasta time.Time) ExtractoCustodio {
	t.Helper()
	return ExtractoCustodio{CorteEn: hasta, Saldo: saldoDeCustodiaHasta(t, ctx, pool, hasta), Origen: "prueba"}
}

// correrCorridasSolapadas lanza las corridas a la vez sobre la misma ventana y espera a todas.
func correrCorridasSolapadas(ctx context.Context, servicios []*Servicio, desde, hasta time.Time, extractoRiel ExtractoRiel, extractoCustodio ExtractoCustodio) []error {
	inicio := make(chan struct{})
	errs := make([]error, len(servicios))
	var wg sync.WaitGroup
	for i, servicio := range servicios {
		wg.Add(1)
		go func(i int, servicio *Servicio) {
			defer wg.Done()
			<-inicio
			_, errs[i] = servicio.conciliarVentana(ctx, desde, hasta, extractoRiel, extractoCustodio)
		}(i, servicio)
	}
	close(inicio)
	wg.Wait()
	return errs
}

// HU-NF-08, criterios 9.1 y 11: la indeterminada que el extracto confirma cierra con el 4a y dos marcas.
func TestHU_NF_08_Criterio9_1Y11IndeterminadaConfirmadaCierraCon4aYDosMarcas(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(180000)
	evento := base.Add(2 * time.Minute)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
	riel := &rielFalso{extracto: extractoDeCierre(id, estadoExtractoConfirmada, monto, evento)}
	desfase, err := Centavos(0).Restar(monto)
	if err != nil {
		t.Fatalf("restar el efecto del 4a: %v", err)
	}
	custodio := custodioConDesfase(t, ctx, pool, hasta, desfase)

	if n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodio, &rastreadorFalso{}); n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	transferenciaID := uuid.UUID(id.Bytes)
	if estado := leerTransferenciaDeResolucion(t, ctx, pool, id).Estado; estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado = %q, quiero confirmada", estado)
	}
	cierre := asientoDeTipo(t, asientosDeTransferencia(t, ctx, pool, id), TipoAsientoLiquidacionConfirmada)
	if !cierre.TransferenciaID.Valid || uuid.UUID(cierre.TransferenciaID.Bytes) != transferenciaID {
		t.Errorf("transferencia del asiento = %v, quiero %s", cierre.TransferenciaID, transferenciaID)
	}
	if !cierre.OcurridoEn.Valid || !cierre.OcurridoEn.Time.UTC().Equal(evento) {
		t.Errorf("ocurrido_en = %v, quiero el momento que reporta el extracto %v", cierre.OcurridoEn.Time, evento)
	}
	if !cierre.RegistradoEn.Valid || cierre.RegistradoEn.Time.UTC().Equal(evento) {
		t.Errorf("registrado_en = %v, quiero la marca de la corrida, distinta del evento", cierre.RegistradoEn.Time)
	}
	if corridaEn := corridaEnDeLaConciliacion(t, ctx, pool); !cierre.RegistradoEn.Time.UTC().Equal(corridaEn) {
		t.Errorf("registrado_en = %v, quiero la marca del servidor %v de la corrida, no el del evento %v", cierre.RegistradoEn.Time, corridaEn, evento)
	}
	lineas := lineasDelAsiento(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada)
	if lineas[CuentaLiquidacionPendienteRiel] != monto.Int64() || lineas[CuentaCustodiaBanco] != -monto.Int64() {
		t.Errorf("líneas del 4a = %v, quiero %d contra %d", lineas, monto.Int64(), -monto.Int64())
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// HU-NF-08, criterio 9.2: la indeterminada que el extracto rechaza se compensa con el 4b.
func TestHU_NF_08_Criterio9_2IndeterminadaRechazadaCierraCon4b(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(175000)
	evento := base.Add(2 * time.Minute)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
	riel := &rielFalso{extracto: extractoDeCierre(id, estadoExtractoRechazada, monto, evento)}

	if n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodioQueCuadra(t, ctx, pool, hasta), &rastreadorFalso{}); n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	transferenciaID := uuid.UUID(id.Bytes)
	if estado := leerTransferenciaDeResolucion(t, ctx, pool, id).Estado; estado != EstadoTransferenciaCompensada {
		t.Errorf("estado = %q, quiero compensada", estado)
	}
	cierre := asientoDeTipo(t, asientosDeTransferencia(t, ctx, pool, id), TipoAsientoCompensacion)
	if !cierre.OcurridoEn.Valid || !cierre.OcurridoEn.Time.UTC().Equal(evento) {
		t.Errorf("ocurrido_en = %v, quiero el momento que reporta el extracto %v", cierre.OcurridoEn.Time, evento)
	}
	lineas := lineasDelAsiento(t, ctx, pool, transferenciaID, TipoAsientoCompensacion)
	if lineas[CuentaLiquidacionPendienteRiel] != monto.Int64() || lineas[NombreCuentaDisponible(leerTransferenciaDeResolucion(t, ctx, pool, id).UsuarioID)] != -monto.Int64() {
		t.Errorf("líneas del 4b = %v, quiero %d contra %d", lineas, monto.Int64(), -monto.Int64())
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// HU-NF-08, criterio 9.2: la indeterminada ausente del extracto se compensa con el 4b y dos marcas.
func TestHU_NF_08_Criterio9_2IndeterminadaAusenteDelExtractoCompensaCon4b(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(165000)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
	riel := &rielFalso{}

	if n := correrConciliacionDeResolucion(t, ctx, pool, desde, hasta, riel, custodioQueCuadra(t, ctx, pool, hasta), &rastreadorFalso{}); n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	transferenciaID := uuid.UUID(id.Bytes)
	if estado := leerTransferenciaDeResolucion(t, ctx, pool, id).Estado; estado != EstadoTransferenciaCompensada {
		t.Errorf("estado = %q, quiero compensada", estado)
	}
	asientos := asientosDeTransferencia(t, ctx, pool, id)
	if tieneAsientoDeTipo(asientos, TipoAsientoLiquidacionConfirmada) {
		t.Error("una ausencia en el extracto no debe producir el 4a")
	}
	cierre := asientoDeTipo(t, asientos, TipoAsientoCompensacion)
	if !cierre.OcurridoEn.Valid || !cierre.RegistradoEn.Valid || cierre.RegistradoEn.Time.UTC().Equal(cierre.OcurridoEn.Time.UTC()) {
		t.Errorf("marcas del cierre = (%v, %v), quiero dos marcas distintas", cierre.OcurridoEn.Time, cierre.RegistradoEn.Time)
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// HU-NF-08, criterio 10.1: dos corridas solapadas escriben un solo cierre y una sola diferencia.
func TestHU_NF_08_Criterio10_1DosCorridasSolapadasEscribenUnSoloCierre(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	for i := 0; i < iteracionesDeCarrera; i++ {
		base := baseUnicaDeResolucion()
		desde, hasta := base, base.Add(ventanaDeResolucion)
		monto := Centavos(100000 + int64(i))
		evento := base.Add(2 * time.Minute)

		id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
		sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
		extracto := extractoDeCierre(id, estadoExtractoRechazada, monto, evento)
		custodio := custodioDelCierre(t, ctx, pool, hasta)
		servicios := []*Servicio{
			NewServicio(pool, nil, nil, &custodioFalso{extracto: custodio}, nil, ConLectorExtractoRiel(&rielFalso{extracto: extracto})),
			NewServicio(pool, nil, nil, &custodioFalso{extracto: custodio}, nil, ConLectorExtractoRiel(&rielFalso{extracto: extracto})),
		}
		for j, err := range correrCorridasSolapadas(ctx, servicios, desde, hasta, extracto, custodio) {
			if err != nil {
				t.Fatalf("iteración %d, corrida %d: %v", i, j, err)
			}
		}

		transferenciaID := uuid.UUID(id.Bytes)
		confirmaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada)
		compensaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoCompensacion)
		if confirmaciones+compensaciones != 1 {
			t.Fatalf("iteración %d: cierres = %d (4a=%d, 4b=%d), quiero exactamente 1", i, confirmaciones+compensaciones, confirmaciones, compensaciones)
		}
		if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaCompensada {
			t.Errorf("iteración %d: estado = %q, quiero compensada", i, estado)
		}
		if filas := contarDescuadres(t, ctx, pool, TipoDescuadreEstadoDistinto, transferenciaID.String()); filas != 1 {
			t.Errorf("iteración %d: descuadres = %d, quiero 1: solo el ganador registra", i, filas)
		}
		verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
	}
}

// HU-NF-08, criterio 10.2: dos corridas y el proceso del riel producen un solo cierre.
func TestHU_NF_08_Criterio10_2DosCorridasYRielEscribenUnSoloCierre(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	for i := 0; i < iteracionesDeCarrera; i++ {
		base := baseUnicaDeResolucion()
		desde, hasta := base, base.Add(ventanaDeResolucion)
		monto := Centavos(120000 + int64(i))
		evento := base.Add(2 * time.Minute)

		id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaDespachada, monto, base.Add(time.Minute), false)
		sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
		outboxID := sembrarOutboxDespachado(t, ctx, pool, id)
		extracto := extractoDeCierre(id, estadoExtractoRechazada, monto, evento)
		custodio := custodioDelCierre(t, ctx, pool, hasta)
		servicioA := NewServicio(pool, nil, nil, &custodioFalso{extracto: custodio}, nil, ConLectorExtractoRiel(&rielFalso{extracto: extracto}))
		servicioB := NewServicio(pool, nil, nil, &custodioFalso{extracto: custodio}, nil, ConLectorExtractoRiel(&rielFalso{extracto: extracto}))
		reclamada := despachoReclamado{
			outbox:          leerOutbox(t, ctx, pool, outboxID),
			transferencia:   leerTransferenciaDeResolucion(t, ctx, pool, id),
			transferenciaID: uuid.UUID(id.Bytes),
		}
		servicioRiel := NewServicio(pool, nil,
			&rielDoble{respuesta: RespuestaRiel{Resultado: ResultadoRechazado, Motivo: "rechazo de prueba", OcurridoEn: evento}}, nil, nil)

		inicio := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 3)
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-inicio
			_, errs[0] = servicioA.conciliarVentana(ctx, desde, hasta, extracto, custodio)
		}()
		go func() {
			defer wg.Done()
			<-inicio
			_, errs[1] = servicioB.conciliarVentana(ctx, desde, hasta, extracto, custodio)
		}()
		go func() {
			defer wg.Done()
			<-inicio
			errs[2] = servicioRiel.entregarAlRiel(ctx, reclamada)
		}()
		close(inicio)
		wg.Wait()
		for j, err := range errs {
			if err != nil {
				t.Fatalf("iteración %d, actor %d: %v", i, j, err)
			}
		}

		transferenciaID := uuid.UUID(id.Bytes)
		confirmaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoLiquidacionConfirmada)
		compensaciones := contarAsientosDeTransferencia(t, ctx, pool, transferenciaID, TipoAsientoCompensacion)
		if confirmaciones+compensaciones != 1 {
			t.Fatalf("iteración %d: cierres = %d (4a=%d, 4b=%d), quiero exactamente 1", i, confirmaciones+compensaciones, confirmaciones, compensaciones)
		}
		if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaCompensada {
			t.Errorf("iteración %d: estado = %q, quiero compensada", i, estado)
		}
		verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
	}
}
