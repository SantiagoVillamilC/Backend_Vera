package nucleo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo/internal/repositorio"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ventanaConciliacion es el rango por tiempo de evento de una corrida, con lo seleccionado y los dos extractos ya leídos.
type ventanaConciliacion struct {
	desde            time.Time
	hasta            time.Time
	asientos         []repositorio.JournalEntry
	transferencias   []repositorio.LeerTransferenciasActivasDeVentanaRow
	extractoRiel     ExtractoRiel
	extractoCustodio ExtractoCustodio
}

// detectorDeVentana es el punto de enganche de NF-A4-02.2: recibe la ventana y devuelve cuántas diferencias registró.
type detectorDeVentana func(ctx context.Context, q *repositorio.Queries, ventana ventanaConciliacion) (int, error)

// sinDiferenciasDeVentana es el relleno que NF-A4-02.2 reemplaza por las reglas de resolución por tipo.
func sinDiferenciasDeVentana(context.Context, *repositorio.Queries, ventanaConciliacion) (int, error) {
	return 0, nil
}

// rangoDeConciliacion arma [desde, hasta): hasta = ahora - N y desde el final de la corrida anterior (HU-NF-08, criterios 1 y 2).
func rangoDeConciliacion(ultima pgtype.Timestamptz, ahora time.Time, antiguedad time.Duration) (desde, hasta time.Time) {
	hasta = ahora.Add(-antiguedad).UTC()
	if !ultima.Valid {
		return hasta, hasta
	}
	return ultima.Time.UTC(), hasta
}

// Conciliar corre una ventana: selecciona por tiempo de evento, lee los extractos y avanza el cursor (HU-NF-08).
func (s *Servicio) Conciliar(ctx context.Context, antiguedad time.Duration) (int, error) {
	if antiguedad <= 0 {
		return 0, fmt.Errorf("nucleo: la antigüedad de conciliación debe ser positiva, recibí %s", antiguedad)
	}
	if s.lectorRiel == nil {
		return 0, ErrRielNoDisponible
	}
	if s.custodio == nil {
		return 0, ErrCustodioNoDisponible
	}
	ultima, err := s.ultimaVentanaConciliada(ctx)
	if err != nil {
		return 0, err
	}
	desde, hasta := rangoDeConciliacion(ultima, s.momentoActual(), antiguedad)
	if !ultima.Valid {
		return 0, s.insertarCorrida(ctx, hasta)
	}
	if !hasta.After(desde) {
		return 0, nil
	}
	// Los dos extractos se piden antes de abrir la transacción de escritura (HU-NF-08, criterio 17).
	extractoRiel, err := s.lectorRiel.Extracto(ctx, desde, hasta)
	if err != nil {
		return 0, fmt.Errorf("nucleo: conciliar: leer el extracto del riel: %w", err)
	}
	extractoCustodio, err := s.custodio.Extracto(ctx, hasta)
	if err != nil {
		return 0, fmt.Errorf("nucleo: conciliar: leer el extracto del custodio: %w", err)
	}
	return s.conciliarVentana(ctx, desde, hasta, extractoRiel, extractoCustodio)
}

// conciliarVentana lee la ventana y la entrega al detector dentro de una sola transacción de escritura.
func (s *Servicio) conciliarVentana(ctx context.Context, desde, hasta time.Time, extractoRiel ExtractoRiel, extractoCustodio ExtractoCustodio) (int, error) {
	detectar := s.detectar
	if detectar == nil {
		detectar = sinDiferenciasDeVentana
	}
	diferencias := 0
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			ventana, err := leerVentanaConciliacion(ctx, q, desde, hasta, extractoRiel, extractoCustodio)
			if err != nil {
				return struct{}{}, err
			}
			n, err := detectar(ctx, q, ventana)
			if err != nil {
				return struct{}{}, err
			}
			diferencias = n
			return struct{}{}, insertarCorridaEnTx(ctx, q, hasta)
		})
	if err != nil {
		return 0, err
	}
	return diferencias, nil
}

// leerVentanaConciliacion lee los asientos y las transferencias activas del rango [desde, hasta).
func leerVentanaConciliacion(ctx context.Context, q *repositorio.Queries, desde, hasta time.Time, extractoRiel ExtractoRiel, extractoCustodio ExtractoCustodio) (ventanaConciliacion, error) {
	intervalo := repositorio.LeerAsientosDeVentanaParams{
		Desde: pgtype.Timestamptz{Time: desde, Valid: true},
		Hasta: pgtype.Timestamptz{Time: hasta, Valid: true},
	}
	asientos, err := q.LeerAsientosDeVentana(ctx, intervalo)
	if err != nil {
		return ventanaConciliacion{}, fmt.Errorf("nucleo: leer los asientos de la ventana: %w", err)
	}
	transferencias, err := q.LeerTransferenciasActivasDeVentana(ctx, repositorio.LeerTransferenciasActivasDeVentanaParams(intervalo))
	if err != nil {
		return ventanaConciliacion{}, fmt.Errorf("nucleo: leer las transferencias activas de la ventana: %w", err)
	}
	return ventanaConciliacion{
		desde:            desde,
		hasta:            hasta,
		asientos:         asientos,
		transferencias:   transferencias,
		extractoRiel:     extractoRiel,
		extractoCustodio: extractoCustodio,
	}, nil
}

// ultimaVentanaConciliada lee el cursor; sin corridas previas devuelve un timestamptz inválido.
func (s *Servicio) ultimaVentanaConciliada(ctx context.Context) (pgtype.Timestamptz, error) {
	var ultima pgtype.Timestamptz
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			v, err := q.UltimaVentanaConciliada(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				return struct{}{}, nil
			}
			if err != nil {
				return struct{}{}, fmt.Errorf("nucleo: leer el cursor de conciliación: %w", err)
			}
			ultima = v
			return struct{}{}, nil
		})
	return ultima, err
}

// insertarCorrida siembra el cursor en la primera corrida, cuya ventana es vacía y no se concilia.
func (s *Servicio) insertarCorrida(ctx context.Context, hasta time.Time) error {
	_, err := enTransaccion(ctx, s.pool,
		func(ctx context.Context, _ transaccionDeNucleo, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, insertarCorridaEnTx(ctx, q, hasta)
		})
	return err
}

// insertarCorridaEnTx guarda el final de la ventana de la corrida en el cursor append-only.
func insertarCorridaEnTx(ctx context.Context, q *repositorio.Queries, hasta time.Time) error {
	_, err := q.InsertarCorridaConciliacion(ctx, pgtype.Timestamptz{Time: hasta, Valid: true})
	if err != nil {
		return fmt.Errorf("nucleo: guardar la corrida de conciliación: %w", err)
	}
	return nil
}

// momentoActual usa el reloj inyectado y cae al reloj real cuando el Servicio se armó a mano.
func (s *Servicio) momentoActual() time.Time {
	if s.ahora == nil {
		return time.Now().UTC()
	}
	return s.ahora().UTC()
}
