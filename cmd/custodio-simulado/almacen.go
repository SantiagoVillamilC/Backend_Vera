// Package main es el custodio simulado: expone el checkout PSE de un depósito y notifica
// la confirmación al núcleo financiero, sin tocar un banco real.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

// formatoInstante fija el ancho del instante para que el orden lexicográfico sea cronológico.
const formatoInstante = "2006-01-02T15:04:05.000000000Z07:00"

// Las dos patas del evento, una por destino.
const (
	pataNucleo   = "leg_nucleo"
	pataCustodio = "leg_custodio"
)

// El estado de cada pata: pendiente hasta entregarse, o terminal.
const (
	pataPendiente = "pendiente"
	pataEntregada = "entregada"
	pataFallida   = "fallida"
	pataNoAplica  = "no_aplica"
)

// Los dos desenlaces que el usuario puede pulsar en la página de PSE.
const (
	estadoAprobado  = "aprobado"
	estadoRechazado = "rechazado"
)

// Envio es el evento que dispara el usuario y que las dos patas entregan.
type Envio struct {
	Referencia string
	Monto      int64
	Moneda     string
	Estado     string
	CUS        string
	OcurridoEn time.Time
}

// Pata es una entrega de un envío a un destino, con su propio reintento.
type Pata struct {
	ID           int64
	Nombre       string
	Estado       string
	Intentos     int
	DisponibleEn time.Time
	EntregadoEn  time.Time
	Entregado    bool
	Envio        Envio
}

// Almacen es el SQLite propio del despachador, separado de la base del núcleo.
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
	f, err := os.OpenFile(ruta, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // ruta viene de la variable de entorno CUSTODIO_DB, config local del proceso, no de entrada de red
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(ruta, 0o600) //nolint:gosec // ruta viene de CUSTODIO_DB, ver AbrirAlmacen
}

// restringirArchivo deja en 0600 la base y los sidecars que ya existan, sin fallar si el sistema no lo permite.
func restringirArchivo(ruta string) {
	for _, sufijo := range []string{"", "-journal", "-wal", "-shm"} {
		_ = os.Chmod(ruta+sufijo, 0o600) //nolint:gosec // ruta viene de CUSTODIO_DB, ver AbrirAlmacen
	}
}

func (a *Almacen) Cerrar() error { return a.db.Close() }

// esquema es la tabla del evento y la de sus dos patas, cada una con su reintento.
const esquema = `
CREATE TABLE IF NOT EXISTS envio (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  referencia  TEXT NOT NULL,
  monto       INTEGER NOT NULL CHECK (monto > 0),
  moneda      TEXT NOT NULL,
  estado      TEXT NOT NULL,
  cus         TEXT,
  ocurrido_en TEXT NOT NULL,
  creado_en   TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS pata (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  envio_id      INTEGER NOT NULL REFERENCES envio(id),
  nombre        TEXT NOT NULL,
  estado        TEXT NOT NULL,
  intentos      INTEGER NOT NULL DEFAULT 0,
  disponible_en TEXT NOT NULL,
  entregado_en  TEXT,
  UNIQUE (envio_id, nombre)
);`

// Registrar guarda el envío y sus dos patas; la de custodia no aplica a un rechazo.
func (a *Almacen) Registrar(ctx context.Context, envio Envio, ahora time.Time) (int64, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO envio (referencia, monto, moneda, estado, cus, ocurrido_en, creado_en)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		envio.Referencia, envio.Monto, envio.Moneda, envio.Estado, nuloSiVacio(envio.CUS),
		envio.OcurridoEn.UTC().Format(formatoInstante), ahora.UTC().Format(formatoInstante))
	if err != nil {
		return 0, fmt.Errorf("insertar envío: %w", err)
	}
	envioID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("id del envío: %w", err)
	}

	custodio := pataPendiente
	if envio.Estado != estadoAprobado {
		custodio = pataNoAplica
	}
	for _, p := range []struct{ nombre, estado string }{
		{pataNucleo, pataPendiente},
		{pataCustodio, custodio},
	} {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO pata (envio_id, nombre, estado, intentos, disponible_en) VALUES (?, ?, ?, 0, ?)`,
			envioID, p.nombre, p.estado, ahora.UTC().Format(formatoInstante)); err != nil {
			return 0, fmt.Errorf("insertar pata %s: %w", p.nombre, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return envioID, nil
}

// Pendientes devuelve las patas vencidas de un destino, sin mirar el estado de la otra.
func (a *Almacen) Pendientes(ctx context.Context, nombre string, ahora time.Time) ([]Pata, error) {
	rows, err := a.db.QueryContext(ctx, consultaPatas+`
		  AND p.nombre = ? AND p.estado = ? AND p.disponible_en <= ?
		  ORDER BY p.id`,
		nombre, pataPendiente, ahora.UTC().Format(formatoInstante))
	if err != nil {
		return nil, fmt.Errorf("leer patas pendientes de %s: %w", nombre, err)
	}
	defer func() { _ = rows.Close() }()
	return escanearPatas(rows)
}

// PatasDe devuelve las patas de un envío, para inspección y para las pruebas.
func (a *Almacen) PatasDe(ctx context.Context, envioID int64) ([]Pata, error) {
	rows, err := a.db.QueryContext(ctx, consultaPatas+` AND p.envio_id = ? ORDER BY p.id`, envioID)
	if err != nil {
		return nil, fmt.Errorf("leer patas del envío %d: %w", envioID, err)
	}
	defer func() { _ = rows.Close() }()
	return escanearPatas(rows)
}

// consultaPatas es la lectura común de una pata con los datos de su envío.
const consultaPatas = `
SELECT p.id, p.nombre, p.estado, p.intentos, p.disponible_en, p.entregado_en,
       e.referencia, e.monto, e.moneda, e.estado, e.cus, e.ocurrido_en
  FROM pata p JOIN envio e ON e.id = p.envio_id
 WHERE 1 = 1`

// MarcarEntregada cierra la pata una sola vez, aunque la pasada se repita.
func (a *Almacen) MarcarEntregada(ctx context.Context, id int64, ahora time.Time) error {
	_, err := a.db.ExecContext(ctx,
		`UPDATE pata SET estado = ?, entregado_en = ? WHERE id = ? AND estado = ?`,
		pataEntregada, ahora.UTC().Format(formatoInstante), id, pataPendiente)
	if err != nil {
		return fmt.Errorf("marcar pata %d entregada: %w", id, err)
	}
	return nil
}

// Reprogramar suma un intento y empuja la próxima entrega.
func (a *Almacen) Reprogramar(ctx context.Context, id int64, intentos int, disponibleEn time.Time) error {
	_, err := a.db.ExecContext(ctx,
		`UPDATE pata SET intentos = ?, disponible_en = ? WHERE id = ? AND estado = ?`,
		intentos, disponibleEn.UTC().Format(formatoInstante), id, pataPendiente)
	if err != nil {
		return fmt.Errorf("reprogramar pata %d: %w", id, err)
	}
	return nil
}

// MarcarFallida deja la pata en su estado terminal, sin más reintentos.
func (a *Almacen) MarcarFallida(ctx context.Context, id int64, intentos int) error {
	_, err := a.db.ExecContext(ctx,
		`UPDATE pata SET estado = ?, intentos = ? WHERE id = ? AND estado = ?`,
		pataFallida, intentos, id, pataPendiente)
	if err != nil {
		return fmt.Errorf("marcar pata %d fallida: %w", id, err)
	}
	return nil
}

// escanearPatas materializa las filas leídas con el instante ya convertido.
func escanearPatas(rows *sql.Rows) ([]Pata, error) {
	patas := make([]Pata, 0, 2)
	for rows.Next() {
		var (
			p          Pata
			disponible string
			ocurrido   string
			entregado  sql.NullString
			cus        sql.NullString
		)
		if err := rows.Scan(&p.ID, &p.Nombre, &p.Estado, &p.Intentos, &disponible, &entregado,
			&p.Envio.Referencia, &p.Envio.Monto, &p.Envio.Moneda, &p.Envio.Estado, &cus, &ocurrido); err != nil {
			return nil, fmt.Errorf("leer pata: %w", err)
		}
		var err error
		if p.DisponibleEn, err = parsearInstante(disponible); err != nil {
			return nil, err
		}
		if p.Envio.OcurridoEn, err = parsearInstante(ocurrido); err != nil {
			return nil, err
		}
		if entregado.Valid {
			if p.EntregadoEn, err = parsearInstante(entregado.String); err != nil {
				return nil, err
			}
			p.Entregado = true
		}
		p.Envio.CUS = cus.String
		patas = append(patas, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recorrer patas: %w", err)
	}
	return patas, nil
}

// parsearInstante lee el formato fijo con el que se guardan las marcas de tiempo.
func parsearInstante(s string) (time.Time, error) {
	t, err := time.Parse(formatoInstante, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("instante %q: %w", s, err)
	}
	return t.UTC(), nil
}

// nuloSiVacio deja NULL la columna opcional en vez de guardar la cadena vacía.
func nuloSiVacio(s string) any {
	if s == "" {
		return nil
	}
	return s
}
