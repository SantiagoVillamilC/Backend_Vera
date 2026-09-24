//go:build integration

package auth_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/otpsim"
	platformredis "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/redis"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// filaDeRecuperacion es lo que el test lee de la fila persistida del desafío de recuperación.
type filaDeRecuperacion struct {
	parteID      pgtype.UUID
	credencialID pgtype.UUID
	proposito    string
	estado       string
	celular      string
	intentos     int
	creadoEn     time.Time
	expiraEn     time.Time
}

// sembrarCredencialDeRecuperacion inserta parte, consentimiento y credencial con estado y verificación dados.
func sembrarCredencialDeRecuperacion(tb testing.TB, pool *pgxpool.Pool, estado string, verificado, conOptIn bool) (parteID, credencialID uuid.UUID, correo, celular string) {
	tb.Helper()

	parteID, credencialID = uuid.New(), uuid.New()
	correo = "a3-02-1-" + uuid.NewString() + "@centinela.test"
	celular = "+57300" + strings.ReplaceAll(uuid.NewString(), "-", "")[:7]
	porRolAuth(tb, pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "INSERT INTO auth.partes (id, tipo) VALUES ($1, 'persona')", pgtype.UUID{Bytes: parteID, Valid: true}); err != nil {
			return err
		}
		if conOptIn {
			if _, err := tx.Exec(ctx,
				`INSERT INTO auth.consentimientos (id, parte_id, tipo, version_texto, texto, aceptado, accion, origen, metodo)
				 VALUES ($1, $2, 'whatsapp_optin', 'v1', 'texto', true, 'otorgar', 'test', 'check')`,
				pgtype.UUID{Bytes: uuid.New(), Valid: true}, pgtype.UUID{Bytes: parteID, Valid: true}); err != nil {
				return err
			}
		}
		if verificado {
			_, err := tx.Exec(ctx,
				`INSERT INTO auth.credenciales (id, parte_id, correo, celular, valor_derivado, estado, celular_verificado_en)
				 VALUES ($1, $2, $3, $4, '$argon2id$prueba', $5, now())`,
				pgtype.UUID{Bytes: credencialID, Valid: true}, pgtype.UUID{Bytes: parteID, Valid: true}, correo, celular, estado)
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO auth.credenciales (id, parte_id, correo, celular, valor_derivado, estado)
			 VALUES ($1, $2, $3, $4, '$argon2id$prueba', $5)`,
			pgtype.UUID{Bytes: credencialID, Valid: true}, pgtype.UUID{Bytes: parteID, Valid: true}, correo, celular, estado)
		return err
	})
	return parteID, credencialID, correo, celular
}

// leerFilaDeRecuperacion lee como rol_auth la fila del desafío de recuperación.
func leerFilaDeRecuperacion(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) filaDeRecuperacion {
	tb.Helper()

	var fila filaDeRecuperacion
	porRolAuth(tb, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT parte_id, credencial_id, proposito, estado, celular_destino, intentos, creado_en, expira_en
			 FROM auth.desafios WHERE id = $1`, pgtype.UUID{Bytes: id, Valid: true}).
			Scan(&fila.parteID, &fila.credencialID, &fila.proposito, &fila.estado, &fila.celular, &fila.intentos, &fila.creadoEn, &fila.expiraEn)
	})
	return fila
}

// contarDesafiosDeLaParte cuenta como rol_auth los desafíos de una parte.
func contarDesafiosDeLaParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) int {
	tb.Helper()

	total := 0
	porRolAuth(tb, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM auth.desafios WHERE parte_id = $1`,
			pgtype.UUID{Bytes: parteID, Valid: true}).Scan(&total)
	})
	return total
}

// contarEventosDeRecuperacion cuenta los eventos de recuperación con un resultado dado.
func contarEventosDeRecuperacion(tb testing.TB, pool *pgxpool.Pool, resultado string) int {
	tb.Helper()

	total := 0
	porRolAuth(tb, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM auth.eventos_autenticacion WHERE tipo = 'recuperacion' AND resultado = $1`, resultado).Scan(&total)
	})
	return total
}

// servicioDeRecuperacionExterno arma el servicio real sobre Postgres y Redis.
func servicioDeRecuperacionExterno(tb testing.TB, pool *pgxpool.Pool, canal auth.CanalOTP, cliente *redis.Client) *auth.ServicioDeRecuperacion {
	tb.Helper()

	gestor := gestorDeRecuperacionExterno(tb, pool, canal, cliente)
	puerto, err := contadores.NuevoRedis(cliente)
	if err != nil {
		tb.Fatalf("NuevoRedis: %v", err)
	}
	servicio, err := auth.NuevoServicioDeRecuperacion(pool, gestor, auth.NuevoRegistradorEventos(pool), puerto, clavesExternas(tb), pepperDeReenvio)
	if err != nil {
		tb.Fatalf("NuevoServicioDeRecuperacion: %v", err)
	}
	return servicio
}

// gestorDeRecuperacionExterno arma el gestor con el lector de opt-in real de consentimientos.
func gestorDeRecuperacionExterno(tb testing.TB, pool *pgxpool.Pool, canal auth.CanalOTP, cliente *redis.Client) *auth.GestorDesafios {
	tb.Helper()

	puerto, err := contadores.NuevoRedis(cliente)
	if err != nil {
		tb.Fatalf("NuevoRedis: %v", err)
	}
	gestor, err := auth.NuevoGestorDesafios(pool, canal, auth.NuevoRegistradorEventos(pool), auth.NuevoLectorOptInDeConsentimientos(pool), pepperDeReenvio,
		auth.ConRacionamiento(puerto, clavesExternas(tb)))
	if err != nil {
		tb.Fatalf("NuevoGestorDesafios: %v", err)
	}
	return gestor
}

// contadoresSinTope anula los topes para medir tiempos sin bloquear la cuenta.
type contadoresSinTope struct{}

func (contadoresSinTope) Incrementar(context.Context, string, time.Duration) (int64, error) {
	return 1, nil
}

func (contadoresSinTope) Bloquear(context.Context, string, time.Duration) error { return nil }

func (contadoresSinTope) Bloqueada(context.Context, string) (bool, error) { return false, nil }

// simuladorDeRecuperacion arma el canal simulado con un código fijo de seis dígitos.
func simuladorDeRecuperacion(tb testing.TB) *otpsim.Simulador {
	tb.Helper()

	canal, err := otpsim.Nuevo("123456")
	if err != nil {
		tb.Fatalf("otpsim.Nuevo: %v", err)
	}
	return canal
}

func TestSolicitarRecuperacionCuentaActivaEnviaElCodigo(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := simuladorDeRecuperacion(t)
	parteID, _, correo, celular := sembrarCredencialDeRecuperacion(t, pool, "activo", true, true)
	servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)

	desafio, err := servicio.SolicitarRecuperacion(t.Context(), correo, "10.30.1.1")
	if err != nil {
		t.Fatalf("SolicitarRecuperacion: %v", err)
	}
	if desafio.Proposito != auth.PropositoRecuperacion || desafio.ExpiraEnSegundos != 300 {
		t.Errorf("desafío = %+v, quiero recuperación/300", desafio)
	}
	fila := leerFilaDeRecuperacion(t, pool, desafio.ID)
	if !fila.parteID.Valid || uuid.UUID(fila.parteID.Bytes) != parteID {
		t.Errorf("parte_id = %v, quiero %s", fila.parteID, parteID)
	}
	if fila.proposito != string(auth.PropositoRecuperacion) || fila.estado != "pendiente" {
		t.Errorf("proposito/estado = %s/%s, quiero recuperacion/pendiente", fila.proposito, fila.estado)
	}
	if fila.expiraEn.Sub(fila.creadoEn) != 5*time.Minute {
		t.Errorf("expira_en - creado_en = %s, quiero 5m", fila.expiraEn.Sub(fila.creadoEn))
	}
	codigo, ok := canal.UltimoCodigo(celular)
	if !ok || len(codigo) != 6 {
		t.Errorf("UltimoCodigo(%s) = (%q, %v), quiero seis dígitos", celular, codigo, ok)
	}
	if contarEventosDeRecuperacion(t, pool, "solicitud_enviada") == 0 {
		t.Error("no quedó el evento recuperacion/solicitud_enviada")
	}
}

func TestSolicitarRecuperacionCorreoInexistenteNoEnvia(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := simuladorDeRecuperacion(t)
	_, _, _, celularSembrado := sembrarCredencialDeRecuperacion(t, pool, "activo", true, true)
	servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)

	desafio, err := servicio.SolicitarRecuperacion(t.Context(), "no-existe-"+uuid.NewString()+"@centinela.test", "10.30.1.2")
	if err != nil {
		t.Fatalf("SolicitarRecuperacion: %v", err)
	}
	fila := leerFilaDeRecuperacion(t, pool, desafio.ID)
	if fila.parteID.Valid || fila.credencialID.Valid {
		t.Errorf("parte/credencial = %v/%v, quiero NULL y NULL", fila.parteID, fila.credencialID)
	}
	if fila.proposito != string(auth.PropositoRecuperacion) || fila.estado != "pendiente" {
		t.Errorf("proposito/estado = %s/%s, quiero recuperacion/pendiente", fila.proposito, fila.estado)
	}
	if _, ok := canal.UltimoCodigo(celularSembrado); ok {
		t.Error("el relleno envió un código a la cuenta sembrada")
	}
	if _, ok := canal.UltimoCodigo(fila.celular); ok {
		t.Error("el relleno envió un código a su celular de destino")
	}
	if contarEventosDeRecuperacion(t, pool, "solicitud_relleno") == 0 {
		t.Error("no quedó el evento recuperacion/solicitud_relleno")
	}
}

func TestSolicitarRecuperacionEstadosNoElegiblesCreanRelleno(t *testing.T) {
	casos := map[string]struct {
		estado     string
		verificado bool
	}{
		"pendiente_verificacion": {"pendiente_verificacion", true},
		"suspendido":             {"suspendido", true},
		"cerrado":                {"cerrado", true},
		"activo_sin_verificar":   {"activo", false},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			pool := abrirPoolExterno(t)
			cliente := abrirRedisExterno(t)
			canal := simuladorDeRecuperacion(t)
			_, _, correo, celular := sembrarCredencialDeRecuperacion(t, pool, caso.estado, caso.verificado, true)
			servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)

			desafio, err := servicio.SolicitarRecuperacion(t.Context(), correo, "10.30.1.3")
			if err != nil {
				t.Fatalf("SolicitarRecuperacion: %v", err)
			}
			fila := leerFilaDeRecuperacion(t, pool, desafio.ID)
			if fila.parteID.Valid || fila.credencialID.Valid {
				t.Errorf("parte/credencial = %v/%v, quiero fila de relleno", fila.parteID, fila.credencialID)
			}
			if _, ok := canal.UltimoCodigo(celular); ok {
				t.Error("un estado no elegible recibió un envío real")
			}
		})
	}
}

func TestSolicitarRecuperacionActivoSinOptInCreaRelleno(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := simuladorDeRecuperacion(t)
	_, _, correo, celular := sembrarCredencialDeRecuperacion(t, pool, "activo", true, false)
	servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)

	desafio, err := servicio.SolicitarRecuperacion(t.Context(), correo, "10.30.1.9")
	if err != nil {
		t.Fatalf("SolicitarRecuperacion: %v", err)
	}
	if fila := leerFilaDeRecuperacion(t, pool, desafio.ID); fila.parteID.Valid || fila.credencialID.Valid {
		t.Errorf("parte/credencial = %v/%v, quiero fila de relleno sin opt-in", fila.parteID, fila.credencialID)
	}
	if _, ok := canal.UltimoCodigo(celular); ok {
		t.Error("una cuenta sin opt-in recibió un envío real")
	}
}

func TestVerificarDesafioDeLoginNoSirveParaRecuperacion(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := simuladorDeRecuperacion(t)
	parteID, credencialID, _, celular := sembrarCredencialDeRecuperacion(t, pool, "activo", true, true)
	gestor := gestorReenvioExterno(t, pool, canal, cliente)

	desafio, err := gestor.CrearDesafio(t.Context(), auth.Solicitud{
		ParteID:      parteID,
		CredencialID: credencialID,
		Proposito:    auth.PropositoLogin,
		Celular:      celular,
		Origen:       "10.30.1.4",
	})
	if err != nil {
		t.Fatalf("CrearDesafio: %v", err)
	}
	codigo, _ := canal.UltimoCodigo(celular)

	if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, codigo, auth.PropositoRecuperacion); !errors.Is(err, auth.ErrDesafioInvalido) {
		t.Fatalf("cruce de propósito = %v, quiero ErrDesafioInvalido", err)
	}
}

func TestRellenoConsumeIntentosComoUnDesafioReal(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := simuladorDeRecuperacion(t)
	servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)

	relleno, err := servicio.SolicitarRecuperacion(t.Context(), "relleno-"+uuid.NewString()+"@centinela.test", "10.30.1.5")
	if err != nil {
		t.Fatalf("SolicitarRecuperacion relleno: %v", err)
	}

	gestor := gestorReenvioExterno(t, pool, canal, cliente)
	for intento := 1; intento <= 3; intento++ {
		if _, err := gestor.VerificarDesafio(t.Context(), relleno.ID, "000000", auth.PropositoRecuperacion); !errors.Is(err, auth.ErrCodigoInvalido) {
			t.Fatalf("intento %d sobre el relleno = %v, quiero ErrCodigoInvalido", intento, err)
		}
	}
	if fila := leerFilaDeRecuperacion(t, pool, relleno.ID); fila.estado != "invalidado" || fila.intentos != 3 {
		t.Errorf("relleno estado/intentos = %s/%d, quiero invalidado/3", fila.estado, fila.intentos)
	}
	if _, err := gestor.VerificarDesafio(t.Context(), relleno.ID, "000000", auth.PropositoRecuperacion); !errors.Is(err, auth.ErrDemasiadosIntentos) {
		t.Fatalf("cuarto intento sobre el relleno = %v, quiero ErrDemasiadosIntentos", err)
	}
}

func TestReenviarDesafioDeRellenoNoEnvia(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := simuladorDeRecuperacion(t)
	servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)

	relleno, err := servicio.SolicitarRecuperacion(t.Context(), "resend-"+uuid.NewString()+"@centinela.test", "10.30.1.6")
	if err != nil {
		t.Fatalf("SolicitarRecuperacion: %v", err)
	}
	fila := leerFilaDeRecuperacion(t, pool, relleno.ID)
	envejecerUltimoEnvio(t, pool, relleno.ID)

	nuevo, err := gestorReenvioExterno(t, pool, canal, cliente).ReenviarDesafio(t.Context(), relleno.ID, "10.30.1.6")
	if err != nil {
		t.Fatalf("ReenviarDesafio sobre el relleno: %v", err)
	}
	if nuevo.Proposito != auth.PropositoRecuperacion || nuevo.ExpiraEnSegundos != 300 {
		t.Errorf("reenvío = %+v, quiero recuperación/300", nuevo)
	}
	if _, ok := canal.UltimoCodigo(fila.celular); ok {
		t.Error("el reenvío del relleno envió un código")
	}
}

func TestSolicitarRecuperacionSextaBloqueaAmbosCaminos(t *testing.T) {
	pool := abrirPoolExterno(t)
	cliente := abrirRedisExterno(t)
	canal := simuladorDeRecuperacion(t)
	_, _, correoExistente, _ := sembrarCredencialDeRecuperacion(t, pool, "activo", true, true)
	servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)

	casos := map[string]string{
		"existente":   correoExistente,
		"inexistente": "tope-" + uuid.NewString() + "@centinela.test",
	}
	origen := "origen-" + uuid.NewString()
	for nombre, correo := range casos {
		t.Run(nombre, func(t *testing.T) {
			for i := 0; i < 5; i++ {
				if _, err := servicio.SolicitarRecuperacion(t.Context(), correo, origen); err != nil {
					t.Fatalf("solicitud %d: %v", i+1, err)
				}
			}
			if _, err := servicio.SolicitarRecuperacion(t.Context(), correo, origen); !errors.Is(err, auth.ErrDemasiadosEnvios) {
				t.Fatalf("sexta solicitud = %v, quiero ErrDemasiadosEnvios", err)
			}
		})
	}
}

func TestSolicitarRecuperacionFailClosedNoCreaFila(t *testing.T) {
	pool := abrirPoolExterno(t)
	parteID, _, correo, _ := sembrarCredencialDeRecuperacion(t, pool, "activo", true, true)
	cliente, err := platformredis.NuevoCliente("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatalf("cliente Redis caído: %v", err)
	}
	t.Cleanup(func() { _ = cliente.Close() })
	canal := simuladorDeRecuperacion(t)
	servicio := servicioDeRecuperacionExterno(t, pool, canal, cliente)
	antes := contarDesafiosDeLaParte(t, pool, parteID)

	if _, err := servicio.SolicitarRecuperacion(t.Context(), correo, "10.30.1.8"); !errors.Is(err, auth.ErrAuthNoDisponible) {
		t.Fatalf("fail closed = %v, quiero ErrAuthNoDisponible", err)
	}
	if despues := contarDesafiosDeLaParte(t, pool, parteID); despues != antes {
		t.Errorf("desafíos creados con Redis caído = %d, quiero %d", despues, antes)
	}
}

func TestSolicitarRecuperacionTiempos(t *testing.T) {
	pool := abrirPoolExterno(t)
	canal := simuladorDeRecuperacion(t)
	_, _, correo, _ := sembrarCredencialDeRecuperacion(t, pool, "activo", true, true)
	gestor, err := auth.NuevoGestorDesafios(pool, canal, auth.NuevoRegistradorEventos(pool), auth.SiempreOptIn{}, pepperDeReenvio)
	if err != nil {
		t.Fatalf("NuevoGestorDesafios: %v", err)
	}
	servicio, err := auth.NuevoServicioDeRecuperacion(pool, gestor, auth.NuevoRegistradorEventos(pool), contadoresSinTope{}, clavesExternas(t), pepperDeReenvio)
	if err != nil {
		t.Fatalf("NuevoServicioDeRecuperacion: %v", err)
	}

	real := medirMediana(t, 20, func() error {
		_, err := servicio.SolicitarRecuperacion(t.Context(), correo, "10.31.1.1")
		return err
	})
	relleno := medirMediana(t, 20, func() error {
		_, err := servicio.SolicitarRecuperacion(t.Context(), "tiempo-"+uuid.NewString()+"@centinela.test", "10.31.1.2")
		return err
	})
	t.Logf("medianas: real=%s relleno=%s", real, relleno)
	if real > relleno*10 && real-relleno > 5*time.Millisecond {
		t.Errorf("el camino real (%s) supera en un orden de magnitud al relleno (%s)", real, relleno)
	}
}

// medirMediana corre la operación n veces y devuelve la mediana de las duraciones.
func medirMediana(tb testing.TB, n int, operacion func() error) time.Duration {
	tb.Helper()

	duraciones := make([]time.Duration, 0, n)
	for range n {
		inicio := time.Now()
		if err := operacion(); err != nil {
			tb.Fatalf("operación de tiempo: %v", err)
		}
		duraciones = append(duraciones, time.Since(inicio))
	}
	sort.Slice(duraciones, func(i, j int) bool { return duraciones[i] < duraciones[j] })
	return duraciones[len(duraciones)/2]
}
