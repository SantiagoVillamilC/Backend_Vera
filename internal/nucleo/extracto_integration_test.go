//go:build integration

package nucleo_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/custodiosim"
	"github.com/jackc/pgx/v5/pgxpool"
)

// abrirPoolDeIntegracion conecta con el Postgres real de APP_DATABASE_URL, con una sola conexión.
func abrirPoolDeIntegracion(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		t.Skip("APP_DATABASE_URL no definida: la prueba de integración necesita un Postgres real")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parsear DSN: %v", err)
	}
	cfg.MaxConns = 1
	cfg.MinIdleConns = 0

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("abrir pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

func TestRegistrarExtractoCustodioLeeAntesDeLaTransaccionYGuardaLaFila(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	ctx := t.Context()
	corte := time.Now().UTC().Truncate(time.Microsecond)

	llego := make(chan struct{})
	soltar := make(chan struct{})
	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(llego)
		<-soltar
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"corte_en":"`+corte.Format(time.RFC3339Nano)+`","saldo_centavos":"700","origen":"simulador-custodia"}`)
	}))
	defer servidor.Close()

	servicio := nucleo.NewServicio(pool, nucleo.KYCNoDisponible{}, nucleo.RielNoDisponible{},
		custodiosim.NuevoLectorExtracto(servidor.URL+"/extracto", nil), nil)

	hecho := make(chan error, 1)
	go func() {
		_, err := servicio.RegistrarExtractoCustodio(ctx, corte)
		hecho <- err
	}()

	select {
	case <-llego:
	case <-time.After(5 * time.Second):
		t.Fatal("el lector no llegó al servidor de test")
	}
	if abiertas := pool.Stat().AcquiredConns(); abiertas != 0 {
		t.Errorf("conexiones del núcleo tomadas mientras el servidor está congelado = %d, quiero 0", abiertas)
	}
	close(soltar)
	if err := <-hecho; err != nil {
		t.Fatalf("RegistrarExtractoCustodio: %v", err)
	}

	obtenido, saldo, origen := leerExtracto(t, pool, corte)
	if !obtenido.Equal(corte) {
		t.Errorf("corte_en = %v, quiero %v", obtenido, corte)
	}
	if saldo != 700 {
		t.Errorf("saldo = %d, quiero 700", saldo)
	}
	if origen != "simulador-custodia" {
		t.Errorf("origen = %q, quiero %q", origen, "simulador-custodia")
	}
}

func TestRegistrarExtractoCustodioSinRespuestaNoEscribeFila(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	ctx := t.Context()
	antes := contarExtractos(t, pool)

	servidor := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := servidor.URL + "/extracto"
	servidor.Close()

	servicio := nucleo.NewServicio(pool, nucleo.KYCNoDisponible{}, nucleo.RielNoDisponible{},
		custodiosim.NuevoLectorExtracto(url, nil), nil)

	if _, err := servicio.RegistrarExtractoCustodio(ctx, time.Now()); err == nil {
		t.Fatal("RegistrarExtractoCustodio sin servidor = nil, quiero error para que la corrida siguiente reintente")
	}
	if despues := contarExtractos(t, pool); despues != antes {
		t.Errorf("filas en extracto_custodio = %d, quiero %d (sin extracto no se escribe nada)", despues, antes)
	}
}

// leerExtracto lee la fila escrita por el núcleo bajando a rol_nucleo dentro de una transacción.
func leerExtracto(t *testing.T, pool *pgxpool.Pool, corte time.Time) (time.Time, int64, string) {
	t.Helper()

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("abrir transacción de lectura: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}

	var (
		obtenido time.Time
		saldo    int64
		origen   string
	)
	if err := tx.QueryRow(t.Context(),
		"SELECT corte_en, saldo, origen FROM nucleo.extracto_custodio WHERE corte_en = $1", corte).
		Scan(&obtenido, &saldo, &origen); err != nil {
		t.Fatalf("leer la fila del extracto: %v", err)
	}
	return obtenido, saldo, origen
}

// contarExtractos cuenta las filas de extracto_custodio bajando a rol_nucleo dentro de una transacción.
func contarExtractos(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()

	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("abrir transacción de conteo: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}

	var filas int64
	if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM nucleo.extracto_custodio").Scan(&filas); err != nil {
		t.Fatalf("contar extractos: %v", err)
	}
	return filas
}
