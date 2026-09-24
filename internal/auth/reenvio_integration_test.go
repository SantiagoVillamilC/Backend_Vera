//go:build integration

package auth_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// pepperDeReenvio es el OTP_PEPPER con el que el test externo construye el gestor.
var pepperDeReenvio = bytes.Repeat([]byte{0x22}, 32)

// canalFalso registra el último celular y el último código entregados.
type canalFalso struct {
	ultimoCelular string
	ultimoTexto   string
	err           error
}

// Enviar implementa auth.CanalOTP sin red.
func (c *canalFalso) Enviar(_ context.Context, celular, codigo string) (string, error) {
	if c.err != nil {
		return "", c.err
	}
	c.ultimoCelular, c.ultimoTexto = celular, codigo
	return "sim-1", nil
}

// abrirPoolExterno abre el pool de rol_api contra APP_DATABASE_URL.
func abrirPoolExterno(tb testing.TB) *pgxpool.Pool {
	tb.Helper()

	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		tb.Fatalf("APP_DATABASE_URL es obligatoria bajo el tag integration")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parsear DSN: %v", err)
	}
	cfg.MaxConns = 1
	cfg.MinIdleConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		tb.Fatalf("abrir pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	return pool
}

// porRolAuth ejecuta fn dentro de una transacción con SET LOCAL ROLE rol_auth.
func porRolAuth(tb testing.TB, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) {
	tb.Helper()

	tx, err := pool.Begin(tb.Context())
	if err != nil {
		tb.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(tb.Context(), "SET LOCAL ROLE rol_auth"); err != nil {
		tb.Fatalf("SET LOCAL ROLE rol_auth: %v", err)
	}
	if err := fn(tb.Context(), tx); err != nil {
		tb.Fatalf("consulta como rol_auth: %v", err)
	}
	if err := tx.Commit(tb.Context()); err != nil {
		tb.Fatalf("commit: %v", err)
	}
}

// sembrarCredencialExterna inserta una parte y su credencial activa con celular verificado.
func sembrarCredencialExterna(tb testing.TB, pool *pgxpool.Pool) (parteID, credencialID uuid.UUID, correo, celular string) {
	tb.Helper()

	parteID, credencialID = uuid.New(), uuid.New()
	correo = "a2-06-3-" + uuid.NewString() + "@centinela.test"
	celular = "+57300" + strings.ReplaceAll(uuid.NewString(), "-", "")[:7]
	porRolAuth(tb, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO auth.partes (id, tipo) VALUES ($1, 'persona')", pgtype.UUID{Bytes: parteID, Valid: true}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO auth.credenciales (id, parte_id, correo, celular, valor_derivado, estado, celular_verificado_en)
			 VALUES ($1, $2, $3, $4, '$argon2id$prueba', 'activo', now())`,
			pgtype.UUID{Bytes: credencialID, Valid: true}, pgtype.UUID{Bytes: parteID, Valid: true}, correo, celular)
		return err
	})
	return parteID, credencialID, correo, celular
}

// leerReenvioExterno lee intentos, reenvíos y HMAC del desafío como rol_auth.
func leerReenvioExterno(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) (intentos, reenvios int, hmac []byte) {
	tb.Helper()

	porRolAuth(tb, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT intentos, reenvios, codigo_hmac FROM auth.desafios WHERE id = $1`,
			pgtype.UUID{Bytes: id, Valid: true}).Scan(&intentos, &reenvios, &hmac)
	})
	return intentos, reenvios, hmac
}

// envejecerUltimoEnvio corre el último envío fuera de la ventana de enfriamiento.
func envejecerUltimoEnvio(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) {
	tb.Helper()

	porRolAuth(tb, pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE auth.desafios SET ultimo_envio_en = now() - interval '61 seconds' WHERE id = $1`,
			pgtype.UUID{Bytes: id, Valid: true})
		return err
	})
}

// contarDesafiosExterno cuenta los desafíos de una parte como rol_auth.
func contarDesafiosExterno(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) int {
	tb.Helper()

	total := 0
	porRolAuth(tb, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM auth.desafios WHERE parte_id = $1`,
			pgtype.UUID{Bytes: parteID, Valid: true}).Scan(&total)
	})
	return total
}

// clavesExternas arma las claves de contadores con un pepper fijo de prueba.
func clavesExternas(tb testing.TB) *auth.Claves {
	tb.Helper()

	claves, err := auth.NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		tb.Fatalf("NuevasClaves: %v", err)
	}
	return claves
}

// abrirRedisExterno conecta con el Redis del carril y lo cierra al terminar.
func abrirRedisExterno(tb testing.TB) *redis.Client {
	tb.Helper()

	raw := os.Getenv("REDIS_URL")
	if raw == "" {
		tb.Fatalf("REDIS_URL es obligatoria bajo el tag integration")
	}
	cliente, err := platformredis.NuevoCliente(raw)
	if err != nil {
		tb.Fatalf("construir cliente Redis: %v", err)
	}
	tb.Cleanup(func() { _ = cliente.Close() })
	return cliente
}

// gestorReenvioExterno construye el gestor con los topes agregados sobre el Redis del carril.
func gestorReenvioExterno(tb testing.TB, pool *pgxpool.Pool, canal auth.CanalOTP, cliente *redis.Client) *auth.GestorDesafios {
	tb.Helper()

	puerto, err := contadores.NuevoRedis(cliente)
	if err != nil {
		tb.Fatalf("NuevoRedis: %v", err)
	}
	gestor, err := auth.NuevoGestorDesafios(pool, canal, auth.NuevoRegistradorEventos(pool), auth.SiempreOptIn{}, pepperDeReenvio,
		auth.ConRacionamiento(puerto, clavesExternas(tb)))
	if err != nil {
		tb.Fatalf("NuevoGestorDesafios: %v", err)
	}
	return gestor
}

func TestReenviarDesafioReemplazaElHmacYConservaIntentos(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	parteID, credencialID, _, celular := sembrarCredencialExterna(t, pool)
	canal := &canalFalso{}
	gestor := gestorReenvioExterno(t, pool, canal, cliente)

	desafio, err := gestor.CrearDesafio(t.Context(), auth.Solicitud{
		ParteID:      parteID,
		CredencialID: credencialID,
		Proposito:    auth.PropositoLogin,
		Celular:      celular,
		Origen:       "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("CrearDesafio: %v", err)
	}
	codigoViejo := canal.ultimoTexto
	_, _, hmacViejo := leerReenvioExterno(t, pool, desafio.ID)

	if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, "000000", auth.PropositoLogin); !errors.Is(err, auth.ErrCodigoInvalido) {
		t.Fatalf("intento fallido = %v, quiero ErrCodigoInvalido", err)
	}
	envejecerUltimoEnvio(t, pool, desafio.ID)

	nuevo, err := gestor.ReenviarDesafio(t.Context(), desafio.ID, "127.0.0.1")
	if err != nil {
		t.Fatalf("ReenviarDesafio: %v", err)
	}
	if nuevo.ExpiraEnSegundos != 300 {
		t.Errorf("ExpiraEnSegundos = %d, quiero 300", nuevo.ExpiraEnSegundos)
	}
	if canal.ultimoTexto == codigoViejo {
		t.Error("el reenvío entregó el mismo código viejo")
	}

	intentos, reenvios, hmacNuevo := leerReenvioExterno(t, pool, desafio.ID)
	if intentos != 1 {
		t.Errorf("intentos = %d, quiero 1 (el reenvío no reinicia intentos)", intentos)
	}
	if reenvios != 1 {
		t.Errorf("reenvios = %d, quiero 1", reenvios)
	}
	if bytes.Equal(hmacViejo, hmacNuevo) {
		t.Error("el reenvío no reemplazó el HMAC anterior")
	}

	if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, codigoViejo, auth.PropositoLogin); !errors.Is(err, auth.ErrCodigoInvalido) {
		t.Fatalf("código viejo = %v, quiero ErrCodigoInvalido", err)
	}
	if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, canal.ultimoTexto, auth.PropositoLogin); err != nil {
		t.Fatalf("código nuevo = %v, quiero nil", err)
	}
}

func TestReenviarDesafioDeRellenoNoEnviaYElRealSi(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := &canalFalso{}
	gestor := gestorDeRecuperacionExterno(t, pool, canal, cliente)
	servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)
	origen := "10.31.7." + uuid.NewString()[:3]

	relleno, err := servicio.SolicitarRecuperacion(t.Context(), "sin-cuenta-"+uuid.NewString()+"@centinela.test", origen)
	if err != nil {
		t.Fatalf("SolicitarRecuperacion sin cuenta: %v", err)
	}
	if fila := leerFilaDeRecuperacion(t, pool, relleno.ID); fila.parteID.Valid || fila.credencialID.Valid {
		t.Fatalf("la fila de relleno trae parte o credencial: %+v", fila)
	}
	envejecerUltimoEnvio(t, pool, relleno.ID)
	respuesta, err := gestor.ReenviarDesafio(t.Context(), relleno.ID, origen)
	if err != nil {
		t.Fatalf("ReenviarDesafio sobre el relleno: %v", err)
	}
	if respuesta.ID != relleno.ID || respuesta.ExpiraEnSegundos != 300 {
		t.Errorf("respuesta del relleno = %+v, quiero el mismo id y 300 segundos", respuesta)
	}
	if canal.ultimoCelular != "" || canal.ultimoTexto != "" {
		t.Fatalf("el reenvio del relleno toco el canal: (%q, %q)", canal.ultimoCelular, canal.ultimoTexto)
	}

	parteID, credencialID, _, celular := sembrarCredencialExterna(t, pool)
	real, err := gestorReenvioExterno(t, pool, canal, cliente).CrearDesafio(t.Context(), auth.Solicitud{ParteID: parteID, CredencialID: credencialID, Proposito: auth.PropositoRegistro, Celular: celular, Origen: origen})
	if err != nil {
		t.Fatalf("CrearDesafio de registro con parte y credencial: %v", err)
	}
	if fila := leerFilaDeRecuperacion(t, pool, real.ID); !fila.parteID.Valid || !fila.credencialID.Valid {
		t.Fatalf("el desafio real de registro no trae parte y credencial: %+v", fila)
	}
	canal.ultimoCelular, canal.ultimoTexto = "", ""
	envejecerUltimoEnvio(t, pool, real.ID)
	if _, err := gestor.ReenviarDesafio(t.Context(), real.ID, origen); err != nil {
		t.Fatalf("ReenviarDesafio sobre el real: %v", err)
	}
	if canal.ultimoCelular != celular || len(canal.ultimoTexto) != 6 {
		t.Errorf("el canal recibio (%q, %q), quiero el celular real %q y un codigo de seis digitos", canal.ultimoCelular, canal.ultimoTexto, celular)
	}
}

func TestCrearDesafioRacionadoSextoEnvioDeLaCuentaBloquea(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := &canalFalso{}
	gestor := gestorReenvioExterno(t, pool, canal, cliente)
	solicitud := auth.Solicitud{
		Proposito:         auth.PropositoRegistro,
		Celular:           "+573009998877",
		Origen:            "10.10.0.1",
		CorreoNormalizado: "tope-" + uuid.NewString() + "@centinela.test",
	}

	for i := 0; i < 5; i++ {
		if _, err := gestor.CrearDesafioRacionado(t.Context(), solicitud); err != nil {
			t.Fatalf("envío %d: %v", i+1, err)
		}
	}
	if _, err := gestor.CrearDesafioRacionado(t.Context(), solicitud); !errors.Is(err, auth.ErrDemasiadosEnvios) {
		t.Fatalf("sexto envío = %v, quiero ErrDemasiadosEnvios", err)
	}
}

func TestRacionadorOrigenVigesimoPrimeroBloquea(t *testing.T) {
	cliente := abrirRedisExterno(t)
	puerto, err := contadores.NuevoRedis(cliente)
	if err != nil {
		t.Fatalf("NuevoRedis: %v", err)
	}
	racionador, err := auth.NuevoRacionadorOTP(puerto, clavesExternas(t))
	if err != nil {
		t.Fatalf("NuevoRacionadorOTP: %v", err)
	}
	origen := "10.20.30." + uuid.NewString()[:3]

	for i := 0; i < 20; i++ {
		if err := racionador.ExigirOrigen(t.Context(), origen); err != nil {
			t.Fatalf("envío %d: %v", i+1, err)
		}
	}
	if err := racionador.ExigirOrigen(t.Context(), origen); !errors.Is(err, auth.ErrDemasiadosEnvios) {
		t.Fatalf("vigésimo primero = %v, quiero ErrDemasiadosEnvios", err)
	}
}

func TestCrearDesafioRacionadoFailClosedNoCreaFila(t *testing.T) {
	pool := abrirPoolExterno(t)
	parteID, credencialID, _, celular := sembrarCredencialExterna(t, pool)
	cliente, err := platformredis.NuevoCliente("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("cliente Redis caído: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	canal := &canalFalso{}
	gestor := gestorReenvioExterno(t, pool, canal, cliente)

	_, err = gestor.CrearDesafioRacionado(t.Context(), auth.Solicitud{
		ParteID:           parteID,
		CredencialID:      credencialID,
		Proposito:         auth.PropositoLogin,
		Celular:           celular,
		Origen:            "127.0.0.1",
		CorreoNormalizado: "caido@centinela.test",
	})
	if !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("fail closed = %v, quiero ErrAuthNoDisponible", err)
	}
	if total := contarDesafiosExterno(t, pool, parteID); total != 0 {
		t.Errorf("desafíos creados con Redis caído = %d, quiero 0", total)
	}
}
