//go:build integration

package nucleo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// rielConsultado responde la consulta de estado y sirve el extracto pedido por la prueba.
type rielConsultado struct {
	extracto       ExtractoRiel
	estados        map[uuid.UUID]RespuestaRiel
	err            error
	llamadas       int
	abridor        *abridorDeConciliacion
	beginsAlLlamar int
}

// Enviar no lo usa esta prueba: el envío al riel lo cubre el worker de outbox.
func (r *rielConsultado) Enviar(context.Context, OrdenRiel) (RespuestaRiel, error) {
	return RespuestaRiel{}, errors.New("prueba: Enviar no se usa")
}

// ConsultarEstado devuelve el desenlace configurado o el error que simula el riel caído.
func (r *rielConsultado) ConsultarEstado(_ context.Context, id uuid.UUID) (RespuestaRiel, error) {
	r.llamadas++
	if r.abridor != nil {
		r.beginsAlLlamar = r.abridor.begins
	}
	if r.err != nil {
		return RespuestaRiel{}, r.err
	}
	respuesta, ok := r.estados[id]
	if !ok {
		return RespuestaRiel{}, ErrRielSinRegistro
	}
	return respuesta, nil
}

// Extracto sirve el extracto que la prueba configuró, para la conciliación por ventana.
func (r *rielConsultado) Extracto(context.Context, time.Time, time.Time) (ExtractoRiel, error) {
	return r.extracto, nil
}

// respuestaConfirmada arma la respuesta del riel que cierra una indeterminada con el asiento 4a.
func respuestaConfirmada(id uuid.UUID, evento time.Time) RespuestaRiel {
	return RespuestaRiel{TransferenciaID: id, Resultado: ResultadoConfirmado, OcurridoEn: evento}
}

// rielDeConsulta arma el riel falso con una única respuesta configurada.
func rielDeConsulta(id uuid.UUID, respuesta RespuestaRiel) *rielConsultado {
	return &rielConsultado{estados: map[uuid.UUID]RespuestaRiel{id: respuesta}}
}

// rielQueCuelga bloquea la consulta hasta que el contexto de la corrida se agota.
type rielQueCuelga struct {
	llamadas int
}

// Enviar no lo usa esta prueba: el envío al riel lo cubre el worker de outbox.
func (r *rielQueCuelga) Enviar(context.Context, OrdenRiel) (RespuestaRiel, error) {
	return RespuestaRiel{}, errors.New("prueba: Enviar no se usa")
}

// ConsultarEstado espera la cancelación del contexto y la reporta como causa.
func (r *rielQueCuelga) ConsultarEstado(ctx context.Context, _ uuid.UUID) (RespuestaRiel, error) {
	r.llamadas++
	<-ctx.Done()
	return RespuestaRiel{}, ctx.Err()
}

// HU-NF-08, criterio 21: la consulta resuelve la indeterminada sin esperar a que su ventana madure.
func TestHU_NF_08_Criterio21ConsultaResuelveIndeterminadaSinEsperarLaVentana(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(140000)
	// actualizada_en posterior al hasta de la ventana: solo la consulta puede alcanzarla.
	actualizada := hasta.Add(time.Minute)
	evento := actualizada.Add(time.Second)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, actualizada, false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
	riel := rielDeConsulta(uuid.UUID(id.Bytes), respuestaConfirmada(uuid.UUID(id.Bytes), evento))
	servicio := NewServicio(pool, nil, riel, custodioQueCuadra(t, ctx, pool, hasta), &rastreadorFalso{}, ConLectorExtractoRiel(riel))
	servicio.ahora = func() time.Time { return desde.Add(2 * ventanaDeResolucion) }
	fijarCursor(t, pool, desde)

	n, err := servicio.Conciliar(ctx, ventanaDeResolucion)
	if err != nil {
		t.Fatalf("Conciliar: %v", err)
	}
	if !actualizada.After(hasta) {
		t.Fatalf("la prueba no aísla la consulta: actualizada_en %v debía quedar fuera de [%v, %v)", actualizada, desde, hasta)
	}
	if n != 1 {
		t.Fatalf("diferencias = %d, quiero 1", n)
	}
	transferenciaID := uuid.UUID(id.Bytes)
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado = %q, quiero confirmada", estado)
	}
	cierre := asientoDeTipo(t, asientosDeTransferencia(t, ctx, pool, id), TipoAsientoLiquidacionConfirmada)
	if !cierre.OcurridoEn.Valid || !cierre.OcurridoEn.Time.UTC().Equal(evento) {
		t.Errorf("ocurrido_en = %v, quiero el momento que reportó el riel %v", cierre.OcurridoEn.Time, evento)
	}
	verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
}

// HU-NF-10, criterio 24.2: consultar el estado no escribe asiento ni mueve la transferencia.
func TestHU_NF_10_Criterio24_2LaConsultaNoEscribePorSiSola(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	monto := Centavos(130000)
	evento := base.Add(2 * time.Minute)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
	riel := rielDeConsulta(uuid.UUID(id.Bytes), respuestaConfirmada(uuid.UUID(id.Bytes), evento))
	servicio := NewServicio(pool, nil, riel, nil, nil, ConLectorExtractoRiel(riel))

	operaciones, err := servicio.consultarIndeterminadas(ctx)
	if err != nil {
		t.Fatalf("consultarIndeterminadas: %v", err)
	}
	if len(operaciones) == 0 {
		t.Fatal("la consulta no devolvió la operación del riel")
	}
	transferenciaID := uuid.UUID(id.Bytes)
	if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaIndeterminada {
		t.Errorf("estado = %q, quiero indeterminada: la consulta no debe moverla", estado)
	}
	asientos := asientosDeTransferencia(t, ctx, pool, id)
	if tieneAsientoDeTipo(asientos, TipoAsientoLiquidacionConfirmada) || tieneAsientoDeTipo(asientos, TipoAsientoCompensacion) {
		t.Errorf("asientos = %+v, quiero solo el despacho: la consulta no debe asentar", asientos)
	}
	if filas := contarDescuadres(t, ctx, pool, TipoDescuadreEstadoDistinto, transferenciaID.String()); filas != 0 {
		t.Errorf("descuadres = %d, quiero 0: la consulta no registra la diferencia", filas)
	}
}

// HU-NF-08, criterios 1 y 24.1: la consulta al riel se lee antes de abrir la transacción de escritura.
func TestConciliarConsultaElEstadoAntesDeLaTransaccionDeEscritura(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	desde := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	hasta := desde.Add(ventanaDeResolucion)
	monto := Centavos(110000)

	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, hasta.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, id, desde.Add(time.Minute))

	abridor := &abridorDeConciliacion{interno: poolDeNucleo{pool: pool}}
	riel := rielDeConsulta(uuid.UUID(id.Bytes), respuestaConfirmada(uuid.UUID(id.Bytes), hasta.Add(time.Minute)))
	riel.abridor = abridor
	espia := &detectorEspia{}
	servicio := &Servicio{
		pool:       abridor,
		custodio:   &custodioFalso{},
		riel:       riel,
		lectorRiel: riel,
		detectar:   espia.detectarFn(),
		ahora:      func() time.Time { return hasta.Add(ventanaDeResolucion) },
	}
	fijarCursor(t, pool, desde)

	if _, err := servicio.Conciliar(ctx, ventanaDeResolucion); err != nil {
		t.Fatalf("Conciliar: %v", err)
	}
	// Begin 1 lee el cursor y Begin 2 la lista de indeterminadas; la consulta externa va antes del Begin 3.
	if riel.llamadas == 0 {
		t.Fatal("no se consultó el estado de ninguna indeterminada")
	}
	if riel.beginsAlLlamar != 2 {
		t.Errorf("la consulta al riel ocurrió tras %d Begin, quiero 2 (cursor e indeterminadas)", riel.beginsAlLlamar)
	}
	if abridor.begins != 3 {
		t.Errorf("Begin = %d, quiero 3 (cursor, indeterminadas y escritura de la corrida)", abridor.begins)
	}
}

// HU-NF-08, criterio 10.2: la consulta y una corrida de ventana cierran una sola vez.
func TestHU_NF_10_Criterio24ConsultaYRielCierranUnSoloCierre(t *testing.T) {
	pool := abrirPoolConcurrenteDePrueba(t)
	ctx := t.Context()

	for i := 0; i < iteracionesDeCarrera; i++ {
		base := baseUnicaDeResolucion()
		desde, hasta := base, base.Add(ventanaDeResolucion)
		monto := Centavos(150000 + int64(i))
		evento := base.Add(2 * time.Minute)

		id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, base.Add(time.Minute), false)
		sembrarDespacho(t, ctx, pool, id, base.Add(time.Minute))
		extracto := extractoDeCierre(id, estadoExtractoConfirmada, monto, evento)
		custodio := custodioDelCierre(t, ctx, pool, hasta)
		riel := rielDeConsulta(uuid.UUID(id.Bytes), respuestaConfirmada(uuid.UUID(id.Bytes), evento))
		consultaServicio := NewServicio(pool, nil, riel, &custodioFalso{extracto: custodio}, nil, ConLectorExtractoRiel(riel))
		ventanaServicio := NewServicio(pool, nil, nil, &custodioFalso{extracto: custodio}, nil, ConLectorExtractoRiel(&rielFalso{extracto: extracto}))

		inicio := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-inicio
			consultadas, err := consultaServicio.consultarIndeterminadas(ctx)
			if err != nil {
				errs[0] = err
				return
			}
			_, errs[0] = consultaServicio.conciliarVentana(ctx, desde, hasta, ExtractoRiel{Operaciones: consultadas}, custodio)
		}()
		go func() {
			defer wg.Done()
			<-inicio
			_, errs[1] = ventanaServicio.conciliarVentana(ctx, desde, hasta, extracto, custodio)
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
		if estado := leerEstadoTransferencia(t, ctx, pool, transferenciaID); estado != EstadoTransferenciaConfirmada {
			t.Errorf("iteración %d: estado = %q, quiero confirmada", i, estado)
		}
		verificarAsientosCuadradosDeLaTransferencia(t, ctx, pool, transferenciaID)
	}
}

// HU-NF-08, criterio 7: una indeterminada que la ventana trae con otro monto no se cierra, y la que está fuera de la ventana sí cierra por la consulta.
func TestHU_NF_08_Criterio7IndeterminadaConMontoDistintoNoCierraYLaFueraDeVentanaSiCierra(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	interno := Centavos(160000)
	externo := Centavos(159999)
	eventoEnVentana := base.Add(2 * time.Minute)
	eventoFuera := hasta.Add(time.Minute).Add(time.Second)

	// En la ventana y con otro monto en el extracto real: la consulta la reporta confirmada, pero manda el extracto.
	enVentana, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, interno, base.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, enVentana, base.Add(time.Minute))
	// Fuera de la ventana: solo la consulta puede alcanzarla (criterio 21).
	fuera, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, 170000, hasta.Add(time.Minute), false)
	sembrarDespacho(t, ctx, pool, fuera, base.Add(time.Minute))

	riel := &rielConsultado{
		extracto: extractoDeCierre(enVentana, estadoExtractoConfirmada, externo, eventoEnVentana),
		estados: map[uuid.UUID]RespuestaRiel{
			uuid.UUID(enVentana.Bytes): respuestaConfirmada(uuid.UUID(enVentana.Bytes), eventoEnVentana),
			uuid.UUID(fuera.Bytes):     respuestaConfirmada(uuid.UUID(fuera.Bytes), eventoFuera),
		},
	}
	rastreador := &rastreadorFalso{}
	servicio := NewServicio(pool, nil, riel, custodioQueCuadra(t, ctx, pool, hasta), rastreador, ConLectorExtractoRiel(riel))
	servicio.ahora = func() time.Time { return hasta.Add(ventanaDeResolucion) }
	fijarCursor(t, pool, desde)

	n, err := servicio.Conciliar(ctx, ventanaDeResolucion)
	if err != nil {
		t.Fatalf("Conciliar: %v", err)
	}
	if n != 2 {
		t.Fatalf("diferencias = %d, quiero 2 (el monto distinto abierto y el cierre por consulta)", n)
	}
	if estado := leerTransferenciaDeResolucion(t, ctx, pool, enVentana).Estado; estado != EstadoTransferenciaIndeterminada {
		t.Errorf("estado en ventana = %q, quiero indeterminada: el monto distinto no se automatiza", estado)
	}
	if tieneAsientoDeTipo(asientosDeTransferencia(t, ctx, pool, enVentana), TipoAsientoLiquidacionConfirmada) {
		t.Error("el monto distinto de la ventana no debe producir el 4a")
	}
	enVentanaID := uuid.UUID(enVentana.Bytes).String()
	if filas := contarDescuadresAbiertos(t, ctx, pool, TipoDescuadreMontoDistinto, enVentanaID); filas != 1 {
		t.Fatalf("descuadres abiertos de monto_distinto = %d, quiero 1", filas)
	}
	fila := leerDescuadre(t, ctx, pool, TipoDescuadreMontoDistinto, enVentanaID)
	if !fila.MontoInterno.Valid || fila.MontoInterno.Int64 != interno.Int64() || !fila.MontoExterno.Valid || fila.MontoExterno.Int64 != externo.Int64() {
		t.Errorf("descuadre = %+v, quiero interno %d y externo %d", fila, interno.Int64(), externo.Int64())
	}
	if estado := leerTransferenciaDeResolucion(t, ctx, pool, fuera).Estado; estado != EstadoTransferenciaConfirmada {
		t.Errorf("estado fuera de ventana = %q, quiero confirmada por la consulta", estado)
	}
	if !tieneAsientoDeTipo(asientosDeTransferencia(t, ctx, pool, fuera), TipoAsientoLiquidacionConfirmada) {
		t.Error("la indeterminada fuera de la ventana debe cerrar con el 4a por la consulta")
	}
}

// HU-NF-08, criterio 21 / M2: una indeterminada que no responde no consume más allá del presupuesto de la corrida.
func TestConsultarIndeterminadasCortaConElPresupuesto(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, 120000, base.Add(time.Minute), false)
	sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, 121000, base.Add(2*time.Minute), false)

	riel := &rielQueCuelga{}
	servicio := NewServicio(pool, nil, riel, nil, nil)
	servicio.presupuestoIndeterminadas = 50 * time.Millisecond

	inicio := time.Now()
	operaciones, err := servicio.consultarIndeterminadas(ctx)
	if err != nil {
		t.Fatalf("consultarIndeterminadas: %v", err)
	}
	if transcurrido := time.Since(inicio); transcurrido > time.Second {
		t.Errorf("la consulta tardó %v, quiero el corte por presupuesto", transcurrido)
	}
	if len(operaciones) != 0 {
		t.Errorf("operaciones = %d, quiero 0: el presupuesto corta antes de resolver", len(operaciones))
	}
	if riel.llamadas != 1 {
		t.Errorf("llamadas = %d, quiero 1: se corta en la primera que no responde", riel.llamadas)
	}
}

// HU-NF-10, criterio 24.1: un riel caído durante la consulta no bloquea la corrida ni deja el cursor sin avanzar.
func TestHU_NF_10_Criterio24ConsultaConRielCaidoNoBloqueaLaCorrida(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := baseUnicaDeResolucion()
	desde, hasta := base, base.Add(ventanaDeResolucion)
	monto := Centavos(125000)

	// Fuera de la ventana: sin la consulta, la corrida no tiene por qué tocarla.
	id, _ := sembrarUsuarioYTransferencia(t, ctx, pool, EstadoTransferenciaIndeterminada, monto, hasta.Add(time.Minute), false)
	riel := &rielConsultado{err: errors.New("riel caído")}
	rastreador := &rastreadorFalso{}
	servicio := NewServicio(pool, nil, riel, custodioQueCuadra(t, ctx, pool, hasta), rastreador, ConLectorExtractoRiel(riel))
	servicio.ahora = func() time.Time { return hasta.Add(ventanaDeResolucion) }
	fijarCursor(t, pool, desde)

	if _, err := servicio.Conciliar(ctx, ventanaDeResolucion); err != nil {
		t.Fatalf("Conciliar con el riel caído = %v, quiero nil: no debe bloquear la corrida", err)
	}
	if estado := leerEstadoTransferencia(t, ctx, pool, uuid.UUID(id.Bytes)); estado != EstadoTransferenciaIndeterminada {
		t.Errorf("estado = %q, quiero indeterminada: sin respuesta del riel no se resuelve", estado)
	}
	if len(rastreador.capturas) == 0 {
		t.Error("la consulta fallida no dejó rastro en el rastreador")
	}
	if got := ultimaVentana(t, pool); !got.Equal(hasta) {
		t.Errorf("cursor = %v, quiero %v: la transacción de escritura cerró y el cursor avanzó", got, hasta)
	}
}
