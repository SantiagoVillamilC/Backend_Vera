//go:build integration

package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/correosim"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// correoDePrueba devuelve un destino único por corrida para no cruzar filas entre tests.
func correoDePrueba() string { return uuid.NewString() + "@centinela.test" }

// encolarAvisoDePrueba encola un aviso de correo confirmado en su propia transacción.
func encolarAvisoDePrueba(tb testing.TB, pool *pgxpool.Pool, tipo, destino string) {
	tb.Helper()

	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			return struct{}{}, EncolarAviso(ctx, q, Aviso{
				Tipo:      tipo,
				Destino:   DestinoCorreo,
				Correo:    destino,
				Plantilla: "marcador",
			})
		}); err != nil {
		tb.Fatalf("encolar aviso: %v", err)
	}
}

// rastreadorDeAviso es lo que el test lee de una fila del outbox.
type rastreadorDeAviso struct {
	despachadoEn pgtype.Timestamptz
	intentos     int32
	disponibleEn pgtype.Timestamptz
	ultimoError  pgtype.Text
	idMensaje    pgtype.Text
}

// leerAviso lee como rol_auth la última fila del correo y libera la conexión.
func leerAviso(tb testing.TB, pool *pgxpool.Pool, correo string) rastreadorDeAviso {
	tb.Helper()

	var fila rastreadorDeAviso
	_, err := enTransaccion(context.Background(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT despachado_en, intentos, disponible_en, ultimo_error, id_mensaje_proveedor
				   FROM auth.outbox
				  WHERE payload->>'correo' = $1
				  ORDER BY id DESC LIMIT 1`, correo).
				Scan(&fila.despachadoEn, &fila.intentos, &fila.disponibleEn, &fila.ultimoError, &fila.idMensaje)
		})
	if err != nil {
		tb.Fatalf("leer aviso: %v", err)
	}
	return fila
}

// contarAvisos cuenta como rol_auth las filas del correo y libera la conexión.
func contarAvisos(tb testing.TB, pool *pgxpool.Pool, correo string) int {
	tb.Helper()

	var total int
	_, err := enTransaccion(context.Background(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT count(*) FROM auth.outbox WHERE payload->>'correo' = $1`, correo).Scan(&total)
		})
	if err != nil {
		tb.Fatalf("contar avisos: %v", err)
	}
	return total
}

func TestAvisoEnTransaccionQueRevierteNoQueda(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	correo := correoDePrueba()

	tx, q := abrirTransaccionDePrueba(t, pool)
	if err := EncolarAviso(t.Context(), q, Aviso{
		Tipo:      TipoAvisoRecuperacionSolicitada,
		Destino:   DestinoCorreo,
		Correo:    correo,
		Plantilla: "marcador",
	}); err != nil {
		t.Fatalf("encolar aviso: %v", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("revertir: %v", err)
	}

	if total := contarAvisos(t, pool, correo); total != 0 {
		t.Errorf("avisos tras revertir = %d, quiero 0", total)
	}
}

func TestAvisoConfirmadoQueda(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	correo := correoDePrueba()

	encolarAvisoDePrueba(t, pool, TipoAvisoRecuperacionSolicitada, correo)

	if total := contarAvisos(t, pool, correo); total != 1 {
		t.Errorf("avisos confirmados = %d, quiero 1", total)
	}
}

func TestDespachadorEntregaPorElSimuladorDeCorreo(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	correo := correoDePrueba()
	encolarAvisoDePrueba(t, pool, TipoAvisoContrasenaCambiada, correo)

	simuladorCorreo := correosim.Nuevo()
	despachador := NuevoDespachadorDeAvisos(pool,
		NuevoNotificadorDeAvisos(simuladorCorreo, &canalOTPFalso{}),
		PoliticaReintento{Base: time.Second, Factor: 2, Tope: time.Minute})

	if err := despachador.DespacharAvisos(t.Context()); err != nil {
		t.Fatalf("despachar: %v", err)
	}

	aviso, ok := simuladorCorreo.UltimoAviso(correo)
	if !ok || aviso.Asunto == "" || aviso.Cuerpo == "" {
		t.Fatalf("el simulador de correo no guardó el aviso: ok=%v aviso=%+v", ok, aviso)
	}

	fila := leerAviso(t, pool, correo)
	if !fila.despachadoEn.Valid {
		t.Error("despachado_en quedó nulo")
	}
	if !fila.idMensaje.Valid || fila.idMensaje.String == "" {
		t.Error("id_mensaje_proveedor quedó vacío")
	}
}

func TestDespachadorConCanalQueFallaReintenta(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	correo := correoDePrueba()
	encolarAvisoDePrueba(t, pool, TipoAvisoCambioNumero, correo)

	canal := &canalCorreoFalso{err: fmt.Errorf("no se pudo entregar a %s", correo)}
	despachador := NuevoDespachadorDeAvisos(pool,
		NuevoNotificadorDeAvisos(canal, &canalOTPFalso{}),
		PoliticaReintento{Base: time.Hour, Factor: 2, Tope: 24 * time.Hour})

	inicio := time.Now()
	if err := despachador.DespacharAvisos(t.Context()); err == nil {
		t.Fatal("el barrido debía fallar")
	}

	fila := leerAviso(t, pool, correo)
	if fila.intentos != 1 {
		t.Errorf("intentos = %d, quiero 1", fila.intentos)
	}
	if !fila.disponibleEn.Valid || !fila.disponibleEn.Time.After(inicio.Add(30*time.Minute)) {
		t.Errorf("disponible_en = %v, quiero después de %v", fila.disponibleEn.Time, inicio.Add(30*time.Minute))
	}
	if !fila.ultimoError.Valid || fila.ultimoError.String == "" {
		t.Error("ultimo_error quedó vacío")
	}
	if strings.Contains(fila.ultimoError.String, correo) {
		t.Errorf("ultimo_error filtra el destino: %q", fila.ultimoError.String)
	}
	if fila.despachadoEn.Valid {
		t.Error("despachado_en no debe quedar puesto si falló")
	}
}

// canalCorreoConcurrente cuenta entregas por correo y se demora un poco para forzar el solapamiento.
type canalCorreoConcurrente struct {
	mu       sync.Mutex
	entregas map[string]int
}

func nuevoCanalCorreoConcurrente() *canalCorreoConcurrente {
	return &canalCorreoConcurrente{entregas: map[string]int{}}
}

func (c *canalCorreoConcurrente) Enviar(_ context.Context, correo, _, _ string) (string, error) {
	time.Sleep(5 * time.Millisecond)
	c.mu.Lock()
	c.entregas[correo]++
	c.mu.Unlock()
	return "msg:" + correo, nil
}

func (c *canalCorreoConcurrente) veces(correo string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entregas[correo]
}

func TestDosDespachadoresNoEntreganLaMismaFilaDosVeces(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	const total = 10
	correos := make([]string, 0, total)
	for range total {
		correo := correoDePrueba()
		correos = append(correos, correo)
		encolarAvisoDePrueba(t, pool, TipoAvisoRecuperacionSolicitada, correo)
	}

	canal := nuevoCanalCorreoConcurrente()
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			despachador := NuevoDespachadorDeAvisos(pool,
				NuevoNotificadorDeAvisos(canal, &canalOTPFalso{}),
				PoliticaReintento{Base: time.Second, Factor: 2, Tope: time.Minute})
			if err := despachador.DespacharAvisos(context.Background()); err != nil {
				t.Errorf("despachar concurrente: %v", err)
			}
		})
	}
	wg.Wait()

	for _, correo := range correos {
		if veces := canal.veces(correo); veces != 1 {
			t.Errorf("el correo %s se entregó %d veces, quiero 1", correo, veces)
		}
	}
}

func TestRolAuthNoPuedeBorrarElOutbox(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	tx, _ := abrirTransaccionDePrueba(t, pool)

	_, err := tx.Exec(t.Context(), "DELETE FROM auth.outbox")
	if err == nil {
		t.Fatal("DELETE como rol_auth no falló")
	}
	t.Logf("error literal de Postgres: %v", err)

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("el error de DELETE no es *pgconn.PgError: %v", err)
	}
	if pgErr.Code != pgerrcode.InsufficientPrivilege {
		t.Errorf("SQLSTATE = %q, quiero %q", pgErr.Code, pgerrcode.InsufficientPrivilege)
	}
	if !strings.Contains(pgErr.Message, "permission denied for table outbox") {
		t.Errorf("mensaje = %q, quiero que contenga %q", pgErr.Message, "permission denied for table outbox")
	}
}
