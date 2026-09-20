package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/jobs"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// longitudLoteAvisos acota cuántas filas entrega cada barrido del outbox.
const longitudLoteAvisos = 50

// plazoEntregaAviso es el tope de una entrega, menor que los 30 s del apagado.
const plazoEntregaAviso = 20 * time.Second

// arriendoEntregaAviso supera al plazo para que otro barrido no reclame una fila todavía en vuelo.
const arriendoEntregaAviso = 30 * time.Second

// PoliticaReintento es la espera creciente del outbox de auth: min(Base*Factor^(intentos-1), Tope).
type PoliticaReintento struct {
	Base   time.Duration
	Factor float64
	Tope   time.Duration
}

// PoliticaReintentoPorDefecto son los valores de respaldo de la espera creciente del outbox de auth.
func PoliticaReintentoPorDefecto() PoliticaReintento {
	return PoliticaReintento{Base: 5 * time.Second, Factor: 2, Tope: 5 * time.Minute}
}

// Demora devuelve la espera tras el intento fallido número intentos, con la política ya saneada.
func (p PoliticaReintento) Demora(intentos int) time.Duration {
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
func (p PoliticaReintento) saneada() PoliticaReintento {
	if p.Base <= 0 || p.Factor <= 0 || p.Tope <= 0 {
		return PoliticaReintentoPorDefecto()
	}
	return p
}

// ManejadorDeAviso ejecuta el efecto de un tipo de aviso y devuelve el id del proveedor si lo hubo.
type ManejadorDeAviso func(ctx context.Context, tipo string, payload []byte) (IDMensaje string, err error)

// avisoReclamado es una fila de outbox ya arrendada, lista para entregar fuera de la transacción.
type avisoReclamado struct {
	id      int64
	tipo    string
	payload []byte
}

// almacenDeAvisos es la persistencia que el despachador necesita del outbox de auth.
type almacenDeAvisos interface {
	ReclamarSiguiente(ctx context.Context) (*avisoReclamado, error)
	RegistrarFallo(ctx context.Context, id int64, ultimoError string) error
	MarcarEntregado(ctx context.Context, id int64, idMensaje string) error
}

// almacenPostgres es la persistencia real del outbox, con el reclamo y el arriendo del núcleo.
type almacenPostgres struct {
	pool     *pgxpool.Pool
	politica PoliticaReintento
}

// ReclamarSiguiente arrienda una fila pendiente en su propia transacción con FOR UPDATE SKIP LOCKED.
func (a *almacenPostgres) ReclamarSiguiente(ctx context.Context) (*avisoReclamado, error) {
	return enTransaccion(ctx, poolDeAuth{pool: a.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (*avisoReclamado, error) {
			filas, err := q.TomarAvisosPendientes(ctx, 1)
			if err != nil {
				return nil, fmt.Errorf("auth: tomar los avisos pendientes del outbox: %w", err)
			}
			if len(filas) == 0 {
				return nil, nil
			}
			fila := filas[0]
			if err := avanzarVentanaAviso(ctx, q, fila.ID, arriendoEntregaAviso); err != nil {
				return nil, err
			}
			return &avisoReclamado{id: fila.ID, tipo: fila.Tipo, payload: fila.Payload}, nil
		})
}

// RegistrarFallo incrementa intentos y reprograma el aviso con la espera de la política.
func (a *almacenPostgres) RegistrarFallo(ctx context.Context, id int64, ultimoError string) error {
	_, err := enTransaccion(ctx, poolDeAuth{pool: a.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			frescos, err := q.ReintentarAviso(ctx, repositorio.ReintentarAvisoParams{
				ID:          id,
				UltimoError: pgtype.Text{String: ultimoError, Valid: true},
			})
			if err != nil {
				return struct{}{}, fmt.Errorf("auth: registrar el fallo del aviso %d: %w", id, err)
			}
			if err := avanzarVentanaAviso(ctx, q, id, a.politica.Demora(int(frescos))); err != nil {
				return struct{}{}, err
			}
			return struct{}{}, nil
		})
	return err
}

// MarcarEntregado cierra la fila con el id del proveedor, o lo deja nulo si el aviso no envió nada.
func (a *almacenPostgres) MarcarEntregado(ctx context.Context, id int64, idMensaje string) error {
	_, err := enTransaccion(ctx, poolDeAuth{pool: a.pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			filas, err := q.MarcarAvisoEntregado(ctx, repositorio.MarcarAvisoEntregadoParams{
				ID:                 id,
				IDMensajeProveedor: pgtype.Text{String: idMensaje, Valid: idMensaje != ""},
			})
			if err != nil {
				return struct{}{}, fmt.Errorf("auth: marcar entregado el aviso %d: %w", id, err)
			}
			if filas != 1 {
				return struct{}{}, fmt.Errorf("auth: el aviso %d no quedó marcado como entregado", id)
			}
			return struct{}{}, nil
		})
	return err
}

// avanzarVentanaAviso adelanta disponible_en para arrendar o reintentar el aviso.
func avanzarVentanaAviso(ctx context.Context, q *repositorio.Queries, id int64, ventana time.Duration) error {
	filas, err := q.AvanzarVentanaAviso(ctx, repositorio.AvanzarVentanaAvisoParams{
		ID:        id,
		Intervalo: intervaloDeAviso(ventana),
	})
	if err != nil {
		return fmt.Errorf("auth: adelantar la ventana del aviso %d: %w", id, err)
	}
	if filas != 1 {
		return fmt.Errorf("auth: la ventana del aviso %d no quedó adelantada", id)
	}
	return nil
}

// intervaloDeAviso lleva una duración a la unidad de pgtype.Interval, que es el microsegundo.
func intervaloDeAviso(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

// DespachadorDeAvisos reclama avisos del outbox y los entrega fuera de toda transacción.
type DespachadorDeAvisos struct {
	almacen     almacenDeAvisos
	manejadores map[string]ManejadorDeAviso
}

// nuevoDespachadorDeAvisos liga los cuatro avisos de notificación al notificador.
func nuevoDespachadorDeAvisos(almacen almacenDeAvisos, notificador Notificador) *DespachadorDeAvisos {
	d := &DespachadorDeAvisos{almacen: almacen, manejadores: map[string]ManejadorDeAviso{}}
	for _, tipo := range []string{TipoAvisoContrasenaCambiada, TipoAvisoCambioNumero, TipoAvisoRecuperacionSolicitada, TipoAvisoRegistroDuplicado} {
		d.manejadores[tipo] = manejadorDeNotificacion(notificador)
	}
	return d
}

// NuevoDespachadorDeAvisos construye el despachador sobre el pool, con el notificador y la política.
func NuevoDespachadorDeAvisos(pool *pgxpool.Pool, notificador Notificador, politica PoliticaReintento) *DespachadorDeAvisos {
	return nuevoDespachadorDeAvisos(&almacenPostgres{pool: pool, politica: politica}, notificador)
}

// Registrar añade o reemplaza el handler de un tipo de aviso que trae su propio efecto.
func (d *DespachadorDeAvisos) Registrar(tipo string, manejador ManejadorDeAviso) {
	d.manejadores[tipo] = manejador
}

// DespacharAvisos entrega los avisos pendientes uno a uno hasta agotar el lote o el contexto.
func (d *DespachadorDeAvisos) DespacharAvisos(ctx context.Context) error {
	var fallos []error
	for range longitudLoteAvisos {
		if ctx.Err() != nil {
			break
		}
		reclamado, err := d.almacen.ReclamarSiguiente(ctx)
		if err != nil {
			fallos = append(fallos, err)
			break
		}
		if reclamado == nil {
			break
		}
		manejador, ok := d.manejadores[reclamado.tipo]
		if !ok {
			err := fmt.Errorf("auth: tipo de aviso desconocido %q en la fila %d", reclamado.tipo, reclamado.id)
			if errRegistrar := d.registrarFallo(ctx, *reclamado, err); errRegistrar != nil {
				fallos = append(fallos, errors.Join(err, errRegistrar))
			} else {
				fallos = append(fallos, err)
			}
			continue
		}
		if err := d.entregar(ctx, *reclamado, manejador); err != nil {
			fallos = append(fallos, err)
		}
	}
	return errors.Join(fallos...)
}

// entregar llama al handler fuera de la transacción y cierra la fila según el desenlace.
func (d *DespachadorDeAvisos) entregar(ctx context.Context, reclamado avisoReclamado, manejador ManejadorDeAviso) error {
	ctxEntrega, cancel := context.WithTimeout(ctx, plazoEntregaAviso)
	defer cancel()
	idMensaje, err := manejador(ctxEntrega, reclamado.tipo, reclamado.payload)
	if err != nil {
		if errRegistrar := d.registrarFallo(ctx, reclamado, err); errRegistrar != nil {
			return errors.Join(err, errRegistrar)
		}
		return err
	}
	return d.almacen.MarcarEntregado(ctx, reclamado.id, idMensaje)
}

// registrarFallo persiste el error sin la dirección del payload y reprograma el aviso.
func (d *DespachadorDeAvisos) registrarFallo(ctx context.Context, reclamado avisoReclamado, causa error) error {
	return d.almacen.RegistrarFallo(ctx, reclamado.id, ultimoErrorDeAviso(causa, reclamado.payload))
}

// manejadorDeNotificacion decodifica el payload y entrega el aviso por el notificador.
func manejadorDeNotificacion(notificador Notificador) ManejadorDeAviso {
	return func(ctx context.Context, tipo string, payload []byte) (string, error) {
		var fila payloadAviso
		if err := json.Unmarshal(payload, &fila); err != nil {
			return "", fmt.Errorf("auth: leer el payload del aviso: %w", err)
		}
		return notificador.Enviar(ctx, Aviso{
			Tipo:             tipo,
			Destino:          fila.Destino,
			Correo:           fila.Correo,
			Celular:          fila.Celular,
			Plantilla:        fila.Plantilla,
			ParteID:          fila.ParteID,
			NuevoEnmascarado: fila.NuevoEnmascarado,
			Fecha:            fila.Fecha,
			ContactoSoporte:  fila.ContactoSoporte,
		})
	}
}

// ultimoErrorDeAviso redacta del error las direcciones del payload antes de persistirlo.
func ultimoErrorDeAviso(causa error, payload []byte) string {
	mensaje := causa.Error()
	var datos map[string]json.RawMessage
	if err := json.Unmarshal(payload, &datos); err != nil {
		return mensaje
	}
	for _, clave := range []string{"correo", "celular"} {
		var valor string
		if raw, ok := datos[clave]; ok && json.Unmarshal(raw, &valor) == nil && valor != "" {
			mensaje = strings.ReplaceAll(mensaje, valor, "[redactado]")
		}
	}
	return mensaje
}

// DespacharAvisosArgs no lleva datos: el barrido del outbox de auth no recibe parámetros.
type DespacharAvisosArgs struct{}

// Kind es el nombre estable de la fila encolada.
func (DespacharAvisosArgs) Kind() string { return "auth_despachar_avisos" }

// DespacharAvisosWorker entrega los avisos pendientes sobre la cola auth.
type DespacharAvisosWorker struct {
	river.WorkerDefaults[DespacharAvisosArgs]
	despachador *DespachadorDeAvisos
}

// NuevoDespacharAvisosWorker construye el worker del outbox con su despachador.
func NuevoDespacharAvisosWorker(despachador *DespachadorDeAvisos) *DespacharAvisosWorker {
	return &DespacharAvisosWorker{despachador: despachador}
}

// Work entrega el lote de avisos y propaga el error para que River reintente.
func (w *DespacharAvisosWorker) Work(ctx context.Context, _ *river.Job[DespacharAvisosArgs]) error {
	return w.despachador.DespacharAvisos(ctx)
}

// TrabajoPeriodicoDeOutbox arma el trabajo periódico del barrido para la cola auth.
func TrabajoPeriodicoDeOutbox(intervalo time.Duration) jobs.TrabajoPeriodicoDeAuth {
	return jobs.TrabajoPeriodicoDeAuth{
		ID:      "auth_despachar_avisos",
		Periodo: intervalo,
		Args: func() (river.JobArgs, *river.InsertOpts) {
			return DespacharAvisosArgs{}, &river.InsertOpts{Queue: jobs.ColaAuth}
		},
	}
}
