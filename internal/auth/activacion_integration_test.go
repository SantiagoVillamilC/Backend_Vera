//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// filaCodigoRespaldo es lo que el test lee de una fila de auth.codigos_respaldo.
type filaCodigoRespaldo struct {
	hmac  []byte
	usado bool
}

// sembrarPendienteDeActivacion inserta parte y credencial en pendiente_verificacion.
func sembrarPendienteDeActivacion(tb testing.TB, pool *pgxpool.Pool) credencialDeDesafio {
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
				Estado:        string(EstadoPendienteVerificacion),
			})
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("sembrar la credencial pendiente: %v", err)
	}
	return credencialDeDesafio{parteID: parteID, credencialID: credencialID, celular: celular}
}

// activadorDePruebaReal arma el activador con el gestor de desafios y estados reales.
func activadorDePruebaReal(t *testing.T, pool *pgxpool.Pool, canal CanalOTP) (*ActivadorDeCuenta, *GestorDesafios) {
	t.Helper()

	gestor := gestorDeDesafio(t, pool, canal, SiempreOptIn{})
	activador, err := NuevoActivadorDeCuenta(pool, gestor, NuevoGestorDeEstados(NuevoRegistradorEventos(pool)), pepperDeDesafio)
	if err != nil {
		t.Fatalf("NuevoActivadorDeCuenta: %v", err)
	}
	return activador, gestor
}

// desafioDeActivacion crea el desafio de registro de una credencial pendiente.
func desafioDeActivacion(t *testing.T, gestor *GestorDesafios, cred credencialDeDesafio) Desafio {
	t.Helper()

	desafio, err := gestor.CrearDesafio(t.Context(), Solicitud{
		ParteID:      cred.parteID,
		CredencialID: cred.credencialID,
		Proposito:    PropositoRegistro,
		Celular:      cred.celular,
		Origen:       "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("CrearDesafio de registro: %v", err)
	}
	return desafio
}

// leerCodigosRespaldo lee como rol_auth los HMAC y el uso de los codigos de la credencial.
func leerCodigosRespaldo(t *testing.T, pool *pgxpool.Pool, credencialID uuid.UUID) []filaCodigoRespaldo {
	t.Helper()

	var filas []filaCodigoRespaldo
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx,
				"SELECT codigo_hmac, usado_en IS NOT NULL FROM auth.codigos_respaldo WHERE credencial_id = $1", pgUUID(credencialID))
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var fila filaCodigoRespaldo
				if err := rows.Scan(&fila.hmac, &fila.usado); err != nil {
					return struct{}{}, err
				}
				filas = append(filas, fila)
			}
			return struct{}{}, rows.Err()
		}); err != nil {
		t.Fatalf("leer los codigos de respaldo: %v", err)
	}
	return filas
}

// celularVerificadoDeCredencial reporta si la credencial tiene el sello de verificacion.
func celularVerificadoDeCredencial(t *testing.T, pool *pgxpool.Pool, credencialID uuid.UUID) bool {
	t.Helper()

	var verificado bool
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT celular_verificado_en IS NOT NULL FROM auth.credenciales WHERE id = $1", pgUUID(credencialID)).Scan(&verificado)
		}); err != nil {
		t.Fatalf("leer celular_verificado_en: %v", err)
	}
	return verificado
}

// contarEventosDeCredencial cuenta los eventos de un tipo y resultado para la credencial.
func contarEventosDeCredencial(t *testing.T, pool *pgxpool.Pool, credencialID uuid.UUID, tipo, resultado string) int {
	t.Helper()

	var total int
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				"SELECT count(*) FROM auth.eventos_autenticacion WHERE credencial_id = $1 AND tipo = $2 AND resultado = $3",
				pgUUID(credencialID), tipo, resultado).Scan(&total)
		}); err != nil {
		t.Fatalf("contar eventos de la credencial: %v", err)
	}
	return total
}

// envejecerCredencial mueve creada_en al pasado para que el job la considere vencida.
func envejecerCredencial(t *testing.T, pool *pgxpool.Pool, credencialID uuid.UUID, antiguedad time.Duration) {
	t.Helper()

	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx,
				"UPDATE auth.credenciales SET creada_en = now() - ($1::double precision * interval '1 second') WHERE id = $2",
				antiguedad.Seconds(), pgUUID(credencialID))
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("envejecer la credencial: %v", err)
	}
}

// exigirCodigosPersistidos comprueba 10 HMAC distintos, ninguno con el codigo en claro.
func exigirCodigosPersistidos(t *testing.T, filas []filaCodigoRespaldo, codigos []string) {
	t.Helper()

	if len(filas) != cantidadCodigosRespaldo {
		t.Fatalf("filas en codigos_respaldo = %d, quiero %d", len(filas), cantidadCodigosRespaldo)
	}
	esperados := make(map[string]bool, len(codigos))
	for _, codigo := range codigos {
		esperados[hex.EncodeToString(hmacDeCodigoRespaldo(pepperDeDesafio, codigo))] = true
	}
	vistos := make(map[string]bool, len(filas))
	for _, fila := range filas {
		if fila.usado {
			t.Error("un codigo recien emitido aparece como usado")
		}
		huella := hex.EncodeToString(fila.hmac)
		if !esperados[huella] {
			t.Errorf("HMAC %s no corresponde a ningun codigo devuelto", huella)
		}
		vistos[huella] = true
		for _, codigo := range codigos {
			if bytes.Contains(fila.hmac, []byte(normalizarCodigoRespaldo(codigo))) {
				t.Errorf("el HMAC de la fila contiene el codigo %q en claro", codigo)
			}
		}
	}
	if len(vistos) != cantidadCodigosRespaldo {
		t.Errorf("HMAC distintos = %d, quiero %d", len(vistos), cantidadCodigosRespaldo)
	}
}

func TestActivarCuentaVerificaActivaYEmiteCodigos(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalConFijo{codigo: "123456"}
	activador, gestor := activadorDePruebaReal(t, pool, canal)
	cred := sembrarPendienteDeActivacion(t, pool)
	desafio := desafioDeActivacion(t, gestor, cred)

	activacion, err := activador.ActivarCuenta(t.Context(), cred.celular, "123456")
	if err != nil {
		t.Fatalf("ActivarCuenta: %v", err)
	}
	if activacion.ParteID != cred.parteID {
		t.Errorf("ParteID = %s, quiero %s", activacion.ParteID, cred.parteID)
	}
	if activacion.Celular != cred.celular {
		t.Errorf("Celular = %q, quiero %q", activacion.Celular, cred.celular)
	}
	if len(activacion.CodigosRespaldo) != cantidadCodigosRespaldo {
		t.Fatalf("codigos devueltos = %d, quiero %d", len(activacion.CodigosRespaldo), cantidadCodigosRespaldo)
	}

	if estado := leerEstado(t, pool, cred.credencialID); estado != string(EstadoActivo) {
		t.Errorf("estado = %q, quiero %q", estado, EstadoActivo)
	}
	if !celularVerificadoDeCredencial(t, pool, cred.credencialID) {
		t.Error("celular_verificado_en quedo nulo tras la activacion")
	}
	anterior, nuevo, motivo := leerUltimaTransicion(t, pool, cred.credencialID)
	if anterior != string(EstadoPendienteVerificacion) || nuevo != string(EstadoActivo) || motivo != motivoCelularVerificado {
		t.Errorf("transicion = (%q, %q, %q), quiero (%q, %q, %q)", anterior, nuevo, motivo,
			EstadoPendienteVerificacion, EstadoActivo, motivoCelularVerificado)
	}
	if estado := leerFilaDesafio(t, pool, desafio.ID).estado; estado != "verificado" {
		t.Errorf("estado del desafio = %q, quiero verificado", estado)
	}
	if total := contarEventosDeParte(t, pool, cred.parteID, TipoEventoVerificacion, resultadoVerificacionOk); total != 1 {
		t.Errorf("eventos verificacion/ok = %d, quiero 1", total)
	}
	if total := contarEventosDeCredencial(t, pool, cred.credencialID, TipoEventoVerificacion, string(EstadoActivo)); total != 1 {
		t.Errorf("eventos verificacion/activo = %d, quiero 1", total)
	}
	exigirCodigosPersistidos(t, leerCodigosRespaldo(t, pool, cred.credencialID), activacion.CodigosRespaldo)
}

func TestActivarCuentaConElDesafioYaUsadoEsInvalido(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalConFijo{codigo: "123456"}
	activador, gestor := activadorDePruebaReal(t, pool, canal)
	cred := sembrarPendienteDeActivacion(t, pool)
	desafioDeActivacion(t, gestor, cred)

	if _, err := activador.ActivarCuenta(t.Context(), cred.celular, "123456"); err != nil {
		t.Fatalf("primera activacion: %v", err)
	}
	if _, err := activador.ActivarCuenta(t.Context(), cred.celular, "123456"); !errors.Is(err, ErrDesafioInvalido) {
		t.Fatalf("segunda activacion = %v, quiero ErrDesafioInvalido", err)
	}
	if estado := leerEstado(t, pool, cred.credencialID); estado != string(EstadoActivo) {
		t.Errorf("estado = %q, quiero activo", estado)
	}
	if total := len(leerCodigosRespaldo(t, pool, cred.credencialID)); total != cantidadCodigosRespaldo {
		t.Errorf("codigos tras la segunda activacion = %d, quiero %d", total, cantidadCodigosRespaldo)
	}
}

func TestActivarCuentaConCodigosMalosInvalidaElDesafio(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalConFijo{codigo: "123456"}
	activador, gestor := activadorDePruebaReal(t, pool, canal)
	cred := sembrarPendienteDeActivacion(t, pool)
	desafio := desafioDeActivacion(t, gestor, cred)

	for intento := 1; intento <= 3; intento++ {
		if _, err := activador.ActivarCuenta(t.Context(), cred.celular, "000000"); !errors.Is(err, ErrCodigoInvalido) {
			t.Fatalf("intento %d = %v, quiero ErrCodigoInvalido", intento, err)
		}
	}
	if _, err := activador.ActivarCuenta(t.Context(), cred.celular, "000000"); !errors.Is(err, ErrDemasiadosIntentos) {
		t.Fatalf("cuarto intento = %v, quiero ErrDemasiadosIntentos", err)
	}
	if estado := leerFilaDesafio(t, pool, desafio.ID).estado; estado != "invalidado" {
		t.Errorf("estado del desafio = %q, quiero invalidado", estado)
	}
	if estado := leerEstado(t, pool, cred.credencialID); estado != string(EstadoPendienteVerificacion) {
		t.Errorf("estado = %q, quiero pendiente_verificacion", estado)
	}
	if total := len(leerCodigosRespaldo(t, pool, cred.credencialID)); total != 0 {
		t.Errorf("codigos emitidos tras los intentos malos = %d, quiero 0", total)
	}
	if total := contarEventosDeParte(t, pool, cred.parteID, TipoEventoVerificacion, resultadoVerificacionOk); total != 0 {
		t.Errorf("eventos verificacion/ok = %d, quiero 0", total)
	}
}

func TestActivarCuentaConDesafioVencidoRespondeExpirado(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalConFijo{codigo: "123456"}
	activador, gestor := activadorDePruebaReal(t, pool, canal)
	cred := sembrarPendienteDeActivacion(t, pool)
	desafio := desafioDeActivacion(t, gestor, cred)
	vencerDesafio(t, pool, desafio.ID)

	if _, err := activador.ActivarCuenta(t.Context(), cred.celular, "123456"); !errors.Is(err, ErrCodigoExpirado) {
		t.Fatalf("desafio vencido = %v, quiero ErrCodigoExpirado", err)
	}
	if estado := leerEstado(t, pool, cred.credencialID); estado != string(EstadoPendienteVerificacion) {
		t.Errorf("estado = %q, quiero pendiente_verificacion", estado)
	}
	if total := len(leerCodigosRespaldo(t, pool, cred.credencialID)); total != 0 {
		t.Errorf("codigos emitidos sobre el desafio vencido = %d, quiero 0", total)
	}
}

func TestActivarCuentaRecortaElCelularAntesDelMatch(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalConFijo{codigo: "123456"}
	activador, gestor := activadorDePruebaReal(t, pool, canal)
	cred := sembrarPendienteDeActivacion(t, pool)
	desafioDeActivacion(t, gestor, cred)

	activacion, err := activador.ActivarCuenta(t.Context(), " "+cred.celular+" ", "123456")
	if err != nil {
		t.Fatalf("ActivarCuenta con espacios: %v", err)
	}
	if activacion.Celular != cred.celular {
		t.Errorf("Celular = %q, quiero %q", activacion.Celular, cred.celular)
	}
	if estado := leerEstado(t, pool, cred.credencialID); estado != string(EstadoActivo) {
		t.Errorf("estado = %q, quiero activo", estado)
	}
}

func TestDosActivacionesConcurrentesDejanUnSoloDesenlace(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	canal := &canalConFijo{codigo: "123456"}
	activador, gestor := activadorDePruebaReal(t, pool, canal)
	cred := sembrarPendienteDeActivacion(t, pool)
	desafioDeActivacion(t, gestor, cred)

	resultados := make([]error, 2)
	var wg sync.WaitGroup
	for i := range resultados {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, resultados[i] = activador.ActivarCuenta(context.Background(), cred.celular, "123456")
		}()
	}
	wg.Wait()

	ganadas := 0
	for _, err := range resultados {
		switch {
		case err == nil:
			ganadas++
		case errors.Is(err, ErrDesafioInvalido), errors.Is(err, ErrConflictoDeEstado):
		default:
			t.Errorf("error inesperado de la activacion concurrente: %v", err)
		}
	}
	if ganadas != 1 {
		t.Errorf("activaciones ganadoras = %d, quiero 1", ganadas)
	}
	if estado := leerEstado(t, pool, cred.credencialID); estado != string(EstadoActivo) {
		t.Errorf("estado = %q, quiero activo", estado)
	}
	if !celularVerificadoDeCredencial(t, pool, cred.credencialID) {
		t.Error("celular_verificado_en quedo nulo tras la activacion")
	}
	if total := contarTransiciones(t, pool, cred.credencialID); total != 1 {
		t.Errorf("transiciones = %d, quiero 1", total)
	}
	if total := len(leerCodigosRespaldo(t, pool, cred.credencialID)); total != cantidadCodigosRespaldo {
		t.Errorf("codigos emitidos = %d, quiero %d, una sola emision", total, cantidadCodigosRespaldo)
	}
}

func TestCarreraLimpiezaContraActivacionDejaUnSoloDesenlace(t *testing.T) {
	pool := abrirPoolDeAuthConcurrente(t)
	canal := &canalConFijo{codigo: "123456"}
	activador, gestor := activadorDePruebaReal(t, pool, canal)
	cred := sembrarPendienteDeActivacion(t, pool)
	envejecerCredencial(t, pool, cred.credencialID, 2*time.Hour)
	desafioDeActivacion(t, gestor, cred)
	estados := NuevoGestorDeEstados(NuevoRegistradorEventos(pool))

	var wg sync.WaitGroup
	var errActivacion, errJob error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errActivacion = activador.ActivarCuenta(context.Background(), cred.celular, "123456")
	}()
	go func() {
		defer wg.Done()
		_, errJob = estados.CerrarPendientesVencidas(context.Background(), pool, time.Hour)
	}()
	wg.Wait()

	if errJob != nil {
		t.Errorf("error del job = %v", errJob)
	}
	if errActivacion != nil && !errors.Is(errActivacion, ErrConflictoDeEstado) {
		t.Errorf("error de la activacion = %v", errActivacion)
	}
	estado := leerEstado(t, pool, cred.credencialID)
	if estado != string(EstadoActivo) && estado != string(EstadoCerrado) {
		t.Errorf("estado final = %q, quiero activo o cerrado", estado)
	}
	if total := contarTransiciones(t, pool, cred.credencialID); total != 1 {
		t.Errorf("transiciones = %d, quiero 1", total)
	}
}
