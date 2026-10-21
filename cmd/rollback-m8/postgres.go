package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/SantiagoVillamilC/Backend_Centinela/bd"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/database"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// VerificarDSN exige loopback, el puerto desechable y un prefijo de base de medición.
func VerificarDSN(dsn string, permitirRemoto bool) error {
	return database.DSNLocalDeMedicion(dsn, "55437", "vera_", permitirRemoto)
}

// postgres implementa Catalogos y Migrador sobre la base desechable.
type postgres struct {
	db *sql.DB
}

func abrirPostgres(ctx context.Context, dsn string) (*postgres, error) {
	if err := VerificarDSN(dsn, false); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("rollback-m8: abrir conexión: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("rollback-m8: comprobar la conexión: %w", err)
	}
	goose.SetBaseFS(bd.Migraciones)
	if err := goose.SetDialect("postgres"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("rollback-m8: dialecto postgres: %w", err)
	}
	return &postgres{db: db}, nil
}

func (p *postgres) Cerrar() { _ = p.db.Close() }

// Version devuelve la última migración aplicada del núcleo.
func (p *postgres) Version(ctx context.Context) (int64, error) {
	var version int64
	if err := p.db.QueryRowContext(ctx,
		`SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil {
		return 0, fmt.Errorf("rollback-m8: leer la versión aplicada: %w", err)
	}
	return version, nil
}

// EsquemaExiste reporta si el esquema sigue en pie.
func (p *postgres) EsquemaExiste(ctx context.Context, nombre string) (bool, error) {
	var existe bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, nombre).Scan(&existe); err != nil {
		return false, fmt.Errorf("rollback-m8: comprobar el esquema %q: %w", nombre, err)
	}
	return existe, nil
}

// RolExiste reporta si el rol sigue en pie.
func (p *postgres) RolExiste(ctx context.Context, nombre string) (bool, error) {
	var existe bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, nombre).Scan(&existe); err != nil {
		return false, fmt.Errorf("rollback-m8: comprobar el rol %q: %w", nombre, err)
	}
	return existe, nil
}

// RolEscribeElLedger comprueba el privilegio sin intentar una escritura real.
func (p *postgres) RolEscribeElLedger(ctx context.Context) (bool, error) {
	for _, tabla := range []string{"cuenta", "journal_entry", "posting"} {
		for _, privilegio := range []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE"} {
			var puede bool
			consulta := `SELECT CASE WHEN EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'rol_inteligencia')
				THEN has_table_privilege('rol_inteligencia', $1, $2) ELSE false END`
			if err := p.db.QueryRowContext(ctx, consulta, "nucleo."+tabla, privilegio).Scan(&puede); err != nil {
				return false, fmt.Errorf("rollback-m8: comprobar el privilegio %s sobre %s: %w", privilegio, tabla, err)
			}
			if puede {
				return true, nil
			}
		}
	}
	return false, nil
}

// TrabajosEnCurso cuenta los trabajos de inteligencia corriendo. Los periódicos dejan siempre una fila
// programada para la próxima corrida, así que solo se mira el estado en ejecución.
func (p *postgres) TrabajosEnCurso(ctx context.Context) (int, error) {
	var enCurso int
	err := p.db.QueryRowContext(ctx, `
SELECT count(*) FROM river_job
 WHERE state = 'running' AND kind IN ('enriquecer_movimientos', 'emitir_avisos')`).Scan(&enCurso)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UndefinedTable {
		// Sin la tabla de River todavía no hay trabajos que esperar.
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("rollback-m8: contar los trabajos en curso: %w", err)
	}
	return enCurso, nil
}

// TablasPresentes devuelve las tablas de inteligencia que siguen existiendo.
func (p *postgres) TablasPresentes(ctx context.Context) ([]string, error) {
	filas, err := p.db.QueryContext(ctx, `
SELECT table_name FROM information_schema.tables
 WHERE table_schema = $1 AND table_name = ANY($2)
 ORDER BY table_name`, EsquemaDeInteligencia, TablasDeInteligencia)
	if err != nil {
		return nil, fmt.Errorf("rollback-m8: listar las tablas de inteligencia: %w", err)
	}
	defer func() { _ = filas.Close() }()
	var presentes []string
	for filas.Next() {
		var nombre string
		if err := filas.Scan(&nombre); err != nil {
			return nil, fmt.Errorf("rollback-m8: leer una tabla: %w", err)
		}
		presentes = append(presentes, nombre)
	}
	if err := filas.Err(); err != nil {
		return nil, fmt.Errorf("rollback-m8: recorrer las tablas: %w", err)
	}
	return presentes, nil
}

// Huella toma conteos y una suma de hashes por fila de las tres tablas del ledger. Nunca escribe.
func (p *postgres) Huella(ctx context.Context) (HuellaDelLedger, error) {
	var huella HuellaDelLedger
	if err := p.db.QueryRowContext(ctx, `
SELECT (SELECT count(*) FROM nucleo.cuenta),
       (SELECT count(*) FROM nucleo.journal_entry),
       (SELECT count(*) FROM nucleo.posting),
       md5(
         COALESCE((SELECT sum(hashtextextended(t::text, 0))::text FROM nucleo.cuenta t), '0') || '|' ||
         COALESCE((SELECT sum(hashtextextended(t::text, 0))::text FROM nucleo.journal_entry t), '0') || '|' ||
         COALESCE((SELECT sum(hashtextextended(t::text, 0))::text FROM nucleo.posting t), '0'))`).
		Scan(&huella.Cuentas, &huella.Asientos, &huella.Lineas, &huella.Resumen); err != nil {
		return HuellaDelLedger{}, fmt.Errorf("rollback-m8: tomar la huella del ledger: %w", err)
	}
	return huella, nil
}

// BajarA revierte las migraciones hasta la versión pedida usando goose, sin ningún DROP a mano.
func (p *postgres) BajarA(ctx context.Context, version int64) error {
	if err := goose.RunContext(ctx, "down-to", p.db, "migraciones", fmt.Sprintf("%d", version)); err != nil {
		return fmt.Errorf("bajar a la versión %d: %w", version, err)
	}
	return nil
}

var (
	_ Catalogos = (*postgres)(nil)
	_ Migrador  = (*postgres)(nil)
)
