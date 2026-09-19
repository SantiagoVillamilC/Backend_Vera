//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// contadoresRedisDeLogin implementa Contadores contra el Redis real sin importar el paquete contadores.
type contadoresRedisDeLogin struct{ cliente *redis.Client }

// Incrementar usa INCR con TTL atómico vía script, igual que el adaptador de producción.
func (c *contadoresRedisDeLogin) Incrementar(ctx context.Context, clave string, ventana time.Duration) (int64, error) {
	valor, err := c.cliente.Eval(ctx, scriptIncrementoDeLogin, []string{clave}, ventana.Milliseconds()).Int64()
	if err != nil {
		return 0, ConFailClosed(err)
	}
	return valor, nil
}

// Bloquear marca la clave con SET EX y fail closed.
func (c *contadoresRedisDeLogin) Bloquear(ctx context.Context, clave string, hasta time.Duration) error {
	if err := c.cliente.Set(ctx, "bloqueo:"+clave, 1, hasta).Err(); err != nil {
		return ConFailClosed(err)
	}
	return nil
}

// Bloqueada consulta la existencia de la clave de bloqueo con fail closed.
func (c *contadoresRedisDeLogin) Bloqueada(ctx context.Context, clave string) (bool, error) {
	n, err := c.cliente.Exists(ctx, "bloqueo:"+clave).Result()
	if err != nil {
		return false, ConFailClosed(err)
	}
	return n > 0, nil
}

// scriptIncrementoDeLogin incrementa y fija el TTL solo la primera vez, en un solo comando atómico.
const scriptIncrementoDeLogin = `local v = redis.call('INCR', KEYS[1])
if v == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return v`

var _ Contadores = (*contadoresRedisDeLogin)(nil)

// derivadorDeLogin arma Argon2id con los parametros de prueba.
func derivadorDeLogin(tb testing.TB) Derivador {
	tb.Helper()
	derivador, err := NuevoDerivadorArgon2(ParametrosArgon2DePrueba())
	if err != nil {
		tb.Fatalf("derivador: %v", err)
	}
	return derivador
}

// contadoresDeLogin monta el puerto sobre el Redis real del carril.
func contadoresDeLogin(tb testing.TB) Contadores {
	tb.Helper()
	raw := os.Getenv("REDIS_URL")
	if raw == "" {
		tb.Fatalf("REDIS_URL es obligatoria bajo el tag integration")
	}
	cliente, err := platformredis.NuevoCliente(raw)
	if err != nil {
		tb.Fatalf("cliente Redis: %v", err)
	}
	tb.Cleanup(func() { _ = cliente.Close() })
	if err := cliente.Ping(context.Background()).Err(); err != nil {
		tb.Fatalf("ping al Redis: %v", err)
	}
	return &contadoresRedisDeLogin{cliente: cliente}
}

// sembrarCredencialDeLogin crea una parte y su credencial con el estado y la contrasena pedidos.
func sembrarCredencialDeLogin(tb testing.TB, pool *pgxpool.Pool, derivador Derivador, contrasena string, estado EstadoCredencial) (uuid.UUID, string) {
	tb.Helper()
	correo := "login-" + uuid.NewString() + "@centinela.test"
	return sembrarCredencialDeLoginConCorreo(tb, pool, derivador, correo, contrasena, estado)
}

// sembrarCredencialDeLoginConCorreo crea una parte y su credencial con el correo y el estado pedidos.
func sembrarCredencialDeLoginConCorreo(tb testing.TB, pool *pgxpool.Pool, derivador Derivador, correo, contrasena string, estado EstadoCredencial) (uuid.UUID, string) {
	tb.Helper()

	id := uuid.New()
	parte := uuid.New()
	phc, err := derivador.Derivar(context.Background(), contrasena)
	if err != nil {
		tb.Fatalf("derivar la contrasena sembrada: %v", err)
	}
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(parte), Tipo: "persona"}); err != nil {
				return struct{}{}, err
			}
			_, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
				ID:            pgUUID(id),
				ParteID:       pgUUID(parte),
				Correo:        correo,
				Celular:       "+57" + id.String(),
				ValorDerivado: pgtype.Text{String: phc, Valid: true},
				Estado:        string(estado),
			})
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("sembrar credencial: %v", err)
	}
	return id, correo
}

// verificadorDeLogin arma el verificador sobre el pool y Redis reales.
func verificadorDeLogin(tb testing.TB, pool *pgxpool.Pool, derivador Derivador, eventos RegistradorEventos) (*VerificadorDeCredenciales, *Claves) {
	tb.Helper()
	claves, err := NuevasClaves([]byte("1111111111111111111111111111111111111111111111111111111111111111"))
	if err != nil {
		tb.Fatalf("claves: %v", err)
	}
	v, err := NuevoVerificadorDeCredencialesPostgres(pool, derivador, contadoresDeLogin(tb), claves, eventos, ParametrosArgon2DePrueba())
	if err != nil {
		tb.Fatalf("verificador: %v", err)
	}
	return v, claves
}

func TestAutenticarCredencialActivaConContrasenaCorrecta(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	eventos := &registradorEventosDeLogin{}
	v, _ := verificadorDeLogin(t, pool, derivador, eventos)

	id, correo := sembrarCredencialDeLogin(t, pool, derivador, "contrasena-correcta", EstadoActivo)
	credencial, err := v.Autenticar(t.Context(), correo, "contrasena-correcta", "203.0.113.9")
	if err != nil {
		t.Fatalf("Autenticar: %v", err)
	}
	if credencial.CredencialID != id || credencial.Estado != EstadoActivo {
		t.Errorf("credencial = %+v, quiero id %s activo", credencial, id)
	}
	eventos.exigirEvento(t, TipoEventoInicioSesion, resultadoLoginOk)
}

func TestAutenticarSuspendidaSoloTrasProbarLaContrasena(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	v, _ := verificadorDeLogin(t, pool, derivador, &registradorEventosDeLogin{})

	_, correo := sembrarCredencialDeLogin(t, pool, derivador, "contrasena-correcta", EstadoSuspendido)

	if _, err := v.Autenticar(t.Context(), correo, "contrasena-correcta", "203.0.113.9"); !errors.Is(err, ErrCuentaSuspendida) {
		t.Fatalf("con contrasena buena quiero ErrCuentaSuspendida, obtuve %v", err)
	}
	if _, err := v.Autenticar(t.Context(), correo, "contrasena-mala", "203.0.113.9"); !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("con contrasena mala quiero ErrCredencialesInvalidas, obtuve %v", err)
	}
}

func TestAutenticarCerradaRespondeCredencialesInvalidas(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	v, _ := verificadorDeLogin(t, pool, derivador, &registradorEventosDeLogin{})

	_, correo := sembrarCredencialDeLogin(t, pool, derivador, "contrasena-correcta", EstadoCerrado)
	if _, err := v.Autenticar(t.Context(), correo, "contrasena-correcta", "203.0.113.9"); !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("quiero ErrCredencialesInvalidas, obtuve %v", err)
	}
}

func TestAutenticarTrasNFallosBloqueaTambienElCorreoInexistente(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	v, _ := verificadorDeLogin(t, pool, derivador, &registradorEventosDeLogin{})

	_, correo := sembrarCredencialDeLogin(t, pool, derivador, "contrasena-correcta", EstadoActivo)
	for i := 0; i <= maxFallosPorCuenta; i++ {
		if _, err := v.Autenticar(t.Context(), correo, "contrasena-mala", "203.0.113.9"); !errors.Is(err, ErrCredencialesInvalidas) && !errors.Is(err, ErrDemasiadosEnvios) {
			t.Fatalf("fallo %d: %v", i, err)
		}
	}
	if _, err := v.Autenticar(t.Context(), correo, "contrasena-correcta", "203.0.113.9"); !errors.Is(err, ErrDemasiadosEnvios) {
		t.Fatalf("tras N fallos quiero ErrDemasiadosEnvios, obtuve %v", err)
	}

	inexistente := "nadie-" + uuid.NewString() + "@centinela.test"
	for i := 0; i <= maxFallosPorCuenta; i++ {
		if _, err := v.Autenticar(t.Context(), inexistente, "contrasena-mala", "203.0.113.9"); !errors.Is(err, ErrCredencialesInvalidas) && !errors.Is(err, ErrDemasiadosEnvios) {
			t.Fatalf("inexistente fallo %d: %v", i, err)
		}
	}
	if _, err := v.Autenticar(t.Context(), inexistente, "contrasena-mala", "203.0.113.9"); !errors.Is(err, ErrDemasiadosEnvios) {
		t.Fatalf("el correo inexistente debe agotar el mismo umbral, obtuve %v", err)
	}
}

func TestAutenticarRellenoInexistenteCuentaElFalloIgualQueLaCuentaReal(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	v, claves := verificadorDeLogin(t, pool, derivador, &registradorEventosDeLogin{})

	_, correoReal := sembrarCredencialDeLogin(t, pool, derivador, "contrasena-correcta", EstadoActivo)
	inexistente := "nadie-" + uuid.NewString() + "@centinela.test"

	if _, err := v.Autenticar(t.Context(), correoReal, "contrasena-mala", "203.0.113.60"); !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("cuenta real = %v, quiero ErrCredencialesInvalidas", err)
	}
	if _, err := v.Autenticar(t.Context(), inexistente, rellenoContrasena, "203.0.113.61"); !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("relleno con correo inexistente = %v, quiero ErrCredencialesInvalidas", err)
	}

	claveReal := PrefijoFallosCuenta + claves.ClaveDeCorreo(NormalizarCorreo(correoReal))
	claveInexistente := PrefijoFallosCuenta + claves.ClaveDeCorreo(NormalizarCorreo(inexistente))
	if got := leerContadorDeLogin(t, claveReal); got != 1 {
		t.Fatalf("fallos de la cuenta real = %d, quiero 1", got)
	}
	if got := leerContadorDeLogin(t, claveInexistente); got != 1 {
		t.Fatalf("fallos del correo inexistente = %d, quiero 1 (el relleno ya no sale limpio)", got)
	}
}

func TestAutenticarTrasCerrarYRegistrarElMismoCorreoAutenticaLaNoCerrada(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	v, _ := verificadorDeLogin(t, pool, derivador, &registradorEventosDeLogin{})

	correo := "login-reuso-" + uuid.NewString() + "@centinela.test"
	sembrarCredencialDeLoginConCorreo(t, pool, derivador, correo, "contrasena-cerrada", EstadoCerrado)
	idActiva, _ := sembrarCredencialDeLoginConCorreo(t, pool, derivador, correo, "contrasena-activa", EstadoActivo)

	credencial, err := v.Autenticar(t.Context(), correo, "contrasena-activa", "203.0.113.70")
	if err != nil {
		t.Fatalf("Autenticar: %v", err)
	}
	if credencial.CredencialID != idActiva {
		t.Fatalf("CredencialID = %s, quiero la no cerrada %s", credencial.CredencialID, idActiva)
	}
}

// leerContadorDeLogin lee el valor crudo de una clave de contador en el Redis del carril.
func leerContadorDeLogin(tb testing.TB, clave string) int64 {
	tb.Helper()
	cliente, err := platformredis.NuevoCliente(os.Getenv("REDIS_URL"))
	if err != nil {
		tb.Fatalf("cliente Redis: %v", err)
	}
	defer func() { _ = cliente.Close() }()
	valor, err := cliente.Get(tb.Context(), clave).Int64()
	if errors.Is(err, redis.Nil) {
		return 0
	}
	if err != nil {
		tb.Fatalf("leer contador %q: %v", clave, err)
	}
	return valor
}

func TestRehashConcurrenteNoPisaElValorNuevo(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	derivador := derivadorDeLogin(t)

	viejos := ParametrosArgon2{MemoriaKiB: 4096, Iteraciones: 1, Paralelismo: 1, LongitudSal: 16, LongitudSalida: 32}
	derivadorViejo, err := NuevoDerivadorArgon2(viejos)
	if err != nil {
		t.Fatalf("derivador viejo: %v", err)
	}
	id, _ := sembrarCredencialDeLogin(t, pool, derivadorViejo, "contrasena-correcta", EstadoActivo)
	viejo := leerValorDerivado(t, pool, id)

	// El reset concurrente gana primero y cambia el valor_derivado.
	valorReset, err := derivador.Derivar(t.Context(), "contrasena-reseteada")
	if err != nil {
		t.Fatalf("derivar el reset: %v", err)
	}
	rehashLogin, err := derivador.Derivar(t.Context(), "contrasena-correcta")
	if err != nil {
		t.Fatalf("derivar el rehash: %v", err)
	}

	// Dos goroutines parten del mismo PHC viejo: la del reset gana y la del login no debe pisarla.
	resultados := make([]int64, 2)
	var wg sync.WaitGroup
	valores := []string{valorReset, rehashLogin}
	for i := range resultados {
		wg.Go(func() {
			filas, err := enTransaccion(context.Background(), poolDeAuth{pool: pool},
				func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (int64, error) {
					return q.RehashCredencial(ctx, repositorio.RehashCredencialParams{
						ValorDerivado:   pgtype.Text{String: valores[i], Valid: true},
						ID:              pgUUID(id),
						ValorDerivado_2: pgtype.Text{String: viejo, Valid: true},
					})
				})
			if err != nil {
				t.Errorf("rehash concurrente: %v", err)
			}
			resultados[i] = filas
		})
	}
	wg.Wait()

	if resultados[0]+resultados[1] != 1 {
		t.Errorf("filas aplicadas = %d, quiero exactamente 1 (un CAS gana)", resultados[0]+resultados[1])
	}
	if got := leerValorDerivado(t, pool, id); got != valorReset && got != rehashLogin {
		t.Errorf("valor_derivado final = %q, quiero el de uno de los dos CAS", got)
	}
}

// leerValorDerivado lee el PHC de una credencial y libera la conexión del pool.
func leerValorDerivado(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) string {
	tb.Helper()
	tx, _ := abrirTransaccionDePrueba(tb, pool)
	var valor string
	err := tx.QueryRow(tb.Context(), "SELECT valor_derivado FROM auth.credenciales WHERE id = $1", pgUUID(id)).Scan(&valor)
	_ = tx.Rollback(tb.Context())
	if err != nil {
		tb.Fatalf("leer valor derivado: %v", err)
	}
	return valor
}

func TestTimingMedianaCorreoInexistenteYContrasenaMala(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeLogin(t)
	v, _ := verificadorDeLogin(t, pool, derivador, &registradorEventosDeLogin{})

	_, correo := sembrarCredencialDeLogin(t, pool, derivador, "contrasena-correcta", EstadoActivo)
	inexistente := "nadie-" + uuid.NewString() + "@centinela.test"

	medianaInexistente := medianaDeAutenticacion(t, v, inexistente, "contrasena-mala")
	medianaMala := medianaDeAutenticacion(t, v, correo, "contrasena-mala")
	t.Logf("mediana correo inexistente = %s; mediana contrasena mala = %s", medianaInexistente, medianaMala)

	if medianaInexistente > 10*medianaMala || medianaMala > 10*medianaInexistente {
		t.Errorf("las medianas difieren en más de un orden de magnitud: inexistente=%s mala=%s", medianaInexistente, medianaMala)
	}
}

// medianaDeAutenticacion corre 20 intentos fallidos y devuelve la mediana de su duración.
func medianaDeAutenticacion(t *testing.T, v *VerificadorDeCredenciales, correo, contrasena string) time.Duration {
	t.Helper()
	const iteraciones = 20
	muestras := make([]time.Duration, 0, iteraciones)
	for range iteraciones {
		inicio := time.Now()
		_, _ = v.Autenticar(t.Context(), correo, contrasena, "203.0.113.9")
		muestras = append(muestras, time.Since(inicio))
	}
	for i := range muestras {
		for j := i + 1; j < len(muestras); j++ {
			if muestras[j] < muestras[i] {
				muestras[i], muestras[j] = muestras[j], muestras[i]
			}
		}
	}
	return muestras[len(muestras)/2]
}

// registradorEventosDeLogin captura los eventos en memoria para las aserciones.
type registradorEventosDeLogin struct {
	mu      sync.Mutex
	eventos []Evento
}

// Registrar guarda el evento sin persistir, suficiente para verificar tipo y resultado.
func (r *registradorEventosDeLogin) Registrar(_ context.Context, e Evento) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventos = append(r.eventos, e)
	return nil
}

// exigirEvento exige al menos un evento del tipo y resultado pedidos, sin contraseña ni correo.
func (r *registradorEventosDeLogin) exigirEvento(t *testing.T, tipo, resultado string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.eventos {
		if e.Tipo == tipo && e.Resultado == resultado {
			if e.Contexto == "" {
				t.Errorf("el evento %s/%s no trae huella en Contexto", tipo, resultado)
			}
			return
		}
	}
	t.Fatalf("no hay evento %s/%s entre %d eventos", tipo, resultado, len(r.eventos))
}
