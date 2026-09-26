//go:build integration

package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func cuerpoDeConfirmacion(referencia, monto, moneda string) string {
	return cuerpoDeConfirmacionConEstado(referencia, "aprobado", monto, moneda)
}

func cuerpoDeConfirmacionConEstado(referencia, estado, monto, moneda string) string {
	ocurrido := time.Now().UTC().Format(time.RFC3339Nano)
	cuerpo := `{"referencia":"` + referencia + `","estado":"` + estado + `","monto":"` + monto + `"`
	if moneda != "" {
		cuerpo += `,"moneda":"` + moneda + `"`
	}
	return cuerpo + `,"ocurrido_en":"` + ocurrido + `"}`
}

func cuerpoDeConfirmacionConCUS(referencia, monto, cus string) string {
	return `{"referencia":"` + referencia + `","estado":"aprobado","monto":"` + monto + `","cus":"` + cus + `","ocurrido_en":"` +
		time.Now().UTC().Format(time.RFC3339Nano) + `"}`
}

func cusDelDeposito(t *testing.T, pool *pgxpool.Pool, depositoID uuid.UUID) *string {
	t.Helper()

	var cus *string
	enNucleoDeIntegracion(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT cus FROM nucleo.deposito WHERE id = $1", depositoID).Scan(&cus)
	})
	return cus
}

func sembrarIntencion(t *testing.T, s *nucleo.Servicio, usuarioID string, monto int64) nucleo.IntencionDeposito {
	t.Helper()

	intencion, _, err := s.CrearIntencionDeposito(t.Context(), usuarioID, nucleo.Centavos(monto), "COP", "k-"+uuid.NewString())
	if err != nil {
		t.Fatalf("crear la intención: %v", err)
	}
	return intencion
}

func enNucleoDeIntegracion(t *testing.T, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()

	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+nucleo.RolNucleo); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := fn(ctx, tx); err != nil {
		t.Fatalf("consulta: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func estadoDelDeposito(t *testing.T, pool *pgxpool.Pool, depositoID uuid.UUID) string {
	t.Helper()

	var estado string
	enNucleoDeIntegracion(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT estado FROM nucleo.deposito WHERE id = $1", depositoID).Scan(&estado)
	})
	return estado
}

func marcarEstadoDelDeposito(t *testing.T, pool *pgxpool.Pool, depositoID uuid.UUID, estado string) {
	t.Helper()

	enNucleoDeIntegracion(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "UPDATE nucleo.deposito SET estado = $2, actualizada_en = now() WHERE id = $1", depositoID, estado)
		return err
	})
}

func contarAsientoDelDeposito(t *testing.T, pool *pgxpool.Pool, depositoID uuid.UUID) (asientos, lineas, suma int64) {
	t.Helper()

	enNucleoDeIntegracion(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(DISTINCT je.id), count(p.id), COALESCE(SUM(p.monto), 0)::bigint
			  FROM nucleo.journal_entry je LEFT JOIN nucleo.posting p ON p.entry_id = je.id
			 WHERE je.deposito_id = $1`, depositoID).Scan(&asientos, &lineas, &suma)
	})
	return asientos, lineas, suma
}

func filasDescuadradas(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()

	var filas int64
	enNucleoDeIntegracion(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM (
			SELECT entry_id FROM nucleo.posting GROUP BY entry_id HAVING SUM(monto) <> 0) x`).Scan(&filas)
	})
	return filas
}

func sumaDeCuenta(t *testing.T, pool *pgxpool.Pool, cuentaID string) int64 {
	t.Helper()

	var suma int64
	enNucleoDeIntegracion(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COALESCE(SUM(monto), 0)::bigint FROM nucleo.posting WHERE cuenta_id = $1`, cuentaID).Scan(&suma)
	})
	return suma
}

// HU-NF-07 Criterio 6: una confirmación válida asienta el fondeo con dos líneas que suman cero.
func TestHU_NF_07_Criterio6_ConfirmacionAsientaElFondeo(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	intencion := sembrarIntencion(t, servicio, usuarioID, 250000)

	rec := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "250000", ""), "Bearer "+tokenCustodioDePrueba)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	data := datosDeDeposito(t, rec)
	if data["estado"] != nucleo.EstadoDepositoConfirmada {
		t.Errorf("estado = %v, quiero %q", data["estado"], nucleo.EstadoDepositoConfirmada)
	}
	if data["id"] != intencion.ID.String() {
		t.Errorf("id = %v, quiero %s", data["id"], intencion.ID)
	}
	if data["monto"] != "250000" || data["moneda"] != "COP" {
		t.Errorf("monto/moneda = %v/%v, quiero 250000/COP", data["monto"], data["moneda"])
	}

	if estado := estadoDelDeposito(t, pool, intencion.ID); estado != nucleo.EstadoDepositoConfirmada {
		t.Errorf("estado en base = %q, quiero confirmada", estado)
	}
	if asientos, lineas, suma := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 1 || lineas != 2 || suma != 0 {
		t.Fatalf("asiento = %d con %d líneas y suma %d, quiero 1, 2 y 0", asientos, lineas, suma)
	}
	if filas := filasDescuadradas(t, pool); filas != 0 {
		t.Errorf("el Apéndice C devolvió %d asientos descuadrados, quiero 0", filas)
	}
}

// HU-NF-07 Criterio 22: el CUS que devuelve el flujo de PSE se persiste, sin exponerse en la respuesta.
func TestHU_NF_07_Criterio22_CUSPersistidoSinExponerlo(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	intencion := sembrarIntencion(t, servicio, usuarioID, 100000)
	cus := "cus-" + uuid.NewString()

	rec := pedirConfirmacion(t, r, cuerpoDeConfirmacionConCUS(intencion.Referencia, "100000", cus), "Bearer "+tokenCustodioDePrueba)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	persistido := cusDelDeposito(t, pool, intencion.ID)
	if persistido == nil || *persistido != cus {
		t.Errorf("deposito.cus = %v, quiero %q", persistido, cus)
	}
	if _, expuesto := datosDeDeposito(t, rec)["cus"]; expuesto {
		t.Error("RespuestaDeposito no debe exponer cus")
	}
	if asientos, _, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 1 {
		t.Errorf("asientos = %d, quiero 1", asientos)
	}
}

// HU-NF-07 Criterio 22: la confirmación sin CUS deja la columna opcional en NULL.
func TestHU_NF_07_Criterio22_SinCUSLaColumnaQuedaNULL(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	intencion := sembrarIntencion(t, servicio, usuarioID, 100000)

	rec := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "100000", ""), "Bearer "+tokenCustodioDePrueba)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if persistido := cusDelDeposito(t, pool, intencion.ID); persistido != nil {
		t.Errorf("deposito.cus = %q, quiero NULL", *persistido)
	}
}

// HU-NF-07 Criterio 8: el depósito es el primer movimiento y deja el saldo igual al monto.
func TestHU_NF_07_Criterio8_PrimerMovimientoDejaElSaldoEnElMonto(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	intencion := sembrarIntencion(t, servicio, usuarioID, 275000)

	rec := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "275000", ""), "Bearer "+tokenCustodioDePrueba)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}

	saldo, err := servicio.Saldo(t.Context(), nucleo.NombreCuentaDisponible(usuarioID))
	if err != nil {
		t.Fatalf("consultar saldo: %v", err)
	}
	if saldo != nucleo.Centavos(275000) {
		t.Errorf("saldo = %d, quiero 275000", saldo)
	}
	if suma := sumaDeCuenta(t, pool, nucleo.NombreCuentaDisponible(usuarioID)); suma != -275000 {
		t.Errorf("suma de la subcuenta disponible = %d, quiero -275000", suma)
	}
}

// HU-NF-07 Criterio 4: una referencia desconocida es 404 y no acredita nada.
func TestHU_NF_07_Criterio4_ReferenciaDesconocidaNoAcredita(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{uuid.NewString()}})
	asientosAntes, lineasAntes := contarDiario(t, pool)

	rec := pedirConfirmacion(t, r, cuerpoDeConfirmacion("dep-"+uuid.NewString(), "1000", ""), "Bearer "+tokenCustodioDePrueba)
	assertErrorEnvelope(t, rec, http.StatusNotFound, "DepositNotFound")

	asientos, lineas := contarDiario(t, pool)
	if asientos != asientosAntes || lineas != lineasAntes {
		t.Errorf("el diario cambió: journal_entry %d->%d, posting %d->%d", asientosAntes, asientos, lineasAntes, lineas)
	}
}

// HU-NF-07 Criterio 10.1: un monto o una moneda distintos a los de la intención dan 409.
func TestHU_NF_07_Criterio10_1_MontoOMonedaDistintosDanConflicto(t *testing.T) {
	tests := map[string]struct {
		monto  string
		moneda string
	}{
		"monto distinto":  {monto: "999999"},
		"moneda distinta": {monto: "100000", moneda: "USD"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			pool := abrirPoolDeIntegracion(t)
			servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
			usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
			r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
			intencion := sembrarIntencion(t, servicio, usuarioID, 100000)

			rec := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, tc.monto, tc.moneda), "Bearer "+tokenCustodioDePrueba)
			assertErrorEnvelope(t, rec, http.StatusConflict, "ConfirmationConflict")

			if estado := estadoDelDeposito(t, pool, intencion.ID); estado != nucleo.EstadoDepositoPendiente {
				t.Errorf("estado = %q, quiero pendiente", estado)
			}
			if asientos, _, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 0 {
				t.Errorf("se escribieron %d asientos, quiero 0", asientos)
			}
		})
	}
}

// HU-NF-07 Criterio 10.2: sobre una intención ya confirmada, otro monto es conflicto, no reintento.
func TestHU_NF_07_Criterio10_2_ConfirmadaConMontoDistintoDanConflicto(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	intencion := sembrarIntencion(t, servicio, usuarioID, 120000)

	primera := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "120000", ""), "Bearer "+tokenCustodioDePrueba)
	if primera.Code != http.StatusOK {
		t.Fatalf("primera: status = %d, quiero 200 (cuerpo %q)", primera.Code, primera.Body.String())
	}

	segunda := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "999999", ""), "Bearer "+tokenCustodioDePrueba)
	assertErrorEnvelope(t, segunda, http.StatusConflict, "ConfirmationConflict")

	if estado := estadoDelDeposito(t, pool, intencion.ID); estado != nucleo.EstadoDepositoConfirmada {
		t.Errorf("estado = %q, quiero confirmada", estado)
	}
	if asientos, _, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 1 {
		t.Errorf("asientos = %d, quiero 1", asientos)
	}
}

// HU-NF-07 Criterio 9.1 y 9.2: un reenvío deja un solo asiento y devuelve el mismo resultado.
func TestHU_NF_07_Criterio9_ReenvioNoDuplicaYDevuelveLoMismo(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	intencion := sembrarIntencion(t, servicio, usuarioID, 160000)

	primera := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "160000", ""), "Bearer "+tokenCustodioDePrueba)
	if primera.Code != http.StatusOK {
		t.Fatalf("primera: status = %d, quiero 200 (cuerpo %q)", primera.Code, primera.Body.String())
	}

	segunda := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "160000", ""), "Bearer "+tokenCustodioDePrueba)
	if segunda.Code != http.StatusOK {
		t.Fatalf("reenvío: status = %d, quiero 200 (cuerpo %q)", segunda.Code, segunda.Body.String())
	}
	if primera.Body.String() != segunda.Body.String() {
		t.Errorf("el reenvío devolvió %q, quiero el mismo cuerpo %q", segunda.Body.String(), primera.Body.String())
	}
	if asientos, lineas, suma := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 1 || lineas != 2 || suma != 0 {
		t.Errorf("asiento = %d con %d líneas y suma %d, quiero 1, 2 y 0", asientos, lineas, suma)
	}
}

// HU-NF-07 Criterio 9.3: confirmaciones en paralelo dejan un solo asiento y la misma respuesta.
func TestHU_NF_07_Criterio9_3_ReenvioEnParalelo(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	intencion := sembrarIntencion(t, servicio, usuarioID, 150000)
	cuerpo := cuerpoDeConfirmacion(intencion.Referencia, "150000", "")

	const intentos = 6
	codigos := make(chan int, intentos)
	cuerpos := make(chan string, intentos)
	var wg sync.WaitGroup
	for range intentos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := pedirConfirmacion(t, r, cuerpo, "Bearer "+tokenCustodioDePrueba)
			codigos <- rec.Code
			cuerpos <- rec.Body.String()
		}()
	}
	wg.Wait()
	close(codigos)
	close(cuerpos)

	for codigo := range codigos {
		if codigo != http.StatusOK {
			t.Errorf("status = %d, quiero 200", codigo)
		}
	}
	primero := ""
	for c := range cuerpos {
		if primero == "" {
			primero = c
			continue
		}
		if c != primero {
			t.Errorf("cuerpo distinto: %q vs %q", c, primero)
		}
	}
	if asientos, _, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 1 {
		t.Fatalf("asientos = %d, quiero 1", asientos)
	}
}

// HU-NF-07 Criterio 7: una falla en la segunda escritura no deja nada y la intención sigue pendiente.
func TestHU_NF_07_Criterio7_FallaLaSegundaEscrituraNoDejaNada(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := uuid.NewString()
	intencion := sembrarIntencion(t, servicio, usuarioID, 100000)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	rec := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "100000", ""), "Bearer "+tokenCustodioDePrueba)
	assertErrorEnvelope(t, rec, http.StatusInternalServerError, "InternalError")

	if estado := estadoDelDeposito(t, pool, intencion.ID); estado != nucleo.EstadoDepositoPendiente {
		t.Errorf("estado = %q, quiero pendiente", estado)
	}
	if asientos, lineas, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 0 || lineas != 0 {
		t.Errorf("quedaron asiento=%d líneas=%d, quiero 0 y 0", asientos, lineas)
	}
}

// HU-NF-07 Criterio 19: una confirmación sobre una intención rechazada o expirada no asienta.
func TestHU_NF_07_Criterio19_ConfirmacionTardiaNoAsienta(t *testing.T) {
	for _, estado := range []string{nucleo.EstadoDepositoRechazada, nucleo.EstadoDepositoExpirada} {
		t.Run(estado, func(t *testing.T) {
			pool := abrirPoolDeIntegracion(t)
			servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
			usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
			r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
			intencion := sembrarIntencion(t, servicio, usuarioID, 100000)
			marcarEstadoDelDeposito(t, pool, intencion.ID, estado)

			rec := pedirConfirmacion(t, r, cuerpoDeConfirmacion(intencion.Referencia, "100000", ""), "Bearer "+tokenCustodioDePrueba)
			assertErrorEnvelope(t, rec, http.StatusConflict, "LateConfirmation")

			if actual := estadoDelDeposito(t, pool, intencion.ID); actual != estado {
				t.Errorf("estado = %q, quiero %q", actual, estado)
			}
			if asientos, _, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 0 {
				t.Errorf("se escribieron %d asientos, quiero 0", asientos)
			}
		})
	}
}

// HU-NF-07 Criterio 17: el rechazo explícito deja la intención rechazada y no escribe el diario.
func TestHU_NF_07_Criterio17_RechazoExplicitoNoAsienta(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
	intencion := sembrarIntencion(t, servicio, usuarioID, 100000)
	cuerpo := cuerpoDeConfirmacionConEstado(intencion.Referencia, "rechazado", "100000", "")

	rec := pedirConfirmacion(t, r, cuerpo, "Bearer "+tokenCustodioDePrueba)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if estado := datosDeDeposito(t, rec)["estado"]; estado != nucleo.EstadoDepositoRechazada {
		t.Errorf("estado = %v, quiero %q", estado, nucleo.EstadoDepositoRechazada)
	}
	if estado := estadoDelDeposito(t, pool, intencion.ID); estado != nucleo.EstadoDepositoRechazada {
		t.Errorf("estado en base = %q, quiero rechazada", estado)
	}
	if asientos, lineas, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 0 || lineas != 0 {
		t.Errorf("el rechazo escribió asiento=%d líneas=%d, quiero 0 y 0", asientos, lineas)
	}

	reenvio := pedirConfirmacion(t, r, cuerpo, "Bearer "+tokenCustodioDePrueba)
	if reenvio.Code != http.StatusOK {
		t.Fatalf("reenvío: status = %d, quiero 200 (cuerpo %q)", reenvio.Code, reenvio.Body.String())
	}
	if reenvio.Body.String() != rec.Body.String() {
		t.Errorf("el reenvío devolvió %q, quiero el mismo cuerpo %q", reenvio.Body.String(), rec.Body.String())
	}
	if asientos, lineas, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 0 || lineas != 0 {
		t.Errorf("el reenvío escribió asiento=%d líneas=%d, quiero 0 y 0", asientos, lineas)
	}
}

// HU-NF-07 Criterio 17: el rechazo no exige monto ni fuente y deja la intención rechazada.
func TestHU_NF_07_Criterio17_RechazoAceptaPayloadMinimo(t *testing.T) {
	cuerpos := map[string]string{
		"solo referencia y estado": `{"referencia":"%s","estado":"rechazado"}`,
		"con monto cero":           `{"referencia":"%s","estado":"rechazado","monto":"0"}`,
	}
	for nombre, plantilla := range cuerpos {
		t.Run(nombre, func(t *testing.T) {
			pool := abrirPoolDeIntegracion(t)
			servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
			usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
			r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
			intencion := sembrarIntencion(t, servicio, usuarioID, 100000)

			rec := pedirConfirmacion(t, r, fmt.Sprintf(plantilla, intencion.Referencia), "Bearer "+tokenCustodioDePrueba)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
			}
			if estado := datosDeDeposito(t, rec)["estado"]; estado != nucleo.EstadoDepositoRechazada {
				t.Errorf("estado = %v, quiero %q", estado, nucleo.EstadoDepositoRechazada)
			}
			if estado := estadoDelDeposito(t, pool, intencion.ID); estado != nucleo.EstadoDepositoRechazada {
				t.Errorf("estado en base = %q, quiero rechazada", estado)
			}
			if asientos, lineas, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != 0 || lineas != 0 {
				t.Errorf("el rechazo escribió asiento=%d líneas=%d, quiero 0 y 0", asientos, lineas)
			}
		})
	}
}

// HU-NF-07 Criterio 17: rechazar una intención confirmada o expirada da 409 y no escribe nada.
func TestHU_NF_07_Criterio17_RechazoNoPendienteDa409(t *testing.T) {
	tests := map[string]struct {
		estado string
		code   string
	}{
		"confirmada": {estado: nucleo.EstadoDepositoConfirmada, code: "ConfirmationConflict"},
		"expirada":   {estado: nucleo.EstadoDepositoExpirada, code: "LateConfirmation"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			pool := abrirPoolDeIntegracion(t)
			servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
			usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
			r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})
			intencion := sembrarIntencion(t, servicio, usuarioID, 100000)
			marcarEstadoDelDeposito(t, pool, intencion.ID, tc.estado)
			asientosAntes, lineasAntes, _ := contarAsientoDelDeposito(t, pool, intencion.ID)

			rec := pedirConfirmacion(t, r, cuerpoDeConfirmacionConEstado(intencion.Referencia, "rechazado", "100000", ""), "Bearer "+tokenCustodioDePrueba)
			assertErrorEnvelope(t, rec, http.StatusConflict, tc.code)

			if estado := estadoDelDeposito(t, pool, intencion.ID); estado != tc.estado {
				t.Errorf("estado = %q, quiero %q", estado, tc.estado)
			}
			if asientos, lineas, _ := contarAsientoDelDeposito(t, pool, intencion.ID); asientos != asientosAntes || lineas != lineasAntes {
				t.Errorf("el diario cambió: asientos %d->%d, líneas %d->%d", asientosAntes, asientos, lineasAntes, lineas)
			}
		})
	}
}
