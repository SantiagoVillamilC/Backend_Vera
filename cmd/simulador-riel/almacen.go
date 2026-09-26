// Package main es el simulador del riel de transferencias (Bre-B): resuelve llaves contra un
// directorio y despacha transferencias interbancarias, sin tocar la red real.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

// formatoInstante fija el ancho del instante para que el orden lexicográfico sea cronológico.
const formatoInstante = "2006-01-02T15:04:05.000000000Z07:00"

// Los dos desenlaces definitivos que el simulador registra en su propio almacén.
const (
	estadoConfirmada = "confirmada"
	estadoRechazada  = "rechazada"
)

// motivoFondosInsuficientes es el único motivo de rechazo formal documentado en Bre-B.
const motivoFondosInsuficientes = "fondos_insuficientes"

// ErrOperacionNoEncontrada reporta una transferencia que el simulador no registró.
var ErrOperacionNoEncontrada = errors.New("simulador-riel: la transferencia no está registrada")

// ErrOperacionEnConflicto reporta un reenvío del mismo identificador con otros datos.
var ErrOperacionEnConflicto = errors.New("simulador-riel: la transferencia ya existe con otros datos")

// Operacion es una fila de la tabla propia del simulador del riel.
type Operacion struct {
	ID              int64
	TransferenciaID string
	IdempotencyKey  string
	Llave           string
	MontoCentavos   Centavos
	Moneda          string
	Estado          string
	Motivo          string
	OcurridoEn      time.Time
	RegistradoEn    time.Time
}

// Almacen es el SQLite propio del simulador, separado de la base del núcleo.
type Almacen struct{ db *sql.DB }

// AbrirAlmacen abre el archivo SQLite, restringe sus permisos y crea el esquema si falta.
func AbrirAlmacen(ruta string) (*Almacen, error) {
	if err := crearArchivo(ruta); err != nil {
		return nil, fmt.Errorf("crear sqlite %q: %w", ruta, err)
	}
	db, err := sql.Open("sqlite", ruta)
	if err != nil {
		return nil, fmt.Errorf("abrir sqlite %q: %w", ruta, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(context.Background(), "PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	restringirArchivo(ruta)
	if _, err := db.ExecContext(context.Background(), esquema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("crear esquema: %w", err)
	}
	return &Almacen{db: db}, nil
}

// crearArchivo deja la base en 0600 antes de que SQLite la abra, de donde hereda el modo de sus sidecars.
func crearArchivo(ruta string) error {
	f, err := os.OpenFile(ruta, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(ruta, 0o600)
}

// restringirArchivo deja en 0600 la base y los sidecars que ya existan, sin fallar si el sistema no lo permite.
func restringirArchivo(ruta string) {
	for _, sufijo := range []string{"", "-journal", "-wal", "-shm"} {
		_ = os.Chmod(ruta+sufijo, 0o600)
	}
}

// Cerrar cierra la conexión con la base propia del simulador.
func (a *Almacen) Cerrar() error { return a.db.Close() }

// esquema es la tabla de operaciones del riel, con el identificador único como idempotencia.
const esquema = `
CREATE TABLE IF NOT EXISTS operacion (
  id               INTEGER PRIMARY KEY,
  transferencia_id TEXT UNIQUE NOT NULL,
  idempotency_key  TEXT NOT NULL,
  llave            TEXT NOT NULL,
  monto_centavos   INTEGER NOT NULL CHECK (monto_centavos > 0),
  moneda           TEXT NOT NULL,
  estado           TEXT NOT NULL CHECK (estado IN ('confirmada', 'rechazada')),
  motivo           TEXT NOT NULL DEFAULT '',
  ocurrido_en      TEXT NOT NULL,
  registrado_en    TEXT NOT NULL
);`

// Registrar inserta la operación y, si el identificador ya existía, devuelve la existente con la marca de reintento.
func (a *Almacen) Registrar(ctx context.Context, o Operacion, ahora time.Time) (Operacion, bool, error) {
	registrado := ahora.UTC().Format(formatoInstante)
	res, err := a.db.ExecContext(ctx,
		`INSERT INTO operacion (transferencia_id, idempotency_key, llave,
		                        monto_centavos, moneda, estado, motivo, ocurrido_en, registrado_en)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (transferencia_id) DO NOTHING`,
		o.TransferenciaID, o.IdempotencyKey, o.Llave,
		int64(o.MontoCentavos), o.Moneda, o.Estado, o.Motivo,
		o.OcurridoEn.UTC().Format(formatoInstante), registrado)
	if err != nil {
		return Operacion{}, false, fmt.Errorf("insertar operación: %w", err)
	}
	filas, err := res.RowsAffected()
	if err != nil {
		return Operacion{}, false, fmt.Errorf("filas de la operación: %w", err)
	}
	if filas == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return Operacion{}, false, fmt.Errorf("id de la operación: %w", err)
		}
		o.ID, o.OcurridoEn, o.RegistradoEn = id, o.OcurridoEn.UTC(), ahora.UTC()
		return o, false, nil
	}
	return a.reintento(ctx, o)
}

// reintento compara el reenvío con la fila ya registrada: mismo identificador y datos, o conflicto.
func (a *Almacen) reintento(ctx context.Context, o Operacion) (Operacion, bool, error) {
	existente, err := a.PorTransferencia(ctx, o.TransferenciaID)
	if err != nil {
		return Operacion{}, false, err
	}
	if existente.IdempotencyKey != o.IdempotencyKey || existente.MontoCentavos != o.MontoCentavos || existente.Moneda != o.Moneda || existente.Llave != o.Llave {
		return existente, false, ErrOperacionEnConflicto
	}
	return existente, true, nil
}

// PorTransferencia devuelve la operación de ese identificador, o ErrOperacionNoEncontrada si no existe.
func (a *Almacen) PorTransferencia(ctx context.Context, transferenciaID string) (Operacion, error) {
	rows, err := a.db.QueryContext(ctx, consultaOperacion+` WHERE transferencia_id = ?`, transferenciaID)
	if err != nil {
		return Operacion{}, fmt.Errorf("leer la operación %q: %w", transferenciaID, err)
	}
	defer func() { _ = rows.Close() }()
	operaciones, err := escanearOperaciones(rows)
	if err != nil {
		return Operacion{}, err
	}
	if len(operaciones) == 0 {
		return Operacion{}, ErrOperacionNoEncontrada
	}
	return operaciones[0], nil
}

// Operaciones devuelve todas las operaciones, para inspección y para las pruebas.
func (a *Almacen) Operaciones(ctx context.Context) ([]Operacion, error) {
	rows, err := a.db.QueryContext(ctx, consultaOperacion+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("leer operaciones: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return escanearOperaciones(rows)
}

// EnVentana devuelve las operaciones ocurridas en [desde, hasta), sin incluir el extremo hasta.
func (a *Almacen) EnVentana(ctx context.Context, desde, hasta time.Time) ([]Operacion, error) {
	rows, err := a.db.QueryContext(ctx, consultaOperacion+` WHERE ocurrido_en >= ? AND ocurrido_en < ? ORDER BY ocurrido_en, id`,
		desde.UTC().Format(formatoInstante), hasta.UTC().Format(formatoInstante))
	if err != nil {
		return nil, fmt.Errorf("leer la ventana: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return escanearOperaciones(rows)
}

// consultaOperacion es la lectura común de una fila de operación.
const consultaOperacion = `
SELECT id, transferencia_id, idempotency_key, llave,
       monto_centavos, moneda, estado, motivo, ocurrido_en, registrado_en FROM operacion`

// escanearOperaciones materializa las filas leídas con los instantes ya convertidos.
func escanearOperaciones(rows *sql.Rows) ([]Operacion, error) {
	operaciones := make([]Operacion, 0, 4)
	for rows.Next() {
		var (
			o                    Operacion
			ocurrido, registrado string
		)
		if err := rows.Scan(&o.ID, &o.TransferenciaID, &o.IdempotencyKey, &o.Llave,
			&o.MontoCentavos, &o.Moneda, &o.Estado, &o.Motivo, &ocurrido, &registrado); err != nil {
			return nil, fmt.Errorf("leer operación: %w", err)
		}
		var err error
		if o.OcurridoEn, err = parsearInstante(ocurrido); err != nil {
			return nil, err
		}
		if o.RegistradoEn, err = parsearInstante(registrado); err != nil {
			return nil, err
		}
		operaciones = append(operaciones, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recorrer operaciones: %w", err)
	}
	return operaciones, nil
}

// parsearInstante lee el formato fijo con el que se guardan las marcas de tiempo.
func parsearInstante(s string) (time.Time, error) {
	t, err := time.Parse(formatoInstante, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("instante %q: %w", s, err)
	}
	return t.UTC(), nil
}
