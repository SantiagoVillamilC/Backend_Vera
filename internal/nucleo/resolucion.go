package nucleo

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Los seis tipos del conjunto cerrado de descuadre (NF-A4-01, del capítulo 13 y HU-NF-08).
const (
	TipoDescuadreLibroSinExtracto       = "libro_sin_extracto"
	TipoDescuadreExtractoSinLibro       = "extracto_sin_libro"
	TipoDescuadreMontoDistinto          = "monto_distinto"
	TipoDescuadreEstadoDistinto         = "estado_distinto"
	TipoDescuadreEstadoFinalContradicho = "estado_final_contradicho"
	TipoDescuadreCustodiaSaldoDistinto  = "custodia_saldo_distinto"
)

// Los dos desenlaces que el extracto del riel reporta (contrato del simulador).
const (
	estadoExtractoConfirmada = "confirmada"
	estadoExtractoRechazada  = "rechazada"
)

// Las dos resoluciones que la fila del descuadre guarda al quedar cerrada.
const (
	resolucionCompensada    = "compensada"
	resolucionMandaExtracto = "mando el extracto"
)

// referenciaCustodia es la identidad sin transferencia de la diferencia del invariante 2.
const referenciaCustodia = "custodia_saldo"

// desenlaceDelPar es la decisión fija de un par libro/extracto ya emparejado por identificador y monto.
type desenlaceDelPar struct {
	tipo      string
	compensar bool
}

// clasificarParDeEstados aplica la tabla cerrada de HU-NF-08 a los estados de un par emparejado.
func clasificarParDeEstados(estadoLibro, estadoExtracto string) (desenlaceDelPar, bool) {
	switch {
	case esTransferenciaActiva(estadoLibro) && estadoExtracto == estadoExtractoConfirmada:
		return desenlaceDelPar{tipo: TipoDescuadreEstadoDistinto}, true
	case esTransferenciaActiva(estadoLibro) && estadoExtracto == estadoExtractoRechazada:
		return desenlaceDelPar{tipo: TipoDescuadreEstadoDistinto, compensar: true}, true
	case estadoLibro == EstadoTransferenciaConfirmada && estadoExtracto != estadoExtractoConfirmada:
		return desenlaceDelPar{tipo: TipoDescuadreEstadoFinalContradicho}, true
	case estadoLibro == EstadoTransferenciaCompensada && estadoExtracto != estadoExtractoRechazada:
		return desenlaceDelPar{tipo: TipoDescuadreEstadoFinalContradicho}, true
	default:
		return desenlaceDelPar{}, false
	}
}

// esTransferenciaActiva reporta si el estado es de los que el extracto todavía puede resolver.
func esTransferenciaActiva(estado string) bool {
	return estado == EstadoTransferenciaDespachada || estado == EstadoTransferenciaIndeterminada
}

// transicionesDeCierre arma los pasos del compare-and-set hacia el estado que manda el extracto.
func transicionesDeCierre(estadoActual, nuevo string) []transicionCierre {
	if estadoActual == EstadoTransferenciaDespachada && nuevo == EstadoTransferenciaCompensada {
		return []transicionCierre{
			{esperado: EstadoTransferenciaDespachada, nuevo: EstadoTransferenciaFallida},
			{esperado: EstadoTransferenciaFallida, nuevo: EstadoTransferenciaCompensada},
		}
	}
	return []transicionCierre{{esperado: estadoActual, nuevo: nuevo}}
}

// detectorDiferencias aplica la tabla cerrada de resolución a una ventana dentro de su transacción.
type detectorDiferencias struct {
	ctx           context.Context
	q             *repositorio.Queries
	ventana       ventanaConciliacion
	rastreador    RastreadorErrores
	diferencias   int
	extractoPorID map[uuid.UUID]OperacionDeExtractoRiel
}

// detectarDiferenciasDeVentana registra y resuelve cada diferencia de la ventana (HU-NF-08, criterios 4 a 8, 22 y 23).
func (s *Servicio) detectarDiferenciasDeVentana(ctx context.Context, q *repositorio.Queries, ventana ventanaConciliacion) (int, error) {
	detector := &detectorDiferencias{ctx: ctx, q: q, ventana: ventana, rastreador: s.rastreador}
	if err := detector.correr(); err != nil {
		return 0, err
	}
	return detector.diferencias, nil
}

// correr recorre las tres fuentes: lo del libro, lo del extracto y la custodia.
func (d *detectorDiferencias) correr() error {
	d.extractoPorID = d.indexarExtracto()
	if err := d.revisarLasDelLibro(); err != nil {
		return err
	}
	if err := d.revisarLasDelExtracto(); err != nil {
		return err
	}
	return d.revisarLaCustodia()
}

// indexarExtracto deja las operaciones del extracto emparejables por el identificador global (criterio 20).
func (d *detectorDiferencias) indexarExtracto() map[uuid.UUID]OperacionDeExtractoRiel {
	porID := make(map[uuid.UUID]OperacionDeExtractoRiel, len(d.ventana.extractoRiel.Operaciones))
	for _, operacion := range d.ventana.extractoRiel.Operaciones {
		porID[operacion.TransferenciaID] = operacion
	}
	return porID
}

// revisarLasDelLibro compensa lo que está activo en el libro y no aparece en el extracto (criterios 4 y 5).
func (d *detectorDiferencias) revisarLasDelLibro() error {
	for _, fila := range d.ventana.transferencias {
		id := uuid.UUID(fila.ID.Bytes)
		if _, enExtracto := d.extractoPorID[id]; enExtracto {
			continue
		}
		transferencia, pendiente, err := d.leerTransferencia(id)
		if err != nil {
			return err
		}
		if transferencia == nil || !esTransferenciaActiva(transferencia.Estado) {
			continue
		}
		// Criterio 4: una orden sin marcar como entregada al riel no es descuadre.
		if pendiente {
			continue
		}
		if err := d.compensarLibroSinExtracto(*transferencia); err != nil {
			return err
		}
	}
	return nil
}

// revisarLasDelExtracto empareja cada operación con el libro y aplica su regla (criterios 6, 7, 8 y 22).
func (d *detectorDiferencias) revisarLasDelExtracto() error {
	for _, operacion := range d.ventana.extractoRiel.Operaciones {
		if err := d.revisarOperacion(operacion); err != nil {
			return err
		}
	}
	return nil
}

// revisarOperacion decide el tipo de diferencia de una operación del extracto.
func (d *detectorDiferencias) revisarOperacion(operacion OperacionDeExtractoRiel) error {
	transferencia, _, err := d.leerTransferencia(operacion.TransferenciaID)
	if err != nil {
		return err
	}
	if transferencia == nil {
		return d.escalarExtractoSinLibro(operacion)
	}
	if Centavos(transferencia.Monto) != operacion.Monto {
		return d.escalarMontoDistinto(*transferencia, operacion)
	}
	desenlace, hay := clasificarParDeEstados(transferencia.Estado, operacion.Estado)
	if !hay {
		return nil
	}
	if desenlace.tipo == TipoDescuadreEstadoFinalContradicho {
		return d.escalarEstadoFinalContradicho(*transferencia, operacion)
	}
	return d.adoptarElExtracto(*transferencia, operacion, desenlace)
}

// compensarLibroSinExtracto registra la diferencia y produce el asiento 4b con la exclusión por estado.
func (d *detectorDiferencias) compensarLibroSinExtracto(transferencia repositorio.Transferencia) error {
	id := uuid.UUID(transferencia.ID.Bytes)
	abierto, err := d.hayDescuadreAbierto(TipoDescuadreLibroSinExtracto, id.String())
	if err != nil || abierto {
		return err
	}
	aplicado, err := aplicarCierre(d.ctx, d.q, transferencia.ID, transicionesDeCierre(transferencia.Estado, EstadoTransferenciaCompensada))
	if err != nil {
		return err
	}
	if !aplicado {
		return nil
	}
	if err := escribirAsientoDeCompensacion(d.ctx, d.q, transferencia, "", d.ventana.hasta); err != nil {
		return err
	}
	return d.registrarResuelto(TipoDescuadreLibroSinExtracto, id, pgtype.Text{}, transferencia.Monto, pgtype.Int8{}, resolucionCompensada)
}

// adoptarElExtracto produce el asiento 4a o 4b y deja el descuadre resuelto por el extracto (criterios 8 y 8.3).
func (d *detectorDiferencias) adoptarElExtracto(transferencia repositorio.Transferencia, operacion OperacionDeExtractoRiel, desenlace desenlaceDelPar) error {
	id := uuid.UUID(transferencia.ID.Bytes)
	abierto, err := d.hayDescuadreAbierto(TipoDescuadreEstadoDistinto, id.String())
	if err != nil || abierto {
		return err
	}
	nuevo := EstadoTransferenciaConfirmada
	if desenlace.compensar {
		nuevo = EstadoTransferenciaCompensada
	}
	aplicado, err := aplicarCierre(d.ctx, d.q, transferencia.ID, transicionesDeCierre(transferencia.Estado, nuevo))
	if err != nil {
		return err
	}
	if !aplicado {
		return nil
	}
	if desenlace.compensar {
		err = escribirAsientoDeCompensacion(d.ctx, d.q, transferencia, "", operacion.OcurridoEn)
	} else {
		err = escribirAsientoDeConfirmacion(d.ctx, d.q, transferencia, operacion.OcurridoEn)
	}
	if err != nil {
		return err
	}
	return d.registrarResuelto(TipoDescuadreEstadoDistinto, id, pgtype.Text{}, transferencia.Monto, pgtype.Int8{Int64: operacion.Monto.Int64(), Valid: true}, resolucionMandaExtracto)
}

// escalarExtractoSinLibro registra y escala lo que salió sin quedar registrado, sin asiento (criterio 6).
func (d *detectorDiferencias) escalarExtractoSinLibro(operacion OperacionDeExtractoRiel) error {
	referencia := operacion.TransferenciaID.String()
	abierto, err := d.hayDescuadreAbierto(TipoDescuadreExtractoSinLibro, referencia)
	if err != nil || abierto {
		return err
	}
	fila, err := d.insertarDescuadreSiNoHayAbierto(TipoDescuadreExtractoSinLibro, uuid.Nil, pgtype.Text{String: referencia, Valid: true},
		pgtype.Int8{}, pgtype.Int8{Int64: operacion.Monto.Int64(), Valid: true})
	if err != nil {
		return err
	}
	if fila == nil {
		return nil
	}
	d.escalar(*fila, fmt.Errorf("nucleo: defecto grave: el extracto trae la transferencia %s sin registro en el libro", referencia))
	return nil
}

// escalarMontoDistinto registra ambos montos y escala sin automatizar (criterio 7). Esta ruta nunca
// llama a aplicarCierre, así que no hay ningún CAS que la proteja: usa el mismo INSERT ...
// ON CONFLICT DO NOTHING que escalarExtractoSinLibro, porque dos Conciliar() concurrentes pueden
// encontrar el mismo par a la vez.
func (d *detectorDiferencias) escalarMontoDistinto(transferencia repositorio.Transferencia, operacion OperacionDeExtractoRiel) error {
	id := uuid.UUID(transferencia.ID.Bytes)
	abierto, err := d.hayDescuadreAbierto(TipoDescuadreMontoDistinto, id.String())
	if err != nil || abierto {
		return err
	}
	fila, err := d.insertarDescuadreSiNoHayAbierto(TipoDescuadreMontoDistinto, id, pgtype.Text{},
		pgtype.Int8{Int64: transferencia.Monto, Valid: true}, pgtype.Int8{Int64: operacion.Monto.Int64(), Valid: true})
	if err != nil || fila == nil {
		return err
	}
	d.escalar(*fila, fmt.Errorf("nucleo: montos distintos en %s: interno %d, externo %d", id, transferencia.Monto, operacion.Monto.Int64()))
	return nil
}

// escalarEstadoFinalContradicho registra y escala sin revertir el asiento de cierre ya escrito (criterio 22).
// Mismo motivo que escalarMontoDistinto: la transferencia ya quedó en un estado final, no hay
// aplicarCierre que la proteja con CAS, así que el insert usa ON CONFLICT DO NOTHING.
func (d *detectorDiferencias) escalarEstadoFinalContradicho(transferencia repositorio.Transferencia, operacion OperacionDeExtractoRiel) error {
	id := uuid.UUID(transferencia.ID.Bytes)
	abierto, err := d.hayDescuadreAbierto(TipoDescuadreEstadoFinalContradicho, id.String())
	if err != nil || abierto {
		return err
	}
	fila, err := d.insertarDescuadreSiNoHayAbierto(TipoDescuadreEstadoFinalContradicho, id, pgtype.Text{},
		pgtype.Int8{Int64: transferencia.Monto, Valid: true}, pgtype.Int8{Int64: operacion.Monto.Int64(), Valid: true})
	if err != nil || fila == nil {
		return err
	}
	d.escalar(*fila, fmt.Errorf("nucleo: estado final contradicho en %s: libro %s, extracto %s", id, transferencia.Estado, operacion.Estado))
	return nil
}

// revisarLaCustodia persiste el extracto del corte y registra la diferencia del invariante 2 (criterio 23).
func (d *detectorDiferencias) revisarLaCustodia() error {
	corte := d.ventana.hasta
	if err := d.guardarExtractoDeCustodia(corte); err != nil {
		return err
	}
	resultado, err := compararCustodiaEnTx(d.ctx, d.q, corte)
	if errors.Is(err, ErrExtractoDeCustodiaAusente) {
		return nil
	}
	if err != nil {
		return err
	}
	if resultado.Cuadra() {
		return nil
	}
	abierto, err := d.hayDescuadreAbierto(TipoDescuadreCustodiaSaldoDistinto, referenciaCustodia)
	if err != nil || abierto {
		return err
	}
	// referenciaCustodia es una identidad fija, la misma para cualquier corte: sin CAS posible, dos
	// Conciliar() concurrentes sobre cortes distintos pueden escalar la misma diferencia a la vez.
	fila, err := d.insertarDescuadreSiNoHayAbierto(TipoDescuadreCustodiaSaldoDistinto, uuid.Nil, pgtype.Text{String: referenciaCustodia, Valid: true},
		pgtype.Int8{Int64: resultado.MontoInterno.Int64(), Valid: true}, pgtype.Int8{Int64: resultado.MontoExterno.Int64(), Valid: true})
	if err != nil || fila == nil {
		return err
	}
	d.escalar(*fila, fmt.Errorf("nucleo: la custodia interna %d no coincide con el extracto %d al corte %s",
		resultado.MontoInterno.Int64(), resultado.MontoExterno.Int64(), corte.Format(time.RFC3339Nano)))
	return nil
}

// guardarExtractoDeCustodia persiste el extracto leído por la corrida sin pisar un corte ya guardado.
func (d *detectorDiferencias) guardarExtractoDeCustodia(corte time.Time) error {
	extracto := d.ventana.extractoCustodio
	if err := d.q.InsertarExtractoCustodio(d.ctx, repositorio.InsertarExtractoCustodioParams{
		CorteEn: pgtype.Timestamptz{Time: corte, Valid: true},
		Saldo:   extracto.Saldo.Int64(),
		Origen:  extracto.Origen,
	}); err != nil {
		return fmt.Errorf("nucleo: guardar el extracto del custodio al corte: %w", err)
	}
	return nil
}

// leerTransferencia trae la transferencia con la marca de despacho todavía pendiente (criterio 4).
func (d *detectorDiferencias) leerTransferencia(id uuid.UUID) (*repositorio.Transferencia, bool, error) {
	fila, err := d.q.ObtenerTransferenciaConDespacho(d.ctx, pgtype.UUID{Bytes: id, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("nucleo: leer la transferencia %s de la ventana: %w", id, err)
	}
	return &repositorio.Transferencia{
		ID:             fila.ID,
		UsuarioID:      fila.UsuarioID,
		CuentaOrigen:   fila.CuentaOrigen,
		LlaveDestino:   fila.LlaveDestino,
		Monto:          fila.Monto,
		Moneda:         fila.Moneda,
		IdempotencyKey: fila.IdempotencyKey,
		Estado:         fila.Estado,
		CreadaEn:       fila.CreadaEn,
		ActualizadaEn:  fila.ActualizadaEn,
	}, fila.DespachoPendiente, nil
}

// hayDescuadreAbierto respalda la deduplicación en la consulta antes de insertar (criterio 13).
func (d *detectorDiferencias) hayDescuadreAbierto(tipo, identidad string) (bool, error) {
	abiertos, err := d.q.BuscarDescuadresAbiertos(d.ctx, repositorio.BuscarDescuadresAbiertosParams{Tipo: tipo, Identidad: identidad})
	if err != nil {
		return false, fmt.Errorf("nucleo: buscar descuadres abiertos de %q: %w", tipo, err)
	}
	return len(abiertos) > 0, nil
}

// insertarDescuadre sella el tipo, la identidad, los montos y la ventana de la corrida (criterio 12).
func (d *detectorDiferencias) insertarDescuadre(tipo string, transferenciaID uuid.UUID, referencia pgtype.Text, montoInterno, montoExterno pgtype.Int8) (repositorio.Descuadre, error) {
	fila, err := d.q.InsertarDescuadre(d.ctx, repositorio.InsertarDescuadreParams{
		Tipo:              tipo,
		TransferenciaID:   pgtype.UUID{Bytes: transferenciaID, Valid: transferenciaID != uuid.Nil},
		ReferenciaExterna: referencia,
		MontoInterno:      montoInterno,
		MontoExterno:      montoExterno,
		VentanaDesde:      pgtype.Timestamptz{Time: d.ventana.desde, Valid: true},
		VentanaHasta:      pgtype.Timestamptz{Time: d.ventana.hasta, Valid: true},
	})
	if err != nil {
		return repositorio.Descuadre{}, fmt.Errorf("nucleo: registrar el descuadre %q: %w", tipo, err)
	}
	d.diferencias++
	return fila, nil
}

// insertarDescuadreSiNoHayAbierto inserta la diferencia sin CAS posible y no falla si otra corrida
// concurrente ya ganó la carrera del índice único: el ON CONFLICT DO NOTHING la vuelve un no-op
// silencioso, sin abortar la transacción de esta corrida (HU-NF-08, criterio 10.3).
func (d *detectorDiferencias) insertarDescuadreSiNoHayAbierto(tipo string, transferenciaID uuid.UUID, referencia pgtype.Text, montoInterno, montoExterno pgtype.Int8) (*repositorio.Descuadre, error) {
	fila, err := d.q.InsertarDescuadreSiNoHayAbierto(d.ctx, repositorio.InsertarDescuadreSiNoHayAbiertoParams{
		Tipo:              tipo,
		TransferenciaID:   pgtype.UUID{Bytes: transferenciaID, Valid: transferenciaID != uuid.Nil},
		ReferenciaExterna: referencia,
		MontoInterno:      montoInterno,
		MontoExterno:      montoExterno,
		VentanaDesde:      pgtype.Timestamptz{Time: d.ventana.desde, Valid: true},
		VentanaHasta:      pgtype.Timestamptz{Time: d.ventana.hasta, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("nucleo: registrar el descuadre %q: %w", tipo, err)
	}
	d.diferencias++
	return &fila, nil
}

// registrarResuelto inserta la fila ya resuelta por la regla del tipo.
func (d *detectorDiferencias) registrarResuelto(tipo string, transferenciaID uuid.UUID, referencia pgtype.Text, montoInterno int64, montoExterno pgtype.Int8, resolucion string) error {
	fila, err := d.insertarDescuadre(tipo, transferenciaID, referencia, pgtype.Int8{Int64: montoInterno, Valid: true}, montoExterno)
	if err != nil {
		return err
	}
	if _, err := d.q.ResolverDescuadre(d.ctx, repositorio.ResolverDescuadreParams{
		ID:         fila.ID,
		Resolucion: pgtype.Text{String: resolucion, Valid: true},
	}); err != nil {
		return fmt.Errorf("nucleo: resolver el descuadre %d: %w", fila.ID, err)
	}
	return nil
}

// escalar deja la diferencia sin resolución y la reporta por el puerto de observabilidad (criterio 24).
func (d *detectorDiferencias) escalar(fila repositorio.Descuadre, causa error) {
	if d.rastreador == nil {
		return
	}
	d.rastreador.Capturar(d.ctx, causa, ContextoError{Operacion: "conciliar", Canal: CanalSoloLog, Campos: camposDelDescuadre(fila)})
}

// camposDelDescuadre identifica la diferencia escalada para el rastreador de errores.
func camposDelDescuadre(fila repositorio.Descuadre) []Campo {
	campos := []Campo{
		{Clave: "descuadre_id", Valor: strconv.FormatInt(fila.ID, 10)},
		{Clave: "tipo", Valor: fila.Tipo},
	}
	if fila.TransferenciaID.Valid {
		campos = append(campos, Campo{Clave: "transferencia_id", Valor: uuid.UUID(fila.TransferenciaID.Bytes).String()})
	}
	if fila.ReferenciaExterna.Valid {
		campos = append(campos, Campo{Clave: "referencia_externa", Valor: fila.ReferenciaExterna.String})
	}
	if fila.MontoInterno.Valid {
		campos = append(campos, Campo{Clave: "monto_interno", Valor: strconv.FormatInt(fila.MontoInterno.Int64, 10)})
	}
	if fila.MontoExterno.Valid {
		campos = append(campos, Campo{Clave: "monto_externo", Valor: strconv.FormatInt(fila.MontoExterno.Int64, 10)})
	}
	if fila.VentanaDesde.Valid {
		campos = append(campos, Campo{Clave: "ventana_desde", Valor: fila.VentanaDesde.Time.UTC().Format(time.RFC3339Nano)})
	}
	if fila.VentanaHasta.Valid {
		campos = append(campos, Campo{Clave: "ventana_hasta", Valor: fila.VentanaHasta.Time.UTC().Format(time.RFC3339Nano)})
	}
	if fila.DetectadoEn.Valid {
		campos = append(campos, Campo{Clave: "detectado_en", Valor: fila.DetectadoEn.Time.UTC().Format(time.RFC3339Nano)})
	}
	return campos
}
