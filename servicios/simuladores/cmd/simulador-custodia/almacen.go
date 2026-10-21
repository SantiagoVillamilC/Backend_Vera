// Package main es el simulador del banco custodio: recibe notificaciones de depósito, expone
// el extracto y las perillas de control para chaos testing, sin tocar un banco real.
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

// Los dos tipos de movimiento que acepta el simulador.
const (
	tipoDeposito = "deposito"
	tipoRetiro   = "retiro"
)

// ErrReferenciaEnConflicto reporta una referencia ya registrada con otro tipo o monto.
var ErrReferenciaEnConflicto = errors.New("simulador-custodia: la referencia ya existe con otros datos")

// Movimiento es una fila de la tabla propia del simulador de custodia.
type Movimiento struct {
	ID            int64
	Referencia    string
	Tipo          string
	MontoCentavos Centavos
	OcurridoEn    time.Time
	RegistradoEn  time.Time
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
	f, err := os.OpenFile(ruta, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // ruta viene de la variable de entorno SIMULADOR_CUSTODIA_DB, config local del proceso, no de entrada de red
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

// esquema es la tabla única de movimientos, con la referencia única que da la idempotencia.
const esquema = `
CREATE TABLE IF NOT EXISTS movimiento (
  id             INTEGER PRIMARY KEY,
  referencia     TEXT UNIQUE NOT NULL,
  tipo           TEXT NOT NULL CHECK (tipo IN ('deposito', 'retiro')),
  monto_centavos INTEGER NOT NULL CHECK (monto_centavos > 0),
  ocurrido_en    TEXT NOT NULL,
  registrado_en  TEXT NOT NULL
);`

// Registrar inserta el movimiento y, si la referencia ya existía, devuelve el existente con la marca de reintento.
func (a *Almacen) Registrar(ctx context.Context, m Movimiento, ahora time.Time) (Movimiento, bool, error) {
	registrado := ahora.UTC().Format(formatoInstante)
	res, err := a.db.ExecContext(ctx,
		`INSERT INTO movimiento (referencia, tipo, monto_centavos, ocurrido_en, registrado_en)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (referencia) DO NOTHING`,
		m.Referencia, m.Tipo, int64(m.MontoCentavos), m.OcurridoEn.UTC().Format(formatoInstante), registrado)
	if err != nil {
		return Movimiento{}, false, fmt.Errorf("insertar movimiento: %w", err)
	}
	filas, err := res.RowsAffected()
	if err != nil {
		return Movimiento{}, false, fmt.Errorf("filas del movimiento: %w", err)
	}
	if filas == 1 {
		id, err := res.LastInsertId()
		if err != nil {
			return Movimiento{}, false, fmt.Errorf("id del movimiento: %w", err)
		}
		m.ID, m.OcurridoEn, m.RegistradoEn = id, m.OcurridoEn.UTC(), ahora.UTC()
		return m, false, nil
	}
	return a.reintento(ctx, m)
}

// reintento compara la notificación con la fila ya registrada: misma referencia y datos, o conflicto.
func (a *Almacen) reintento(ctx context.Context, m Movimiento) (Movimiento, bool, error) {
	existente, err := a.PorReferencia(ctx, m.Referencia)
	if err != nil {
		return Movimiento{}, false, err
	}
	if existente.Tipo != m.Tipo || existente.MontoCentavos != m.MontoCentavos {
		return existente, false, ErrReferenciaEnConflicto
	}
	return existente, true, nil
}

// PorReferencia devuelve el movimiento de esa referencia, o error si no existe.
func (a *Almacen) PorReferencia(ctx context.Context, referencia string) (Movimiento, error) {
	rows, err := a.db.QueryContext(ctx, consultaMovimiento+` WHERE referencia = ?`, referencia)
	if err != nil {
		return Movimiento{}, fmt.Errorf("leer movimiento %q: %w", referencia, err)
	}
	defer func() { _ = rows.Close() }()
	movimientos, err := escanearMovimientos(rows)
	if err != nil {
		return Movimiento{}, err
	}
	if len(movimientos) == 0 {
		return Movimiento{}, fmt.Errorf("movimiento %q no encontrado", referencia)
	}
	return movimientos[0], nil
}

// Movimientos devuelve todos los movimientos, para inspección y para las pruebas.
func (a *Almacen) Movimientos(ctx context.Context) ([]Movimiento, error) {
	rows, err := a.db.QueryContext(ctx, consultaMovimiento+` ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("leer movimientos: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return escanearMovimientos(rows)
}

// SaldoHasta suma los depósitos y resta los retiros ocurridos hasta el corte inclusive.
func (a *Almacen) SaldoHasta(ctx context.Context, corte time.Time) (Centavos, error) {
	var saldo int64
	err := a.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(CASE tipo WHEN ? THEN monto_centavos ELSE -monto_centavos END), 0)
		   FROM movimiento WHERE ocurrido_en <= ?`,
		tipoDeposito, corte.UTC().Format(formatoInstante)).Scan(&saldo)
	if err != nil {
		return 0, fmt.Errorf("calcular el saldo al corte: %w", err)
	}
	return Centavos(saldo), nil
}

// consultaMovimiento es la lectura común de una fila de movimiento.
const consultaMovimiento = `
SELECT id, referencia, tipo, monto_centavos, ocurrido_en, registrado_en FROM movimiento`

// escanearMovimientos materializa las filas leídas con los instantes ya convertidos.
func escanearMovimientos(rows *sql.Rows) ([]Movimiento, error) {
	movimientos := make([]Movimiento, 0, 4)
	for rows.Next() {
		var (
			m                    Movimiento
			ocurrido, registrado string
		)
		if err := rows.Scan(&m.ID, &m.Referencia, &m.Tipo, &m.MontoCentavos, &ocurrido, &registrado); err != nil {
			return nil, fmt.Errorf("leer movimiento: %w", err)
		}
		var err error
		if m.OcurridoEn, err = parsearInstante(ocurrido); err != nil {
			return nil, err
		}
		if m.RegistradoEn, err = parsearInstante(registrado); err != nil {
			return nil, err
		}
		movimientos = append(movimientos, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recorrer movimientos: %w", err)
	}
	return movimientos, nil
}

// parsearInstante lee el formato fijo con el que se guardan las marcas de tiempo.
func parsearInstante(s string) (time.Time, error) {
	t, err := time.Parse(formatoInstante, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("instante %q: %w", s, err)
	}
	return t.UTC(), nil
}
