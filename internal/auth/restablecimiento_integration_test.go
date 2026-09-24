//go:build integration

package auth

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/correosim"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// ttlResetDePrueba es la vida del token de restablecimiento en los tests.
const ttlResetDePrueba = 10 * time.Minute

// canalDeCodigoFijo entrega un código determinista y guarda el último por celular sin red.
type canalDeCodigoFijo struct {
	mu      sync.Mutex
	ultimos map[string]string
	codigo  string
}

// Enviar guarda el código del celular y devuelve un id determinista.
func (c *canalDeCodigoFijo) Enviar(_ context.Context, celular, codigo string) (string, error) {
	c.mu.Lock()
	c.ultimos[celular] = codigo
	c.mu.Unlock()
	return "sim-" + celular, nil
}

// UltimoCodigo devuelve el último código guardado para el celular y si existe.
func (c *canalDeCodigoFijo) UltimoCodigo(celular string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	codigo, ok := c.ultimos[celular]
	return codigo, ok
}

// CodigoFijo devuelve el código que el servicio debe generar.
func (c *canalDeCodigoFijo) CodigoFijo() (string, bool) { return c.codigo, c.codigo != "" }

// nuevoCanalDeCodigoFijo arma el canal con su código determinista y su mapa de entregas.
func nuevoCanalDeCodigoFijo(codigo string) *canalDeCodigoFijo {
	return &canalDeCodigoFijo{ultimos: map[string]string{}, codigo: codigo}
}

// montajeDeRestablecimiento agrupa los servicios del cierre con sus colaboradores reales.
type montajeDeRestablecimiento struct {
	recuperacion     *ServicioDeRecuperacion
	restablecimiento *ServicioDeRestablecimiento
	login            *ServicioDeLogin
	sesiones         *GestorSesiones
	denylist         *denylistRedisDeSesion
	canal            *canalDeCodigoFijo
	derivador        Derivador
	claves           *Claves
	eventos          *registradorEventosDeLogin
}

// montajeRestablecimientoDePrueba arma el cierre completo sobre Postgres y Redis reales.
func montajeRestablecimientoDePrueba(tb testing.TB, pool *pgxpool.Pool) montajeDeRestablecimiento {
	tb.Helper()

	derivador := derivadorDeLogin(tb)
	eventos := &registradorEventosDeLogin{}
	canal := nuevoCanalDeCodigoFijo("123456")
	verificador, claves := verificadorDeLogin(tb, pool, derivador, eventos)
	contadores := contadoresDeLogin(tb)
	gestor, err := NuevoGestorDesafios(pool, canal, eventos, NuevoLectorOptInDeConsentimientos(pool), pepperDeDesafio,
		ConRacionamiento(contadores, claves))
	if err != nil {
		tb.Fatalf("gestor de desafíos: %v", err)
	}
	sesiones := NuevoGestorSesiones(pool, eventos)
	tokens, err := NuevoTokensDeAcceso([]byte("clave-de-firma-de-al-menos-32-bytes"), "centinela-auth", "centinela-api")
	if err != nil {
		tb.Fatalf("tokens de acceso: %v", err)
	}
	login, err := NuevoServicioDeLogin(verificador, pool, sesiones, tokens)
	if err != nil {
		tb.Fatalf("servicio de login: %v", err)
	}
	denylist := denylistDeSesionDePrueba(tb)
	recuperacion, err := NuevoServicioDeRecuperacion(pool, gestor, eventos, contadores, claves, pepperDeDesafio)
	if err != nil {
		tb.Fatalf("servicio de recuperación: %v", err)
	}
	restablecimiento, err := NuevoServicioDeRestablecimiento(pool, gestor, sesiones, derivador, denylist, eventos,
		NuevoLectorOptInDeConsentimientos(pool), contadores, claves, pepperDeDesafio, ttlResetDePrueba)
	if err != nil {
		tb.Fatalf("servicio de restablecimiento: %v", err)
	}
	return montajeDeRestablecimiento{
		recuperacion: recuperacion, restablecimiento: restablecimiento, login: login, sesiones: sesiones,
		denylist: denylist, canal: canal, derivador: derivador, claves: claves, eventos: eventos,
	}
}

// clienteRedisDeRestablecimiento abre un cliente directo para inspeccionar el Redis del carril.
func clienteRedisDeRestablecimiento(tb testing.TB) *redis.Client {
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
	return cliente
}

// sembrarOptInWhatsApp inserta el consentimiento que habilita el aviso por WhatsApp.
func sembrarOptInWhatsApp(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) {
	tb.Helper()

	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				`INSERT INTO auth.consentimientos (id, parte_id, tipo, version_texto, texto, aceptado, accion, origen, metodo)
				 VALUES ($1, $2, 'whatsapp_optin', 'v1', 'texto', true, 'otorgar', 'test', 'check')`,
				pgUUID(uuid.New()), pgUUID(parteID))
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("sembrar opt-in: %v", err)
	}
}

// revocarOptInWhatsApp inserta un consentimiento posterior que apaga el opt-in de WhatsApp.
func revocarOptInWhatsApp(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) {
	tb.Helper()

	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				`INSERT INTO auth.consentimientos (id, parte_id, tipo, version_texto, texto, aceptado, accion, origen, metodo, ocurrido_en)
				 VALUES ($1, $2, 'whatsapp_optin', 'v1', 'texto', false, 'revocar', 'test', 'check', now() + interval '1 second')`,
				pgUUID(uuid.New()), pgUUID(parteID))
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("revocar opt-in: %v", err)
	}
}

// sembrarCodigosRespaldo inserta los HMAC de la cantidad pedida y devuelve los códigos en claro.
func sembrarCodigosRespaldo(tb testing.TB, pool *pgxpool.Pool, credencialID uuid.UUID, cantidad int) []string {
	tb.Helper()

	codigos, err := generarCodigosRespaldo(cantidad)
	if err != nil {
		tb.Fatalf("generar códigos de respaldo: %v", err)
	}
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			for _, codigo := range codigos {
				if err := q.CrearCodigoRespaldo(ctx, repositorio.CrearCodigoRespaldoParams{
					ID:           pgUUID(uuid.New()),
					CredencialID: pgUUID(credencialID),
					CodigoHmac:   hmacDeCodigoRespaldo(pepperDeDesafio, codigo),
				}); err != nil {
					return struct{}{}, err
				}
			}
			return struct{}{}, nil
		}); err != nil {
		tb.Fatalf("sembrar códigos de respaldo: %v", err)
	}
	return codigos
}

// crearDesafioDeRecuperacion crea un desafío real de recuperación con el canal determinista.
func crearDesafioDeRecuperacion(tb testing.TB, m montajeDeRestablecimiento, pool *pgxpool.Pool, credencialID uuid.UUID) Desafio {
	tb.Helper()

	desafio, err := m.recuperacion.SolicitarRecuperacion(tb.Context(), correoDeCredencial(tb, pool, credencialID), "10.41.1.1")
	if err != nil {
		tb.Fatalf("SolicitarRecuperacion: %v", err)
	}
	return desafio
}

// correoDeCredencial lee el correo de la credencial para las llamadas que lo exigen.
func correoDeCredencial(tb testing.TB, pool *pgxpool.Pool, credencialID uuid.UUID) string {
	tb.Helper()

	correo, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (string, error) {
			credencial, err := q.CredencialPorID(ctx, pgUUID(credencialID))
			return credencial.Correo, err
		})
	if err != nil {
		tb.Fatalf("leer el correo de la credencial: %v", err)
	}
	return correo
}

// leerEstadoDesafio lee el estado y los intentos del desafío como rol_auth.
func leerEstadoDesafio(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) (string, int) {
	tb.Helper()

	var (
		estado   string
		intentos int
	)
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `SELECT estado, intentos FROM auth.desafios WHERE id = $1`, pgUUID(id)).Scan(&estado, &intentos)
		}); err != nil {
		tb.Fatalf("leer el desafío: %v", err)
	}
	return estado, intentos
}

// leerTokenRestablecimiento lee expiración y uso del token por su hash.
func leerTokenRestablecimiento(tb testing.TB, pool *pgxpool.Pool, hash []byte) (expiraEn time.Time, usado pgtype.Timestamptz) {
	tb.Helper()

	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT expira_en, usado_en FROM auth.tokens_restablecimiento WHERE token_hash = $1`, hash).
				Scan(&expiraEn, &usado)
		}); err != nil {
		tb.Fatalf("leer el token: %v", err)
	}
	return expiraEn, usado
}

// envejecerTokenRestablecimiento lleva la expiración al pasado para probar el token vencido.
func envejecerTokenRestablecimiento(tb testing.TB, hash []byte) {
	tb.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		tb.Fatalf("DATABASE_URL es obligatoria para manipular la expiración como dueño del esquema")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parsear DATABASE_URL: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		tb.Fatalf("abrir pool dueño: %v", err)
	}
	tb.Cleanup(pool.Close)
	if _, err := pool.Exec(tb.Context(),
		`UPDATE auth.tokens_restablecimiento SET expira_en = now() - interval '1 minute' WHERE token_hash = $1`, hash); err != nil {
		tb.Fatalf("envejecer el token como dueño: %v", err)
	}
}

// codigoUsado informa si el código de respaldo ya quedó marcado como usado.
func codigoUsado(tb testing.TB, pool *pgxpool.Pool, credencialID uuid.UUID, codigo string) bool {
	tb.Helper()

	hmac := hmacDeCodigoRespaldo(pepperDeDesafio, codigo)
	usado := false
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT usado_en IS NOT NULL FROM auth.codigos_respaldo WHERE credencial_id = $1 AND codigo_hmac = $2`,
				pgUUID(credencialID), hmac).Scan(&usado)
		}); err != nil {
		tb.Fatalf("leer el código usado: %v", err)
	}
	return usado
}

// contarAvisosPorDestino cuenta las filas del outbox del aviso de contraseña por destino.
func contarAvisosPorDestino(tb testing.TB, pool *pgxpool.Pool, destino, direccion string) int {
	tb.Helper()

	total := 0
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			campo := "correo"
			if destino == DestinoWhatsApp {
				campo = "celular"
			}
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT count(*) FROM auth.outbox WHERE tipo = $1 AND payload->>'destino' = $2 AND payload->>'`+campo+`' = $3`,
				TipoAvisoContrasenaCambiada, destino, direccion).Scan(&total)
		}); err != nil {
		tb.Fatalf("contar avisos: %v", err)
	}
	return total
}

// exigirEventoEnMemoria exige un evento del tipo y resultado pedidos en el registrador falso.
func exigirEventoEnMemoria(t *testing.T, eventos *registradorEventosDeLogin, tipo, resultado string) {
	t.Helper()

	eventos.mu.Lock()
	defer eventos.mu.Unlock()
	for _, e := range eventos.eventos {
		if e.Tipo == tipo && e.Resultado == resultado {
			return
		}
	}
	t.Fatalf("no hay evento %s/%s entre %d eventos", tipo, resultado, len(eventos.eventos))
}

// exigirMotivoDeEvento exige un evento del tipo y resultado pedidos con el motivo indicado.
func exigirMotivoDeEvento(t *testing.T, eventos *registradorEventosDeLogin, tipo, resultado, motivo string) {
	t.Helper()

	eventos.mu.Lock()
	defer eventos.mu.Unlock()
	for _, e := range eventos.eventos {
		if e.Tipo == tipo && e.Resultado == resultado && e.Detalle["motivo"] == motivo {
			return
		}
	}
	t.Fatalf("no hay evento %s/%s con motivo %q", tipo, resultado, motivo)
}

func TestRestablecimientoFlujoCompleto(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	m := montajeRestablecimientoDePrueba(t, pool)
	credencialID, parteID, correo, celular := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-vieja", EstadoActivo, true)
	sembrarOptInWhatsApp(t, pool, parteID)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 2)
	refresh, sesion, err := m.sesiones.EmitirSesion(t.Context(), parteID, "", "test")
	if err != nil {
		t.Fatalf("EmitirSesion: %v", err)
	}

	desafio := crearDesafioDeRecuperacion(t, m, pool, credencialID)
	otp, ok := m.canal.UltimoCodigo(celular)
	if !ok {
		t.Fatalf("el simulador no entregó el código a %s", celular)
	}

	resetToken, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), desafio.ID, otp, codigos[0], "10.41.1.2")
	if err != nil {
		t.Fatalf("VerificarSegundoFactor: %v", err)
	}
	if resetToken == "" {
		t.Fatal("reset_token vacío")
	}
	hash := hashDeResetToken(resetToken)
	if _, usado := leerTokenRestablecimiento(t, pool, hash); usado.Valid {
		t.Error("el token recién emitido ya está usado")
	}

	if err := m.restablecimiento.RestablecerContrasena(t.Context(), resetToken, "contrasena-nueva"); err != nil {
		t.Fatalf("RestablecerContrasena: %v", err)
	}

	if _, err := m.login.IniciarSesion(t.Context(), celular, "contrasena-vieja", "", "10.41.1.4", "dispositivo-de-prueba"); !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("login con la contraseña vieja = %v, quiero ErrCredencialesInvalidas", err)
	}

	activas, err := m.sesiones.ListarActivas(t.Context(), parteID)
	if err != nil {
		t.Fatalf("ListarActivas: %v", err)
	}
	if len(activas) != 0 {
		t.Errorf("sesiones activas tras el reset = %d, quiero 0", len(activas))
	}
	revocado, err := m.denylist.Revocado(t.Context(), sesion.FamiliaID)
	if err != nil {
		t.Fatalf("denylist: %v", err)
	}
	if !revocado {
		t.Error("el sid de la sesión revocada no quedó en la denylist")
	}
	if _, _, err := m.sesiones.RotarRefresh(t.Context(), refresh); !errors.Is(err, ErrRefreshInvalido) {
		t.Fatalf("rotar el refresh revocado = %v, quiero ErrRefreshInvalido", err)
	}

	nuevo, err := m.login.IniciarSesion(t.Context(), celular, "contrasena-nueva", "", "10.41.1.3", "dispositivo-de-prueba")
	if err != nil {
		t.Fatalf("login con la contraseña nueva: %v", err)
	}
	if nuevo.AccessToken == "" || nuevo.RefreshToken == "" {
		t.Error("el login con la contraseña nueva no devolvió el par de tokens")
	}

	if total := contarAvisosPorDestino(t, pool, DestinoCorreo, correo); total != 1 {
		t.Errorf("avisos por correo = %d, quiero 1", total)
	}
	if total := contarAvisosPorDestino(t, pool, DestinoWhatsApp, celular); total != 1 {
		t.Errorf("avisos por WhatsApp = %d, quiero 1", total)
	}

	exigirEventoEnMemoria(t, m.eventos, TipoEventoRecuperacion, resultadoSegundoFactorVerificado)
	exigirEventoEnMemoria(t, m.eventos, TipoEventoRecuperacion, resultadoContrasenaRestablecida)
	exigirEventoEnMemoria(t, m.eventos, TipoEventoRevocacion, resultadoSesion)
	exigirMotivoDeEvento(t, m.eventos, TipoEventoRecuperacion, resultadoContrasenaRestablecida, "aviso_correo_whatsapp")
}

func TestAvisoDeContrasenaSeEntregaPorLosDosCanales(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	m := montajeRestablecimientoDePrueba(t, pool)
	credencialID, parteID, correo, celular := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-vieja", EstadoActivo, true)
	sembrarOptInWhatsApp(t, pool, parteID)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 1)

	desafio := crearDesafioDeRecuperacion(t, m, pool, credencialID)
	otp, _ := m.canal.UltimoCodigo(celular)
	resetToken, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), desafio.ID, otp, codigos[0], "10.42.1.1")
	if err != nil {
		t.Fatalf("VerificarSegundoFactor: %v", err)
	}
	if err := m.restablecimiento.RestablecerContrasena(t.Context(), resetToken, "contrasena-nueva"); err != nil {
		t.Fatalf("RestablecerContrasena: %v", err)
	}

	correoSim := correosim.Nuevo()
	whatsapp := nuevoCanalDeCodigoFijo("")
	despachador := NuevoDespachadorDeAvisos(pool,
		NuevoNotificadorDeAvisos(correoSim, whatsapp),
		PoliticaReintento{Base: time.Second, Factor: 2, Tope: time.Minute})
	despachador.Registrar(TipoAvisoAbrirSubcuentas, func(context.Context, string, []byte) (string, error) { return "", nil })
	if err := despachador.DespacharAvisos(t.Context()); err != nil {
		t.Fatalf("DespacharAvisos: %v", err)
	}

	if _, ok := correoSim.UltimoAviso(correo); !ok {
		t.Error("el simulador de correo no recibió el aviso")
	}
	if _, ok := whatsapp.UltimoCodigo(celular); !ok {
		t.Error("el simulador de WhatsApp no recibió el aviso")
	}

	// El payload no lleva la contraseña ni el PHC.
	campos := map[string]string{
		"payload":      "coalesce(string_agg(payload::text, ' '), '')",
		"ultimo_error": "coalesce(string_agg(coalesce(ultimo_error, ''), ' '), '')",
	}
	for nombre, agregado := range campos {
		var valor string
		if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
			func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
				return struct{}{}, tx.QueryRow(ctx,
					`SELECT `+agregado+` FROM auth.outbox WHERE tipo = $1`,
					TipoAvisoContrasenaCambiada).Scan(&valor)
			}); err != nil {
			t.Fatalf("leer el outbox: %v", err)
		}
		if strings.Contains(valor, "contrasena-nueva") || strings.Contains(valor, "$argon2id$") || strings.Contains(valor, resetToken) {
			t.Errorf("el outbox (%s) filtra un secreto: %q", nombre, valor)
		}
	}
}

func TestCodigoRespaldoUsadoNoVuelveAServir(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	m := montajeRestablecimientoDePrueba(t, pool)
	credencialID, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-vieja", EstadoActivo, true)
	sembrarOptInWhatsApp(t, pool, parteID)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 1)
	correo := correoDeCredencial(t, pool, credencialID)

	desafio := crearDesafioDeRecuperacion(t, m, pool, credencialID)
	otp, _ := m.canal.UltimoCodigo(celular)
	if _, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), desafio.ID, otp, codigos[0], "10.43.1.1"); err != nil {
		t.Fatalf("primer VerificarSegundoFactor: %v", err)
	}
	if !codigoUsado(t, pool, credencialID, codigos[0]) {
		t.Fatal("el código consumido no quedó con usado_en")
	}

	segundo, err := m.recuperacion.SolicitarRecuperacion(t.Context(), correo, "10.43.1.2")
	if err != nil {
		t.Fatalf("segunda solicitud: %v", err)
	}
	otpSegundo, _ := m.canal.UltimoCodigo(celular)
	if _, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), segundo.ID, otpSegundo, codigos[0], "10.43.1.3"); !errors.Is(err, ErrSegundoFactorRequerido) {
		t.Fatalf("segundo uso del código = %v, quiero ErrSegundoFactorRequerido", err)
	}
}

func TestConsumirCodigoRespaldoConcurrenteUnaSolaVez(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	m := montajeRestablecimientoDePrueba(t, pool)
	credencialID, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-vieja", EstadoActivo, true)
	sembrarOptInWhatsApp(t, pool, parteID)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 1)
	correo := correoDeCredencial(t, pool, credencialID)

	primero, err := m.recuperacion.SolicitarRecuperacion(t.Context(), correo, "10.44.1.1")
	if err != nil {
		t.Fatalf("primera solicitud: %v", err)
	}
	segundo, err := m.recuperacion.SolicitarRecuperacion(t.Context(), correo, "10.44.1.2")
	if err != nil {
		t.Fatalf("segunda solicitud: %v", err)
	}
	otp, _ := m.canal.UltimoCodigo(celular)

	resultados := make(chan error, 2)
	for _, desafio := range []Desafio{primero, segundo} {
		go func(id uuid.UUID) {
			_, err := m.restablecimiento.VerificarSegundoFactor(context.Background(), id, otp, codigos[0], "10.44.1.3")
			resultados <- err
		}(desafio.ID)
	}
	exitos, invalidos := 0, 0
	for range 2 {
		switch err := <-resultados; {
		case err == nil:
			exitos++
		case errors.Is(err, ErrSegundoFactorRequerido):
			invalidos++
		default:
			t.Fatalf("error inesperado: %v", err)
		}
	}
	if exitos != 1 || invalidos != 1 {
		t.Fatalf("exitos/invalidos = %d/%d, quiero 1/1", exitos, invalidos)
	}
}

func TestSegundoFactorMaloInvalidaElDesafioYNoSePuedeReintentar(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	m := montajeRestablecimientoDePrueba(t, pool)
	credencialID, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-vieja", EstadoActivo, true)
	sembrarOptInWhatsApp(t, pool, parteID)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 1)

	desafio := crearDesafioDeRecuperacion(t, m, pool, credencialID)
	otp, _ := m.canal.UltimoCodigo(celular)

	if _, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), desafio.ID, otp, "ZZZZ-ZZZZ-ZZZZ-ZZZZ", "10.45.1.1"); !errors.Is(err, ErrSegundoFactorRequerido) {
		t.Fatalf("segundo factor malo = %v, quiero ErrSegundoFactorRequerido", err)
	}
	if estado, intentos := leerEstadoDesafio(t, pool, desafio.ID); estado != "invalidado" || intentos != 1 {
		t.Fatalf("desafío = %s/%d, quiero invalidado/1", estado, intentos)
	}
	if _, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), desafio.ID, otp, codigos[0], "10.45.1.2"); !errors.Is(err, ErrDesafioInvalido) {
		t.Fatalf("reintento sobre el desafío invalidado = %v, quiero ErrDesafioInvalido", err)
	}

	cliente := clienteRedisDeRestablecimiento(t)
	clave := PrefijoFallosCuenta + m.claves.ClaveDeCorreo(correoDeCredencial(t, pool, credencialID))
	if n, err := cliente.Exists(t.Context(), clave).Result(); err != nil || n == 0 {
		t.Fatalf("contador de fallos = %d (err %v), quiero al menos 1", n, err)
	}
	exigirEventoEnMemoria(t, m.eventos, TipoEventoRecuperacion, resultadoSegundoFactorInvalido)
}

func TestResetTokenUnSoloUsoYVencido(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	m := montajeRestablecimientoDePrueba(t, pool)
	credencialID, parteID, _, celular := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-vieja", EstadoActivo, true)
	sembrarOptInWhatsApp(t, pool, parteID)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 3)
	correo := correoDeCredencial(t, pool, credencialID)

	desafio := crearDesafioDeRecuperacion(t, m, pool, credencialID)
	otp, _ := m.canal.UltimoCodigo(celular)
	resetToken, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), desafio.ID, otp, codigos[0], "10.46.1.1")
	if err != nil {
		t.Fatalf("VerificarSegundoFactor: %v", err)
	}
	if err := m.restablecimiento.RestablecerContrasena(t.Context(), resetToken, "contrasena-nueva"); err != nil {
		t.Fatalf("primer reset: %v", err)
	}
	if err := m.restablecimiento.RestablecerContrasena(t.Context(), resetToken, "otra-contrasena"); !errors.Is(err, ErrResetInvalido) {
		t.Fatalf("segundo reset con el mismo token = %v, quiero ErrResetInvalido", err)
	}

	segundo, err := m.recuperacion.SolicitarRecuperacion(t.Context(), correo, "10.46.1.2")
	if err != nil {
		t.Fatalf("segunda solicitud: %v", err)
	}
	otpSegundo, _ := m.canal.UltimoCodigo(celular)
	vencido, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), segundo.ID, otpSegundo, codigos[1], "10.46.1.3")
	if err != nil {
		t.Fatalf("segundo VerificarSegundoFactor: %v", err)
	}
	envejecerTokenRestablecimiento(t, hashDeResetToken(vencido))
	if err := m.restablecimiento.RestablecerContrasena(t.Context(), vencido, "contrasena-nueva"); !errors.Is(err, ErrResetInvalido) {
		t.Fatalf("reset con token vencido = %v, quiero ErrResetInvalido", err)
	}
}

func TestAvisoSinOptInSoloPorCorreo(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	m := montajeRestablecimientoDePrueba(t, pool)
	credencialID, parteID, correo, celular := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-vieja", EstadoActivo, true)
	sembrarOptInWhatsApp(t, pool, parteID)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 1)

	desafio := crearDesafioDeRecuperacion(t, m, pool, credencialID)
	revocarOptInWhatsApp(t, pool, parteID)
	otp, _ := m.canal.UltimoCodigo(celular)
	resetToken, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), desafio.ID, otp, codigos[0], "10.47.1.1")
	if err != nil {
		t.Fatalf("VerificarSegundoFactor: %v", err)
	}
	if err := m.restablecimiento.RestablecerContrasena(t.Context(), resetToken, "contrasena-nueva"); err != nil {
		t.Fatalf("RestablecerContrasena: %v", err)
	}

	if total := contarAvisosPorDestino(t, pool, DestinoCorreo, correo); total != 1 {
		t.Errorf("avisos por correo = %d, quiero 1", total)
	}
	if total := contarAvisosPorDestino(t, pool, DestinoWhatsApp, celular); total != 0 {
		t.Errorf("avisos por WhatsApp sin opt-in = %d, quiero 0", total)
	}
	exigirMotivoDeEvento(t, m.eventos, TipoEventoRecuperacion, resultadoContrasenaRestablecida, "aviso_correo")
}

func TestRellenoPresentadoAVerifyRespondeOtpInvalid(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	m := montajeRestablecimientoDePrueba(t, pool)

	relleno, err := m.recuperacion.SolicitarRecuperacion(t.Context(), "relleno-"+uuid.NewString()+"@centinela.test", "10.48.1.1")
	if err != nil {
		t.Fatalf("SolicitarRecuperacion del relleno: %v", err)
	}
	if _, err := m.restablecimiento.VerificarSegundoFactor(t.Context(), relleno.ID, "123456", "ABCD-EFGH-IJKL-MNOP", "10.48.1.2"); !errors.Is(err, ErrCodigoInvalido) {
		t.Fatalf("relleno presentado a verify = %v, quiero ErrCodigoInvalido", err)
	}
}

func TestRolAuthNoPuedeBorrarTokensRestablecimiento(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	tx, _ := abrirTransaccionDePrueba(t, pool)

	_, err := tx.Exec(t.Context(), "DELETE FROM auth.tokens_restablecimiento")
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
	if !strings.Contains(pgErr.Message, "permission denied for table tokens_restablecimiento") {
		t.Errorf("mensaje = %q, quiero que contenga %q", pgErr.Message, "permission denied for table tokens_restablecimiento")
	}
}
