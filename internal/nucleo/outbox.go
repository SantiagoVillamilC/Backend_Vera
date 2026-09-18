package nucleo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// TipoOutboxDespachoTransferencia identifica la intención de mandar una transferencia al riel.
const TipoOutboxDespachoTransferencia = "despacho_transferencia"

// longitudLoteOutbox es cuántas filas toma cada barrido, el mismo tope del ejemplo del capítulo 10.
const longitudLoteOutbox = 50

// plazoEntregaRiel es el plazo máximo de la llamada al riel (Criterio 16).
const plazoEntregaRiel = 20 * time.Second

// arriendoEntregaRiel supera a plazoEntregaRiel para que otra pasada no reclame una fila todavía en vuelo.
const arriendoEntregaRiel = 30 * time.Second

// ErrRielSinRespuesta marca que el riel no respondió dentro de su propio plazo, no del contexto padre.
var ErrRielSinRespuesta = errors.New("nucleo: el riel no respondió dentro de su plazo")

// PayloadDespacho es el cuerpo de una fila de outbox que manda una transferencia al riel.
type PayloadDespacho struct {
	TransferenciaID uuid.UUID `json:"transferencia_id"`
}

// PoliticaReintentoOutbox es la espera creciente: min(Base*Factor^(intentos-1), Tope).
type PoliticaReintentoOutbox struct {
	Base   time.Duration
	Factor float64
	Tope   time.Duration
}

// PoliticaReintentoOutboxPorDefecto devuelve los valores resueltos para esta unidad: 30s, 2 y 30m.
func PoliticaReintentoOutboxPorDefecto() PoliticaReintentoOutbox {
	return PoliticaReintentoOutbox{Base: 30 * time.Second, Factor: 2, Tope: 30 * time.Minute}
}

// Demora devuelve la espera tras el intento fallido número intentos, con la política ya saneada.
func (p PoliticaReintentoOutbox) Demora(intentos int) time.Duration {
	p = p.saneada()
	if intentos <= 0 {
		return 0
	}
	demora := float64(p.Base) * math.Pow(p.Factor, float64(intentos-1))
	if demora >= float64(p.Tope) {
		return p.Tope
	}
	return time.Duration(demora)
}

// saneada reemplaza una política vacía o inválida por la de por defecto.
func (p PoliticaReintentoOutbox) saneada() PoliticaReintentoOutbox {
	if p.Base <= 0 || p.Factor <= 0 || p.Tope <= 0 {
		return PoliticaReintentoOutboxPorDefecto()
	}
	return p
}

// despachoReclamado es una fila de outbox ya registrada como en vuelo, lista para entregar al riel.
type despachoReclamado struct {
	outbox          repositorio.Outbox
	transferencia   repositorio.Transferencia
	transferenciaID uuid.UUID
}

// falloDeFilaOutbox marca el error de una fila irrecuperable, que se registra en ella sin cortar el barrido.
type falloDeFilaOutbox struct {
	id  int64
	err error
}

func (f falloDeFilaOutbox) Error() string { return f.err.Error() }

func (f falloDeFilaOutbox) Unwrap() error { return f.err }

// intervalo lleva una duración a la unidad de pgtype.Interval, que es el microsegundo.
func intervalo(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

// encolarDespacho inserta la intención de despachar dentro de la transacción de la reserva (capítulo 10).
func encolarDespacho(ctx context.Context, q *repositorio.Queries, transferenciaID uuid.UUID) (int64, error) {
	payload, err := json.Marshal(PayloadDespacho{TransferenciaID: transferenciaID})
	if err != nil {
		return 0, fmt.Errorf("nucleo: serializar el payload del outbox: %w", err)
	}
	fila, err := q.InsertarOutbox(ctx, repositorio.InsertarOutboxParams{
		Tipo:    TipoOutboxDespachoTransferencia,
		Payload: payload,
	})
	if err != nil {
		return 0, fmt.Errorf("nucleo: encolar el despacho de %s: %w", transferenciaID, err)
	}
	return fila.ID, nil
}

// DespacharOutbox registra el despacho de cada pendiente y lo entrega al riel, fuera de toda transacción.
func (s *Servicio) DespacharOutbox(ctx context.Context) error {
	var fallos []error
	for range longitudLoteOutbox {
		reclamada, err := s.reclamarSiguiente(ctx)
		if err != nil {
			var fallo falloDeFilaOutbox
			if errors.As(err, &fallo) {
				if _, _, errRegistrar := s.registrarFalloOutbox(ctx, fallo.id, fallo.err); errRegistrar != nil {
					fallos = append(fallos, errRegistrar)
				} else {
					fallos = append(fallos, fallo.err)
				}
				continue
			}
			fallos = append(fallos, err)
			break
		}
		if reclamada == nil {
			break
		}
		if err := s.entregarAlRiel(ctx, *reclamada); err != nil {
			fallos = append(fallos, err)
		}
	}
	return errors.Join(fallos...)
}

// reclamarSiguiente toma una sola fila pendiente y la reclama en su propia transacción.
func (s *Servicio) reclamarSiguiente(ctx context.Context) (*despachoReclamado, error) {
	return enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (*despachoReclamado, error) {
			filas, err := q.TomarOutboxPendientes(ctx, 1)
			if err != nil {
				return nil, fmt.Errorf("nucleo: tomar los pendientes del outbox: %w", err)
			}
			if len(filas) == 0 {
				return nil, nil
			}
			reclamada, err := reclamarDespacho(ctx, q, filas[0])
			if err != nil {
				return nil, falloDeFilaOutbox{id: filas[0].ID, err: err}
			}
			return &reclamada, nil
		})
}

// reclamarDespacho asienta el despacho si hace falta y adelanta la ventana para que nadie retome la fila durante la llamada (Criterio 12.1).
func reclamarDespacho(ctx context.Context, q *repositorio.Queries, fila repositorio.Outbox) (despachoReclamado, error) {
	if fila.Tipo != TipoOutboxDespachoTransferencia {
		return despachoReclamado{}, fmt.Errorf("nucleo: tipo de outbox desconocido %q en la fila %d", fila.Tipo, fila.ID)
	}
	var payload PayloadDespacho
	if err := json.Unmarshal(fila.Payload, &payload); err != nil {
		return despachoReclamado{}, fmt.Errorf("nucleo: leer el payload del outbox %d: %w", fila.ID, err)
	}
	if payload.TransferenciaID == uuid.Nil {
		return despachoReclamado{}, fmt.Errorf("nucleo: el outbox %d no trae transferencia_id", fila.ID)
	}
	transferencia, err := q.ObtenerTransferencia(ctx, pgtype.UUID{Bytes: payload.TransferenciaID, Valid: true})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return despachoReclamado{}, fmt.Errorf("nucleo: la transferencia %s del outbox no existe", payload.TransferenciaID)
		}
		return despachoReclamado{}, fmt.Errorf("nucleo: leer la transferencia %s del outbox: %w", payload.TransferenciaID, err)
	}
	if err := asentarDespacho(ctx, q, transferencia); err != nil {
		return despachoReclamado{}, err
	}
	filas, err := q.AvanzarVentanaOutbox(ctx, repositorio.AvanzarVentanaOutboxParams{
		ID:        fila.ID,
		Intervalo: intervalo(arriendoEntregaRiel),
	})
	if err != nil {
		return despachoReclamado{}, fmt.Errorf("nucleo: adelantar la ventana del outbox %d: %w", fila.ID, err)
	}
	if filas != 1 {
		return despachoReclamado{}, fmt.Errorf("nucleo: la ventana del outbox %d no quedó adelantada", fila.ID)
	}
	return despachoReclamado{outbox: fila, transferencia: transferencia, transferenciaID: payload.TransferenciaID}, nil
}

// asentarDespacho pasa la transferencia a despachada y escribe el asiento 3 una sola vez, con el compare-and-set como exclusión.
func asentarDespacho(ctx context.Context, q *repositorio.Queries, transferencia repositorio.Transferencia) error {
	if transferencia.Estado == EstadoTransferenciaDespachada {
		return nil
	}
	if transferencia.Estado != EstadoTransferenciaReservada {
		return fmt.Errorf("nucleo: la transferencia %s está en %q, no se puede despachar", uuid.UUID(transferencia.ID.Bytes), transferencia.Estado)
	}
	if _, err := q.AvanzarEstadoTransferencia(ctx, repositorio.AvanzarEstadoTransferenciaParams{
		ID:             transferencia.ID,
		EstadoEsperado: EstadoTransferenciaReservada,
		EstadoNuevo:    EstadoTransferenciaDespachada,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("nucleo: marcar despachada la transferencia %s: %w", uuid.UUID(transferencia.ID.Bytes), err)
	}
	return escribirAsientoDeDespacho(ctx, q, transferencia)
}

// escribirAsientoDeDespacho escribe el asiento 3 del Apéndice B: el retenido del emisor contra liquidación pendiente del riel.
func escribirAsientoDeDespacho(ctx context.Context, q *repositorio.Queries, transferencia repositorio.Transferencia) error {
	lineas, err := lineasDeDespacho(transferencia)
	if err != nil {
		return err
	}
	return insertarAsientoConLineas(ctx, q, TipoAsientoDespachoRiel, transferencia, time.Now().UTC(), pgtype.Text{}, lineas)
}

// lineasDeDespacho arma las dos líneas del asiento 3, que sacan el dinero retenido hacia el riel.
func lineasDeDespacho(transferencia repositorio.Transferencia) ([]repositorio.InsertarLineaParams, error) {
	monto := Centavos(transferencia.Monto)
	credito, err := Centavos(0).Restar(monto)
	if err != nil {
		return nil, err
	}
	return []repositorio.InsertarLineaParams{
		{CuentaID: NombreCuentaRetenido(transferencia.UsuarioID), Monto: monto.Int64(), Moneda: transferencia.Moneda},
		{CuentaID: CuentaLiquidacionPendienteRiel, Monto: credito.Int64(), Moneda: transferencia.Moneda},
	}, nil
}

// entregarAlRiel envía la orden y cierra el desenlace según la respuesta, sin transacción durante la llamada.
func (s *Servicio) entregarAlRiel(ctx context.Context, reclamada despachoReclamado) error {
	respuesta, err := s.enviarAlRiel(ctx, reclamada.transferencia)
	if err != nil {
		if errors.Is(err, ErrRielSinRespuesta) {
			return s.cerrarIndeterminada(ctx, reclamada)
		}
		return s.reintentarDespacho(ctx, reclamada, err)
	}
	switch respuesta.Resultado {
	case ResultadoConfirmado:
		return s.cerrarConfirmada(ctx, reclamada, respuesta)
	case ResultadoRechazado:
		return s.cerrarCompensada(ctx, reclamada, respuesta)
	default:
		return fmt.Errorf("nucleo: desenlace desconocido %q en la transferencia %s", respuesta.Resultado, reclamada.transferenciaID)
	}
}

// enviarAlRiel entrega la orden con el plazo del Criterio 16 y distingue su vencimiento del del padre.
func (s *Servicio) enviarAlRiel(ctx context.Context, transferencia repositorio.Transferencia) (RespuestaRiel, error) {
	if s.riel == nil {
		return RespuestaRiel{}, errors.New("nucleo: no hay riel configurado")
	}
	ctxRiel, cancel := context.WithTimeout(ctx, plazoEntregaRiel)
	defer cancel()
	respuesta, err := s.riel.Enviar(ctxRiel, ordenDeTransferencia(transferencia))
	if err != nil && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
		return RespuestaRiel{}, ErrRielSinRespuesta
	}
	return respuesta, err
}

// ordenDeTransferencia arma la orden con lo que el riel necesita para ejecutar y reconocer un reenvío.
func ordenDeTransferencia(transferencia repositorio.Transferencia) OrdenRiel {
	return OrdenRiel{
		TransferenciaID: uuid.UUID(transferencia.ID.Bytes),
		IdempotencyKey:  transferencia.IdempotencyKey,
		Llave:           transferencia.LlaveDestino,
		Monto:           Centavos(transferencia.Monto),
		Moneda:          transferencia.Moneda,
	}
}

// reintentarDespacho empuja la ventana con espera creciente y guarda el último error, sin tocar el asiento.
func (s *Servicio) reintentarDespacho(ctx context.Context, reclamada despachoReclamado, causa error) error {
	intentos, demora, err := s.registrarFalloOutbox(ctx, reclamada.outbox.ID, causa)
	if err != nil {
		return errors.Join(err, causa)
	}
	return fmt.Errorf("nucleo: el riel no recibió la transferencia %s (intento %d, reintento en %s): %w",
		reclamada.transferenciaID, intentos, demora, causa)
}

// registrarFalloOutbox incrementa intentos atómicamente y agenda el reintento con el reloj de la base.
func (s *Servicio) registrarFalloOutbox(ctx context.Context, id int64, causa error) (int, time.Duration, error) {
	var intentos int
	var demora time.Duration
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			frescos, err := q.ReintentarOutbox(ctx, repositorio.ReintentarOutboxParams{
				ID:          id,
				UltimoError: pgtype.Text{String: causa.Error(), Valid: true},
			})
			if err != nil {
				return struct{}{}, fmt.Errorf("nucleo: registrar el fallo del outbox %d: %w", id, err)
			}
			intentos = int(frescos)
			demora = s.politicaReintento.Demora(intentos)
			filas, err := q.AvanzarVentanaOutbox(ctx, repositorio.AvanzarVentanaOutboxParams{
				ID:        id,
				Intervalo: intervalo(demora),
			})
			if err != nil {
				return struct{}{}, fmt.Errorf("nucleo: reprogramar el reintento del outbox %d: %w", id, err)
			}
			if filas != 1 {
				return struct{}{}, fmt.Errorf("nucleo: el outbox %d no quedó reprogramado", id)
			}
			return struct{}{}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return intentos, demora, nil
}

// transicionCierre es un paso del cierre; solo aplica si la transferencia está en el estado esperado.
type transicionCierre struct {
	esperado string
	nuevo    string
}

// cerrarConfirmada escribe el asiento 4a y lleva la transferencia a confirmada en su propia transacción (Criterio 14).
func (s *Servicio) cerrarConfirmada(ctx context.Context, reclamada despachoReclamado, respuesta RespuestaRiel) error {
	transiciones := []transicionCierre{{esperado: EstadoTransferenciaDespachada, nuevo: EstadoTransferenciaConfirmada}}
	asentar := func(ctx context.Context, q *repositorio.Queries) error {
		return escribirAsientoDeConfirmacion(ctx, q, reclamada.transferencia, respuesta.OcurridoEn)
	}
	return s.cerrarDespacho(ctx, reclamada, transiciones, asentar)
}

// cerrarCompensada escribe el asiento 4b y lleva la transferencia a compensada conservando el motivo (Criterio 15).
func (s *Servicio) cerrarCompensada(ctx context.Context, reclamada despachoReclamado, respuesta RespuestaRiel) error {
	transiciones := []transicionCierre{
		{esperado: EstadoTransferenciaDespachada, nuevo: EstadoTransferenciaFallida},
		{esperado: EstadoTransferenciaFallida, nuevo: EstadoTransferenciaCompensada},
	}
	asentar := func(ctx context.Context, q *repositorio.Queries) error {
		return escribirAsientoDeCompensacion(ctx, q, reclamada.transferencia, respuesta.Motivo, respuesta.OcurridoEn)
	}
	return s.cerrarDespacho(ctx, reclamada, transiciones, asentar)
}

// cerrarIndeterminada no escribe asiento y deja la transferencia sin desenlace conocido (Criterio 16).
func (s *Servicio) cerrarIndeterminada(ctx context.Context, reclamada despachoReclamado) error {
	transiciones := []transicionCierre{{esperado: EstadoTransferenciaDespachada, nuevo: EstadoTransferenciaIndeterminada}}
	return s.cerrarDespacho(ctx, reclamada, transiciones, nil)
}

// cerrarDespacho aplica las transiciones y el asiento del desenlace en una transacción, y marca la fila entregada.
func (s *Servicio) cerrarDespacho(ctx context.Context, reclamada despachoReclamado, transiciones []transicionCierre, asentar func(context.Context, *repositorio.Queries) error) error {
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			aplicado, err := aplicarCierre(ctx, q, reclamada.transferencia.ID, transiciones)
			if err != nil {
				return struct{}{}, err
			}
			if aplicado && asentar != nil {
				if err := asentar(ctx, q); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, marcarOutboxDespachado(ctx, q, reclamada.outbox.ID)
		})
	return err
}

// aplicarCierre mueve la transferencia por sus transiciones; sin la primera fila otro cierre ya ganó (Criterio 17).
func aplicarCierre(ctx context.Context, q *repositorio.Queries, id pgtype.UUID, transiciones []transicionCierre) (bool, error) {
	for i, transicion := range transiciones {
		_, err := q.AvanzarEstadoTransferencia(ctx, repositorio.AvanzarEstadoTransferenciaParams{
			ID:             id,
			EstadoEsperado: transicion.esperado,
			EstadoNuevo:    transicion.nuevo,
		})
		if err == nil {
			continue
		}
		if i == 0 && errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("nucleo: cerrar la transferencia en %q: %w", transicion.nuevo, err)
	}
	return true, nil
}

// escribirAsientoDeConfirmacion escribe el asiento 4a del Apéndice B: el dinero en vuelo sale del custodio.
func escribirAsientoDeConfirmacion(ctx context.Context, q *repositorio.Queries, transferencia repositorio.Transferencia, ocurridoEn time.Time) error {
	lineas, err := lineasDeConfirmacion(transferencia)
	if err != nil {
		return err
	}
	return insertarAsientoConLineas(ctx, q, TipoAsientoLiquidacionConfirmada, transferencia, ocurridoEn, pgtype.Text{}, lineas)
}

// lineasDeConfirmacion arma las dos líneas del asiento 4a contra la cuenta del custodio.
func lineasDeConfirmacion(transferencia repositorio.Transferencia) ([]repositorio.InsertarLineaParams, error) {
	monto := Centavos(transferencia.Monto)
	credito, err := Centavos(0).Restar(monto)
	if err != nil {
		return nil, err
	}
	return []repositorio.InsertarLineaParams{
		{CuentaID: CuentaLiquidacionPendienteRiel, Monto: monto.Int64(), Moneda: transferencia.Moneda},
		{CuentaID: CuentaCustodiaBanco, Monto: credito.Int64(), Moneda: transferencia.Moneda},
	}, nil
}

// escribirAsientoDeCompensacion escribe el asiento 4b leyendo de la retención el GMF que haya que devolver.
func escribirAsientoDeCompensacion(ctx context.Context, q *repositorio.Queries, transferencia repositorio.Transferencia, motivo string, ocurridoEn time.Time) error {
	gravamen, err := q.GravamenRetenidoDeTransferencia(ctx, repositorio.GravamenRetenidoDeTransferenciaParams{
		TransferenciaID: transferencia.ID,
		Tipo:            TipoAsientoRetencion,
		CuentaID:        CuentaGMFPorPagar,
	})
	if err != nil {
		return fmt.Errorf("nucleo: leer el gravamen retenido de %s: %w", uuid.UUID(transferencia.ID.Bytes), err)
	}
	lineas, err := lineasDeCompensacion(transferencia, Centavos(gravamen))
	if err != nil {
		return err
	}
	return insertarAsientoConLineas(ctx, q, TipoAsientoCompensacion, transferencia, ocurridoEn, motivoDeRechazo(motivo), lineas)
}

// lineasDeCompensacion arma las dos líneas del asiento 4b y, con gravamen, las dos de su devolución (HU-NF-10, criterio 15.2).
func lineasDeCompensacion(transferencia repositorio.Transferencia, gravamen Centavos) ([]repositorio.InsertarLineaParams, error) {
	if gravamen < 0 {
		return nil, ErrMontoInvalido
	}
	monto := Centavos(transferencia.Monto)
	creditoMonto, err := Centavos(0).Restar(monto)
	if err != nil {
		return nil, err
	}
	lineas := []repositorio.InsertarLineaParams{
		{CuentaID: CuentaLiquidacionPendienteRiel, Monto: monto.Int64(), Moneda: transferencia.Moneda},
		{CuentaID: NombreCuentaDisponible(transferencia.UsuarioID), Monto: creditoMonto.Int64(), Moneda: transferencia.Moneda},
	}
	if gravamen == 0 {
		return lineas, nil
	}
	creditoGravamen, err := Centavos(0).Restar(gravamen)
	if err != nil {
		return nil, err
	}
	return append(lineas,
		repositorio.InsertarLineaParams{CuentaID: CuentaGMFPorPagar, Monto: gravamen.Int64(), Moneda: transferencia.Moneda},
		repositorio.InsertarLineaParams{CuentaID: NombreCuentaDisponible(transferencia.UsuarioID), Monto: creditoGravamen.Int64(), Moneda: transferencia.Moneda},
	), nil
}

// motivoDeRechazo lleva el motivo del riel a la descripción del asiento, que es donde se conserva.
func motivoDeRechazo(motivo string) pgtype.Text {
	if motivo == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: motivo, Valid: true}
}

// insertarAsientoConLineas escribe la cabecera y las líneas de un asiento del desenlace en la transacción en curso.
func insertarAsientoConLineas(ctx context.Context, q *repositorio.Queries, tipo string, transferencia repositorio.Transferencia, ocurridoEn time.Time, descripcion pgtype.Text, lineas []repositorio.InsertarLineaParams) error {
	asiento, err := q.InsertarAsiento(ctx, repositorio.InsertarAsientoParams{
		Tipo:            tipo,
		TransferenciaID: transferencia.ID,
		OcurridoEn:      pgtype.Timestamptz{Time: ocurridoEn.UTC(), Valid: true},
		Descripcion:     descripcion,
	})
	if err != nil {
		return fmt.Errorf("nucleo: asentar %q de %s: %w", tipo, uuid.UUID(transferencia.ID.Bytes), err)
	}
	for _, linea := range lineas {
		linea.EntryID = asiento.ID
		if _, err := q.InsertarLinea(ctx, linea); err != nil {
			return fmt.Errorf("nucleo: escribir una línea del asiento %q de %s: %w", tipo, uuid.UUID(transferencia.ID.Bytes), err)
		}
	}
	return nil
}

// marcarOutboxDespachado cierra la fila de la bandeja dentro de la transacción del desenlace.
func marcarOutboxDespachado(ctx context.Context, q *repositorio.Queries, id int64) error {
	filas, err := q.MarcarOutboxDespachado(ctx, id)
	if err != nil {
		return fmt.Errorf("nucleo: marcar despachado el outbox %d: %w", id, err)
	}
	if filas != 1 {
		return fmt.Errorf("nucleo: el outbox %d no quedó marcado como despachado", id)
	}
	return nil
}
