//go:build integration

package nucleo

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// directorioQueEspera avisa que entró y se queda hasta que la prueba lo libera.
type directorioQueEspera struct {
	entro  chan struct{}
	seguir chan struct{}
}

// Resolver señala la entrada y espera la liberación para simular un directorio lento.
func (d *directorioQueEspera) Resolver(context.Context, Llave) (ResolucionDirectorio, error) {
	close(d.entro)
	<-d.seguir
	return ResolucionDirectorio{Entidad: "Banco Vecino", Producto: "Cuenta de ahorros"}, nil
}

// HU-NF-10, criterio 4: la consulta al directorio ocurre antes de abrir la transacción, sin bloqueos.
func TestHU_NF_10_Criterio4_LaConsultaOcurreAntesDeAbrirLaTransaccion(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	control := abrirPoolDeControl(t)
	directorio := &directorioQueEspera{entro: make(chan struct{}), seguir: make(chan struct{})}
	servicio := NewServicio(pool, nil, nil, nil, nil, ConDirectorio(directorio))

	type resultado struct {
		res ResolucionDirectorio
		err error
	}
	listo := make(chan resultado, 1)
	go func() {
		res, err := servicio.ResolverLlave(context.Background(), TipoLlaveCelular, "3001234567")
		listo <- resultado{res: res, err: err}
	}()

	<-directorio.entro
	if abiertas, locks := contarTransaccionesYBloqueos(t, pool); abiertas != 0 || locks != 0 {
		t.Errorf("durante la espera del directorio hay %d transacciones abiertas y %d locks, quiero 0 y 0", abiertas, locks)
	}

	// Control positivo: la sonda sí detecta una transacción abierta en otra sesión.
	tx, err := control.Begin(context.Background())
	if err != nil {
		t.Fatalf("abrir la transacción de control: %v", err)
	}
	if _, err := tx.Exec(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("ejecutar en la transacción de control: %v", err)
	}
	if abiertas, _ := contarTransaccionesYBloqueos(t, pool); abiertas == 0 {
		t.Error("la sonda no detectó la transacción de control: la medición no sirve como evidencia")
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("revertir la transacción de control: %v", err)
	}

	close(directorio.seguir)
	obtenido := <-listo
	if obtenido.err != nil {
		t.Fatalf("ResolverLlave: %v", obtenido.err)
	}
	if obtenido.res.Entidad == "" || obtenido.res.Producto == "" {
		t.Errorf("resolución = %+v, quiero entidad y producto", obtenido.res)
	}
}

// contarTransaccionesYBloqueos cuenta sesiones de esta base con transacción abierta y locks de transacción.
func contarTransaccionesYBloqueos(t *testing.T, pool *pgxpool.Pool) (int, int) {
	t.Helper()

	ctx := context.Background()
	var abiertas int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_stat_activity
		WHERE datname = current_database() AND xact_start IS NOT NULL AND pid <> pg_backend_pid()`).Scan(&abiertas); err != nil {
		t.Fatalf("contar transacciones abiertas: %v", err)
	}
	var locks int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_locks
		WHERE locktype = 'transactionid'
		  AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&locks); err != nil {
		t.Fatalf("contar locks de transacción: %v", err)
	}
	return abiertas, locks
}

// abrirPoolDeControl abre un segundo pool con el mismo DSN para una sesión de control aparte.
func abrirPoolDeControl(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), os.Getenv("APP_DATABASE_URL"))
	if err != nil {
		t.Fatalf("abrir el pool de control: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
