//go:build integration

package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// crearDesafioDeVerificacion siembra la credencial, crea el desafío y devuelve su código en claro.
func crearDesafioDeVerificacion(t *testing.T, pool *pgxpool.Pool, gestor *GestorDesafios, canal *canalOTPFalso, proposito Proposito) (Desafio, credencialDeDesafio, string) {
	t.Helper()

	cred := sembrarCredencialDeDesafio(t, pool, true)
	desafio, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    proposito,
		Celular:      cred.celular,
		Contexto:     "web",
		Origen:       "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("CrearDesafio: %v", err)
	}
	return desafio, cred, canal.ultimoTexto
}

// codigoDistinto devuelve un código de seis dígitos diferente al correcto.
func codigoDistinto(correcto string) string {
	if correcto == "000000" {
		return "111111"
	}
	return "000000"
}

// vencerDesafio mueve expira_en al pasado para ejercitar la rama de vencimiento.
func vencerDesafio(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()

	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE auth.desafios SET expira_en = now() - interval '1 second' WHERE id = $1", pgUUID(id))
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("vencer el desafío: %v", err)
	}
}

// leerIntentosDesafio lee como rol_auth el contador de intentos de un desafío.
func leerIntentosDesafio(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()

	var intentos int
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, "SELECT intentos FROM auth.desafios WHERE id = $1", pgUUID(id)).Scan(&intentos)
		}); err != nil {
		t.Fatalf("leer los intentos: %v", err)
	}
	return intentos
}

func TestVerificarDesafioCorrectoVerificaUnaVezYBloqueaElReuso(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})
	desafio, cred, codigo := crearDesafioDeVerificacion(t, pool, gestor, canal, PropositoLogin)

	resultado, err := gestor.VerificarDesafio(t.Context(), desafio.ID, codigo, PropositoLogin)
	if err != nil {
		t.Fatalf("VerificarDesafio: %v", err)
	}
	if resultado.ParteID != cred.parteID || resultado.CredencialID != cred.credencialID {
		t.Errorf("resultado = %+v, quiero parte %s y credencial %s", resultado, cred.parteID, cred.credencialID)
	}
	if resultado.Proposito != PropositoLogin || resultado.Contexto != "web" || resultado.CelularDestino != cred.celular {
		t.Errorf("resultado = %+v, quiero login/web/%s", resultado, cred.celular)
	}

	if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, codigo, PropositoLogin); !errors.Is(err, ErrDesafioInvalido) {
		t.Fatalf("segundo uso = %v, quiero ErrDesafioInvalido", err)
	}
	if estado := leerFilaDesafio(t, pool, desafio.ID).estado; estado != "verificado" {
		t.Errorf("estado = %q, quiero verificado", estado)
	}
	if total := contarEventosDeParte(t, pool, cred.parteID, TipoEventoVerificacion, resultadoVerificacionOk); total != 1 {
		t.Errorf("eventos verificacion/ok = %d, quiero 1", total)
	}
}

func TestVerificarDesafioTresFallosInvalidanYElCuartoDaDemasiadosIntentos(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})
	desafio, cred, codigo := crearDesafioDeVerificacion(t, pool, gestor, canal, PropositoLogin)
	malo := codigoDistinto(codigo)

	for intento := 1; intento <= 3; intento++ {
		if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, malo, PropositoLogin); !errors.Is(err, ErrCodigoInvalido) {
			t.Fatalf("intento %d = %v, quiero ErrCodigoInvalido", intento, err)
		}
	}
	if estado := leerFilaDesafio(t, pool, desafio.ID).estado; estado != "invalidado" {
		t.Errorf("estado = %q, quiero invalidado", estado)
	}
	if intentos := leerIntentosDesafio(t, pool, desafio.ID); intentos != 3 {
		t.Errorf("intentos = %d, quiero 3", intentos)
	}
	if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, codigo, PropositoLogin); !errors.Is(err, ErrDemasiadosIntentos) {
		t.Fatalf("cuarto intento = %v, quiero ErrDemasiadosIntentos", err)
	}
	if total := contarEventosDeParte(t, pool, cred.parteID, TipoEventoVerificacion, resultadoVerificacionCodigoInvalido); total != 3 {
		t.Errorf("eventos verificacion/codigo_invalido = %d, quiero 3", total)
	}
}

func TestVerificarDesafioVencidoDevuelveExpirado(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})
	desafio, cred, codigo := crearDesafioDeVerificacion(t, pool, gestor, canal, PropositoLogin)
	vencerDesafio(t, pool, desafio.ID)

	if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, codigo, PropositoLogin); !errors.Is(err, ErrCodigoExpirado) {
		t.Fatalf("vencido = %v, quiero ErrCodigoExpirado", err)
	}
	if estado := leerFilaDesafio(t, pool, desafio.ID).estado; estado != "expirado" {
		t.Errorf("estado = %q, quiero expirado", estado)
	}
	if total := contarEventosDeParte(t, pool, cred.parteID, TipoEventoVerificacion, resultadoVerificacionExpirado); total != 1 {
		t.Errorf("eventos verificacion/expirado = %d, quiero 1", total)
	}
}

func TestVerificarDesafioDeLoginPresentadoComoRecuperacion(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})
	desafio, _, codigo := crearDesafioDeVerificacion(t, pool, gestor, canal, PropositoLogin)

	if _, err := gestor.VerificarDesafio(t.Context(), desafio.ID, codigo, PropositoRecuperacion); !errors.Is(err, ErrDesafioInvalido) {
		t.Fatalf("propósito distinto = %v, quiero ErrDesafioInvalido", err)
	}
	if estado := leerFilaDesafio(t, pool, desafio.ID).estado; estado != "pendiente" {
		t.Errorf("estado = %q, quiero pendiente", estado)
	}
}

func TestVerificarDesafioConcurrenteVerificaUnaSolaVez(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})
	desafio, cred, codigo := crearDesafioDeVerificacion(t, pool, gestor, canal, PropositoLogin)

	type salida struct {
		resultado ResultadoDesafio
		err       error
	}
	inicio := make(chan struct{})
	salidas := make(chan salida, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-inicio
			resultado, err := gestor.VerificarDesafio(context.Background(), desafio.ID, codigo, PropositoLogin)
			salidas <- salida{resultado: resultado, err: err}
		})
	}
	close(inicio)
	wg.Wait()
	close(salidas)

	var exitos, invalidos int
	for res := range salidas {
		switch {
		case res.err == nil:
			exitos++
		case errors.Is(res.err, ErrDesafioInvalido):
			invalidos++
		default:
			t.Errorf("error inesperado en la carrera: %v", res.err)
		}
	}
	if exitos != 1 || invalidos != 1 {
		t.Fatalf("carrera = %d exitos/%d invalidos, quiero exactamente 1/1", exitos, invalidos)
	}
	if estado := leerFilaDesafio(t, pool, desafio.ID).estado; estado != "verificado" {
		t.Errorf("estado = %q, quiero verificado", estado)
	}
	if intentos := leerIntentosDesafio(t, pool, desafio.ID); intentos != 1 {
		t.Errorf("intentos = %d, quiero 1: el perdedor no incrementa porque el ganador ya dejó la fila en verificado", intentos)
	}
	if total := contarEventosDeParte(t, pool, cred.parteID, TipoEventoVerificacion, resultadoVerificacionOk); total != 1 {
		t.Errorf("eventos verificacion/ok = %d, quiero 1", total)
	}
}

func TestVerificarDesafioEnConsumeElIntentoConLaTransaccionDelLlamador(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalOTPFalso{}
	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})
	desafio, _, codigo := crearDesafioDeVerificacion(t, pool, gestor, canal, PropositoLogin)
	malo := codigoDistinto(codigo)

	_, errRollback := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			_, err := gestor.VerificarDesafioEn(ctx, q, desafio.ID, malo, PropositoLogin)
			return struct{}{}, err
		})
	if !errors.Is(errRollback, ErrCodigoInvalido) {
		t.Fatalf("VerificarDesafioEn = %v, quiero ErrCodigoInvalido", errRollback)
	}
	if intentos := leerIntentosDesafio(t, pool, desafio.ID); intentos != 0 {
		t.Errorf("intentos tras revertir = %d, quiero 0: el llamador decide si confirma", intentos)
	}

	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			_, _ = gestor.VerificarDesafioEn(ctx, q, desafio.ID, malo, PropositoLogin)
			return struct{}{}, nil
		}); err != nil {
		t.Fatalf("confirmar el intento: %v", err)
	}
	if intentos := leerIntentosDesafio(t, pool, desafio.ID); intentos != 1 {
		t.Errorf("intentos tras confirmar = %d, quiero 1", intentos)
	}
}
