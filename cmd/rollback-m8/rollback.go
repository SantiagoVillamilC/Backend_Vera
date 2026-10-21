// Comando rollback-m8 devuelve el contexto de inteligencia a su estado previo a M2 sin tocar el
// ledger: apaga la superficie, baja las migraciones 0021 a 0026 y comprueba que nucleo no cambió.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("rollback-m8: %v", err)
	}
}

func run() error {
	ejecutar := flag.Bool("ejecutar", false, "aplica el rollback, sin esta bandera solo imprime el plan")
	confirmar := flag.String("confirmar", "", "confirmación literal: borrar-inteligencia-m8")
	conservar := flag.Bool("conservar-copia", false, "no baja las migraciones y solo informa")
	permitirRemoto := flag.Bool("permitir-remoto", false, "acepta un host que no sea loopback, solo con una base desechable")
	version := flag.Int64("version-esperada", VersionDeM8, "versión de goose que debe estar aplicada")
	espera := flag.Duration("timeout", 10*time.Minute, "tiempo máximo del derribo")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	cfg := Configuracion{
		DSN:             dsn,
		Ejecutar:        *ejecutar,
		Confirmacion:    *confirmar,
		ConservarCopia:  *conservar,
		PermitirRemoto:  *permitirRemoto,
		VersionEsperada: *version,
	}
	if err := cfg.Validar(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *espera)
	defer cancel()

	postgres, err := abrirPostgres(ctx, dsn)
	if err != nil {
		return err
	}
	defer postgres.Cerrar()

	informe, err := Ejecutar(ctx, cfg, postgres, postgres)
	if err != nil {
		return err
	}
	salida, err := json.MarshalIndent(informe, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar el informe: %w", err)
	}
	fmt.Println(string(salida))
	return nil
}

// VersionDeM8 es la última migración que introdujo M8 y la única desde la que se permite bajar.
const VersionDeM8 int64 = 26

// VersionAnteriorAInteligencia es la versión a la que vuelve el esquema: la última antes de 0021.
const VersionAnteriorAInteligencia int64 = 20

// ConfirmacionDeRollback es la cadena que hay que escribir para ejecutar el derribo.
const ConfirmacionDeRollback = "borrar-inteligencia-m8"

// EsquemaDeInteligencia es el único esquema que el rollback puede tocar.
const EsquemaDeInteligencia = "inteligencia"

// TablasDeInteligencia son las tablas que crean las migraciones 0022 a 0026.
var TablasDeInteligencia = []string{
	"categoria", "movimiento_enriquecido", "memoria_correccion", "cursor_enriquecimiento",
	"aviso", "aviso_entrega",
}

// ErrHuellaCambiada reporta que el ledger cambió durante el rollback. Es el fallo más grave.
var ErrHuellaCambiada = errors.New("rollback-m8: el ledger cambió durante el rollback")

// ErrVersionInesperada reporta que la base no está en el estado que este comando sabe deshacer.
var ErrVersionInesperada = errors.New("rollback-m8: la versión aplicada no es la esperada")

// ErrTrabajosEnCurso reporta un lote o un emisor de avisos corriendo.
var ErrTrabajosEnCurso = errors.New("rollback-m8: hay trabajos de inteligencia en curso")

// ErrRolEscribeElLedger reporta un rol de inteligencia con escritura sobre nucleo.
var ErrRolEscribeElLedger = errors.New("rollback-m8: rol_inteligencia puede escribir el ledger")

// HuellaDelLedger resume el ledger sin traerlo a memoria: conteos y una suma de hashes por fila, que
// detecta altas, bajas y cambios de cualquier columna.
type HuellaDelLedger struct {
	Cuentas  int64  `json:"cuentas"`
	Asientos int64  `json:"asientos"`
	Lineas   int64  `json:"lineas"`
	Resumen  string `json:"resumen"`
}

// Configuracion es lo que el operador decidió.
type Configuracion struct {
	DSN             string
	Ejecutar        bool
	Confirmacion    string
	ConservarCopia  bool
	PermitirRemoto  bool
	VersionEsperada int64
}

// Validar exige que la ejecución venga pedida y confirmada por escrito.
func (c Configuracion) Validar() error {
	if c.DSN == "" {
		return errors.New("rollback-m8: DATABASE_URL es obligatoria")
	}
	if !c.Ejecutar {
		return nil
	}
	if c.Confirmacion != ConfirmacionDeRollback {
		return fmt.Errorf("rollback-m8: para ejecutar escribe --confirmar=%s", ConfirmacionDeRollback)
	}
	return nil
}

// Catalogos es lo que el comando necesita saber de la base antes y después de tocar nada.
type Catalogos interface {
	Version(ctx context.Context) (int64, error)
	EsquemaExiste(ctx context.Context, nombre string) (bool, error)
	RolExiste(ctx context.Context, nombre string) (bool, error)
	RolEscribeElLedger(ctx context.Context) (bool, error)
	TrabajosEnCurso(ctx context.Context) (int, error)
	TablasPresentes(ctx context.Context) ([]string, error)
	Huella(ctx context.Context) (HuellaDelLedger, error)
}

// Migrador baja las migraciones. Se separa para poder probar el comando sin Postgres.
type Migrador interface {
	BajarA(ctx context.Context, version int64) error
}

// Informe es el resultado que el operador archiva.
type Informe struct {
	VersionAntes    int64           `json:"version_antes"`
	VersionDespues  int64           `json:"version_despues"`
	HuellaAntes     HuellaDelLedger `json:"huella_antes"`
	HuellaDespues   HuellaDelLedger `json:"huella_despues"`
	CopiaConservada bool            `json:"copia_conservada"`
	EsquemaPresente bool            `json:"esquema_presente"`
	RolPresente     bool            `json:"rol_presente"`
	TablasPresentes []string        `json:"tablas_presentes"`
	Ejecutado       bool            `json:"ejecutado"`
	Plan            []string        `json:"plan"`
}

// PlanDeRollback es la secuencia que se imprime y se ejecuta en el mismo orden.
func PlanDeRollback(conservarCopia bool) []string {
	pasos := []string{
		"verificar que la versión aplicada es la 26",
		"verificar que no hay trabajos de enriquecimiento ni de avisos corriendo",
		"verificar que rol_inteligencia no puede escribir en nucleo",
		"tomar la huella del ledger antes",
	}
	if conservarCopia {
		pasos = append(pasos, "conservar la copia y no bajar migraciones")
	} else {
		pasos = append(pasos, "bajar goose a la versión 20, que revierte 0026 a 0021",
			"verificar que las seis tablas de inteligencia y el rol desaparecieron")
	}
	return append(pasos, "volver a tomar la huella del ledger y compararla")
}

// Ejecutar aplica el rollback con las comprobaciones en orden y falla cerrado en cualquiera.
func Ejecutar(ctx context.Context, cfg Configuracion, catalogos Catalogos, migrador Migrador) (Informe, error) {
	if err := cfg.Validar(); err != nil {
		return Informe{}, err
	}
	if err := VerificarDSN(cfg.DSN, cfg.PermitirRemoto); err != nil {
		return Informe{}, err
	}
	informe := Informe{Plan: PlanDeRollback(cfg.ConservarCopia)}

	version, err := catalogos.Version(ctx)
	if err != nil {
		return Informe{}, err
	}
	informe.VersionAntes = version
	if version != cfg.VersionEsperada {
		return informe, fmt.Errorf("%w: %d, quiero %d", ErrVersionInesperada, version, cfg.VersionEsperada)
	}

	enCurso, err := catalogos.TrabajosEnCurso(ctx)
	if err != nil {
		return informe, err
	}
	if enCurso != 0 {
		return informe, fmt.Errorf("%w: %d", ErrTrabajosEnCurso, enCurso)
	}

	escribe, err := catalogos.RolEscribeElLedger(ctx)
	if err != nil {
		return informe, err
	}
	if escribe {
		return informe, ErrRolEscribeElLedger
	}

	antes, err := catalogos.Huella(ctx)
	if err != nil {
		return informe, err
	}
	informe.HuellaAntes = antes

	if !cfg.Ejecutar {
		return informe, nil
	}
	informe.Ejecutado = true
	informe.CopiaConservada = cfg.ConservarCopia

	if !cfg.ConservarCopia {
		if migrador == nil {
			return informe, errors.New("rollback-m8: falta el migrador")
		}
		if err := migrador.BajarA(ctx, VersionAnteriorAInteligencia); err != nil {
			return informe, fmt.Errorf("rollback-m8: bajar migraciones: %w", err)
		}
	}

	despues, err := catalogos.Version(ctx)
	if err != nil {
		return informe, err
	}
	informe.VersionDespues = despues
	informe.EsquemaPresente, err = catalogos.EsquemaExiste(ctx, EsquemaDeInteligencia)
	if err != nil {
		return informe, err
	}
	informe.RolPresente, err = catalogos.RolExiste(ctx, "rol_inteligencia")
	if err != nil {
		return informe, err
	}
	informe.TablasPresentes, err = catalogos.TablasPresentes(ctx)
	if err != nil {
		return informe, err
	}

	huellaDespues, err := catalogos.Huella(ctx)
	if err != nil {
		return informe, err
	}
	informe.HuellaDespues = huellaDespues
	if huellaDespues != antes {
		return informe, ErrHuellaCambiada
	}

	if !cfg.ConservarCopia {
		if despues != VersionAnteriorAInteligencia {
			return informe, fmt.Errorf("%w: quedó en %d, quiero %d", ErrVersionInesperada,
				despues, VersionAnteriorAInteligencia)
		}
		if informe.EsquemaPresente || informe.RolPresente || len(informe.TablasPresentes) != 0 {
			return informe, fmt.Errorf("rollback-m8: quedaron restos: esquema %v, rol %v, tablas %v",
				informe.EsquemaPresente, informe.RolPresente, informe.TablasPresentes)
		}
	}
	return informe, nil
}
