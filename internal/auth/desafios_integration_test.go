//go:build integration

package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pepperDeDesafio es el OTP_PEPPER con el que los tests construyen el gestor.
var pepperDeDesafio = bytes.Repeat([]byte{0x22}, 32)

// credencialDeDesafio reúne la parte, la credencial y el celular sembrados para un desafío.
type credencialDeDesafio struct {
	parteID      uuid.UUID
	credencialID uuid.UUID
	celular      string
}

// filaDesafio es lo que el test lee de la fila persistida del desafío.
type filaDesafio struct {
	estado   string
	creadoEn time.Time
	expiraEn time.Time
	hmac     []byte
}

// optInFijo devuelve un opt-in constante para probar la compuerta de consentimiento.
type optInFijo struct{ vigente bool }

func (o optInFijo) OptInVigente(context.Context, uuid.UUID) (bool, error) { return o.vigente, nil }

// sembrarCredencialDeDesafio inserta parte y credencial confirmadas y marca el celular si se pide.
func sembrarCredencialDeDesafio(tb testing.TB, pool *pgxpool.Pool, verificado bool) credencialDeDesafio {
	tb.Helper()

	parteID := uuid.New()
	credencialID := uuid.New()
	celular := "+57300" + strings.ReplaceAll(uuid.NewString(), "-", "")[:7]
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(parteID), Tipo: "persona"}); err != nil {
				return struct{}{}, err
			}
			_, err := q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
				ID:            pgUUID(credencialID),
				ParteID:       pgUUID(parteID),
				Correo:        uuid.NewString() + "@centinela.test",
				Celular:       celular,
				ValorDerivado: pgtype.Text{String: "$argon2id$hash", Valid: true},
				Estado:        "activo",
			})
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("sembrar la credencial del desafío: %v", err)
	}
	if verificado {
		marcarCelularVerificado(tb, pool, credencialID)
	}
	return credencialDeDesafio{parteID: parteID, credencialID: credencialID, celular: celular}
}

// marcarCelularVerificado fija celular_verificado_en para que el login tenga destino verificado.
func marcarCelularVerificado(tb testing.TB, pool *pgxpool.Pool, credencialID uuid.UUID) {
	tb.Helper()

	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE auth.credenciales SET celular_verificado_en = now() WHERE id = $1", pgUUID(credencialID))
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("marcar el celular verificado: %v", err)
	}
}

// gestorDeDesafio construye el servicio con el OTP_PEPPER de prueba.
func gestorDeDesafio(tb testing.TB, pool *pgxpool.Pool, canal CanalOTP, optIn LectorOptIn) *GestorDesafios {
	tb.Helper()

	gestor, err := NuevoGestorDesafios(pool, canal, NuevoRegistradorEventos(pool), optIn, pepperDeDesafio)
	if err != nil {
		tb.Fatalf("NuevoGestorDesafios: %v", err)
	}
	return gestor
}

// leerFilaDesafio lee como rol_auth el estado, la vigencia y el HMAC de un desafío.
func leerFilaDesafio(tb testing.TB, pool *pgxpool.Pool, id uuid.UUID) filaDesafio {
	tb.Helper()

	var fila filaDesafio
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT estado, creado_en, expira_en, codigo_hmac FROM auth.desafios WHERE id = $1`, pgUUID(id)).
				Scan(&fila.estado, &fila.creadoEn, &fila.expiraEn, &fila.hmac)
		}); err != nil {
		tb.Fatalf("leer el desafío %s: %v", id, err)
	}
	return fila
}

// contarDesafiosDeParte cuenta como rol_auth los desafíos de una parte.
func contarDesafiosDeParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) int {
	tb.Helper()

	var total int
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `SELECT count(*) FROM auth.desafios WHERE parte_id = $1`, pgUUID(parteID)).Scan(&total)
		}); err != nil {
		tb.Fatalf("contar desafíos: %v", err)
	}
	return total
}

// estadoUltimoDesafioDeParte devuelve el estado del desafío más reciente de la parte.
func estadoUltimoDesafioDeParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) string {
	tb.Helper()

	var estado string
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT estado FROM auth.desafios WHERE parte_id = $1 ORDER BY creado_en DESC LIMIT 1`, pgUUID(parteID)).Scan(&estado)
		}); err != nil {
		tb.Fatalf("leer el último desafío: %v", err)
	}
	return estado
}

// contarEventosDeParte cuenta los eventos de un tipo y resultado para la parte.
func contarEventosDeParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID, tipo, resultado string) int {
	tb.Helper()

	var total int
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT count(*) FROM auth.eventos_autenticacion WHERE actor_parte_id = $1 AND tipo = $2 AND resultado = $3`,
				pgUUID(parteID), tipo, resultado).Scan(&total)
		}); err != nil {
		tb.Fatalf("contar eventos: %v", err)
	}
	return total
}

func TestCrearDesafioDeLoginConCelularVerificado(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	cred := sembrarCredencialDeDesafio(t, pool, true)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})

	desafio, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoLogin,
		Celular:      cred.celular,
		Origen:       "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("CrearDesafio: %v", err)
	}
	if desafio.ExpiraEnSegundos != 300 {
		t.Errorf("ExpiraEnSegundos = %d, quiero 300", desafio.ExpiraEnSegundos)
	}
	if desafio.Proposito != PropositoLogin {
		t.Errorf("Proposito = %q, quiero %q", desafio.Proposito, PropositoLogin)
	}
	if canal.ultimoCelular != cred.celular {
		t.Errorf("el canal recibió %q, quiero %q", canal.ultimoCelular, cred.celular)
	}

	codigo := canal.ultimoTexto
	if len(codigo) != longitudCodigo {
		t.Fatalf("el canal recibió %q, quiero %d dígitos", codigo, longitudCodigo)
	}
	fila := leerFilaDesafio(t, pool, desafio.ID)
	if fila.estado != "pendiente" {
		t.Errorf("estado = %q, quiero pendiente", fila.estado)
	}
	if fila.expiraEn.Sub(fila.creadoEn) != vigenciaDesafio {
		t.Errorf("expira_en - creado_en = %s, quiero %s", fila.expiraEn.Sub(fila.creadoEn), vigenciaDesafio)
	}
	esperado := hmacDeCodigo(pepperDeDesafio, codigo)
	if !bytes.Equal(fila.hmac, esperado) {
		t.Error("el codigo_hmac no es el HMAC del código entregado")
	}
	if bytes.Contains(fila.hmac, []byte(codigo)) {
		t.Error("el codigo_hmac contiene el código en claro")
	}
	if contarEventosDeParte(t, pool, cred.parteID, TipoEventoDesafio, resultadoDesafioCreado) != 1 {
		t.Error("no quedó el evento desafio/creado")
	}
}

func TestCrearDesafioLoginSinCelularVerificado(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	cred := sembrarCredencialDeDesafio(t, pool, false)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})

	_, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoLogin,
		Celular:      cred.celular,
	})
	if !errors.Is(err, ErrCelularSinVerificar) {
		t.Fatalf("err = %v, quiero ErrCelularSinVerificar", err)
	}
	if canal.ultimoTexto != "" {
		t.Error("se envió el código a un celular sin verificar")
	}
	if total := contarDesafiosDeParte(t, pool, cred.parteID); total != 0 {
		t.Errorf("desafíos persistidos = %d, quiero 0", total)
	}
}

func TestCrearDesafioConCanalQueFalla(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	cred := sembrarCredencialDeDesafio(t, pool, true)
	canal := &canalOTPFalso{err: errors.New("proveedor caído")}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})

	_, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoLogin,
		Celular:      cred.celular,
	})
	if !errors.Is(err, ErrEntregaFallida) {
		t.Fatalf("err = %v, quiero ErrEntregaFallida", err)
	}
	if estado := estadoUltimoDesafioDeParte(t, pool, cred.parteID); estado != "invalidado" {
		t.Errorf("estado del desafío = %q, quiero invalidado", estado)
	}
	if contarEventosDeParte(t, pool, cred.parteID, TipoEventoDesafio, resultadoEntregaFallida) != 1 {
		t.Error("no quedó el evento desafio/entrega_fallida")
	}
}

func TestCrearDesafioOptInNoVigente(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	cred := sembrarCredencialDeDesafio(t, pool, true)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, optInFijo{vigente: false})

	_, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoLogin,
		Celular:      cred.celular,
	})
	if !errors.Is(err, ErrConsentimientoRequerido) {
		t.Fatalf("err = %v, quiero ErrConsentimientoRequerido", err)
	}
	if canal.ultimoTexto != "" {
		t.Error("se envió el código sin opt-in vigente")
	}
	if total := contarDesafiosDeParte(t, pool, cred.parteID); total != 0 {
		t.Errorf("desafíos persistidos = %d, quiero 0", total)
	}
}

func TestDesafioVencidoSeReportaExpiradoPorLaConsulta(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	cred := sembrarCredencialDeDesafio(t, pool, true)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})

	desafio, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoRecuperacion,
		Celular:      cred.celular,
	})
	if err != nil {
		t.Fatalf("CrearDesafio: %v", err)
	}
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE auth.desafios SET expira_en = now() - interval '1 second' WHERE id = $1", pgUUID(desafio.ID))
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("vencer el desafío: %v", err)
	}

	fila, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.DesafioPendientePorIDRow, error) {
			return q.DesafioPendientePorID(ctx, pgUUID(desafio.ID))
		})
	if err != nil {
		t.Fatalf("DesafioPendientePorID: %v", err)
	}
	if !fila.Expirado {
		t.Error("el desafío con expira_en en el pasado no se reporta como expirado")
	}
}

func TestCrearDesafioNoLogueaElCodigo(t *testing.T) {
	capturador := &capturadorDeLogs{}
	previo := slog.Default()
	slog.SetDefault(slog.New(capturador))
	t.Cleanup(func() { slog.SetDefault(previo) })

	pool := abrirPoolDeAuth(t)
	cred := sembrarCredencialDeDesafio(t, pool, true)
	canal := &canalOTPLogueador{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})

	if _, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoLogin,
		Celular:      cred.celular,
		Origen:       "127.0.0.1",
	}); err != nil {
		t.Fatalf("CrearDesafio: %v", err)
	}

	codigo := canal.canalOTPFalso.ultimoTexto
	if len(codigo) != longitudCodigo {
		t.Fatalf("el canal no recibió un código de %d dígitos: %q", longitudCodigo, codigo)
	}
	registros := capturador.contenido()
	if len(registros) == 0 {
		t.Fatal("no se emitió ningún registro: la prueba de fuga sería vacía")
	}
	if strings.Contains(string(registros), codigo) {
		t.Error("el servicio o el canal loguearon el código OTP")
	}
}
