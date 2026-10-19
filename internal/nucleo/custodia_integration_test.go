//go:build integration

package nucleo_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/custodiosim"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var secuenciaCorte atomic.Int64

// corteDePrueba devuelve un corte único por prueba para no chocar con la PK de extracto_custodio.
func corteDePrueba() time.Time {
	return time.Now().UTC().Add(-time.Duration(secuenciaCorte.Add(1)) * time.Millisecond).Truncate(time.Microsecond)
}

// servidorDeExtracto sirve el contrato del simulador con el saldo pedido y el corte pedido.
func servidorDeExtracto(t *testing.T, corte time.Time, saldo int64) *httptest.Server {
	t.Helper()

	servidor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		cuerpo := struct {
			CorteEn       time.Time       `json:"corte_en"`
			SaldoCentavos nucleo.Centavos `json:"saldo_centavos"`
			Origen        string          `json:"origen"`
		}{CorteEn: corte, SaldoCentavos: nucleo.Centavos(saldo), Origen: "simulador-custodia"}
		_ = json.NewEncoder(w).Encode(cuerpo)
	}))
	t.Cleanup(servidor.Close)
	return servidor
}

// servicioDeCustodia arma el núcleo con el lector apuntando a la URL dada.
func servicioDeCustodia(pool *pgxpool.Pool, url string) *nucleo.Servicio {
	return nucleo.NewServicio(pool, nucleo.KYCNoDisponible{}, nucleo.RielNoDisponible{},
		custodiosim.NuevoLectorExtracto(url+"/extracto", nil), nil)
}

// asentarCustodia escribe en el ledger un asiento cuadrado de prueba contra la cuenta ómnibus.
func asentarCustodia(t *testing.T, pool *pgxpool.Pool, monto int64, ocurridoEn time.Time) {
	t.Helper()

	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción de asiento: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}
	var id int64
	if err := tx.QueryRow(ctx,
		"INSERT INTO nucleo.journal_entry (tipo, ocurrido_en) VALUES ('prueba_custodia', $1) RETURNING id",
		ocurridoEn.UTC()).Scan(&id); err != nil {
		t.Fatalf("insertar asiento de custodia: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO nucleo.posting (entry_id, cuenta_id, monto, moneda)
		 VALUES ($1, 'activo:custodia:banco', $2, 'COP'), ($1, 'pasivo:gmf_por_pagar', $3, 'COP')`,
		id, monto, -monto); err != nil {
		t.Fatalf("insertar líneas del asiento de custodia: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("confirmar asiento de custodia: %v", err)
	}
}

// saldoDeCustodiaHasta replica la suma del Apéndice C para calcular el lado interno esperado.
func saldoDeCustodiaHasta(t *testing.T, pool *pgxpool.Pool, corte time.Time) int64 {
	t.Helper()

	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción de suma: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}
	var saldo int64
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(p.monto), 0)::bigint
		   FROM nucleo.posting p
		   JOIN nucleo.journal_entry j ON j.id = p.entry_id
		   JOIN nucleo.cuenta c ON c.id = p.cuenta_id
		  WHERE p.cuenta_id = 'activo:custodia:banco'
		    AND p.moneda = c.moneda
		    AND j.ocurrido_en <= $1`, corte.UTC()).Scan(&saldo); err != nil {
		t.Fatalf("sumar la custodia hasta el corte: %v", err)
	}
	return saldo
}

// registrarYComparar persiste el extracto y devuelve la comparación al mismo corte.
func registrarYComparar(t *testing.T, servicio *nucleo.Servicio, corte time.Time) nucleo.ResultadoCustodia {
	t.Helper()

	ctx := t.Context()
	if _, err := servicio.RegistrarExtractoCustodio(ctx, corte); err != nil {
		t.Fatalf("RegistrarExtractoCustodio: %v", err)
	}
	resultado, err := servicio.CompararCustodia(ctx, corte)
	if err != nil {
		t.Fatalf("CompararCustodia: %v", err)
	}
	return resultado
}

func TestHU_NF_09_Criterio15_2LaComparacionDaCeroCuandoLosDosLadosCoinciden(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	corte := corteDePrueba()
	asentarCustodia(t, pool, 123456, corte.Add(-time.Minute))
	interno := saldoDeCustodiaHasta(t, pool, corte)

	servidor := servidorDeExtracto(t, corte, interno)
	resultado := registrarYComparar(t, servicioDeCustodia(pool, servidor.URL), corte)

	if !resultado.Cuadra() {
		t.Errorf("Cuadra() = false con diferencia %d, quiero true", resultado.Diferencia)
	}
	if resultado.Diferencia != 0 {
		t.Errorf("Diferencia = %d, quiero 0", resultado.Diferencia)
	}
	if resultado.MontoInterno != nucleo.Centavos(interno) {
		t.Errorf("MontoInterno = %d, quiero %d", resultado.MontoInterno, interno)
	}
	if resultado.MontoExterno != nucleo.Centavos(interno) {
		t.Errorf("MontoExterno = %d, quiero %d", resultado.MontoExterno, interno)
	}
	if !resultado.CorteEn.Equal(corte) {
		t.Errorf("CorteEn = %v, quiero %v", resultado.CorteEn, corte)
	}
}

func TestHU_NF_09_Criterio15_1DetectaUnDepositoAsentadoSinNotificarAlSimulador(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	corte := corteDePrueba()
	base := saldoDeCustodiaHasta(t, pool, corte)
	asentarCustodia(t, pool, 500000, corte.Add(-time.Minute))
	interno := saldoDeCustodiaHasta(t, pool, corte)

	servidor := servidorDeExtracto(t, corte, base)
	resultado := registrarYComparar(t, servicioDeCustodia(pool, servidor.URL), corte)

	if resultado.Cuadra() {
		t.Fatal("Cuadra() = true, quiero false: el simulador no recibió el depósito")
	}
	if resultado.MontoInterno != nucleo.Centavos(interno) || resultado.MontoExterno != nucleo.Centavos(base) {
		t.Errorf("montos = interno %d, externo %d, quiero interno %d, externo %d",
			resultado.MontoInterno, resultado.MontoExterno, interno, base)
	}
	if resultado.Diferencia != nucleo.Centavos(base-interno) {
		t.Errorf("Diferencia = %d, quiero %d (externo menos interno)", resultado.Diferencia, base-interno)
	}
}

func TestHU_NF_09_Criterio15_1DetectaUnMovimientoDelSimuladorSinAsientoEnElLedger(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	corte := corteDePrueba()
	interno := saldoDeCustodiaHasta(t, pool, corte)
	externo := interno + 700000

	servidor := servidorDeExtracto(t, corte, externo)
	resultado := registrarYComparar(t, servicioDeCustodia(pool, servidor.URL), corte)

	if resultado.Cuadra() {
		t.Fatal("Cuadra() = true, quiero false: el simulador registró un movimiento que el ledger no asentó")
	}
	if resultado.Diferencia != nucleo.Centavos(externo-interno) {
		t.Errorf("Diferencia = %d, quiero %d", resultado.Diferencia, externo-interno)
	}
	if resultado.MontoInterno != nucleo.Centavos(interno) || resultado.MontoExterno != nucleo.Centavos(externo) {
		t.Errorf("montos = interno %d, externo %d, quiero interno %d, externo %d",
			resultado.MontoInterno, resultado.MontoExterno, interno, externo)
	}
}

func TestHU_NF_09_Criterio14SimuladorCaidoNoGuardaNiFabricaDescuadre(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	corte := corteDePrueba()
	antes := contarExtractos(t, pool)

	servidor := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	servidor.Close()
	servicio := servicioDeCustodia(pool, servidor.URL)

	if _, err := servicio.RegistrarExtractoCustodio(t.Context(), corte); err == nil {
		t.Fatal("RegistrarExtractoCustodio con el simulador caído = nil, quiero error para reintentar")
	}
	if _, err := servicio.CompararCustodia(t.Context(), corte); !errors.Is(err, nucleo.ErrExtractoDeCustodiaAusente) {
		t.Fatalf("CompararCustodia sin extracto = %v, quiero %v", err, nucleo.ErrExtractoDeCustodiaAusente)
	}
	if despues := contarExtractos(t, pool); despues != antes {
		t.Errorf("filas en extracto_custodio = %d, quiero %d (sin simulador no se escribe nada)", despues, antes)
	}
}

func TestHU_NF_09_Criterio15_1ElCorteAcotaLosDosLadosAlMismoInstante(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	corte := corteDePrueba()
	base := saldoDeCustodiaHasta(t, pool, corte)
	asentarCustodia(t, pool, 300000, corte.Add(-time.Minute))
	asentarCustodia(t, pool, 999999, corte.Add(time.Hour))
	interno := saldoDeCustodiaHasta(t, pool, corte)

	servidor := servidorDeExtracto(t, corte, interno)
	resultado := registrarYComparar(t, servicioDeCustodia(pool, servidor.URL), corte)

	if base+300000 != interno {
		t.Fatalf("la suma al corte = %d, quiero %d (el movimiento posterior no debe contar)", interno, base+300000)
	}
	if !resultado.Cuadra() {
		t.Errorf("Cuadra() = false con el movimiento posterior al corte, diferencia %d", resultado.Diferencia)
	}
	if resultado.MontoInterno != nucleo.Centavos(base+300000) {
		t.Errorf("MontoInterno = %d, quiero %d", resultado.MontoInterno, base+300000)
	}
}

// notificarDeposito manda una notificación válida al simulador de custodia.
func notificarDeposito(t *testing.T, base, referencia string, monto int64, ocurrido time.Time) {
	t.Helper()

	cuerpo := fmt.Sprintf(`{"referencia":%q,"monto_centavos":%q,"ocurrido_en":%q}`,
		referencia, strconv.FormatInt(monto, 10), ocurrido.UTC().Format(time.RFC3339Nano))
	respuesta, err := http.Post(base+"/notificaciones/depositos", "application/json", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("notificar al simulador: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode != http.StatusOK {
		datos, _ := io.ReadAll(respuesta.Body)
		t.Fatalf("notificar al simulador: status = %d, quiero 200 (%s)", respuesta.StatusCode, datos)
	}
}

// ponerPerillas cambia una perilla del simulador por su endpoint de control.
func ponerPerillas(t *testing.T, base, cuerpo string) {
	t.Helper()

	peticion, err := http.NewRequest(http.MethodPut, base+"/control/perillas", strings.NewReader(cuerpo))
	if err != nil {
		t.Fatalf("armar cambio de perillas: %v", err)
	}
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := http.DefaultClient.Do(peticion)
	if err != nil {
		t.Fatalf("cambiar perillas: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	if respuesta.StatusCode != http.StatusOK {
		datos, _ := io.ReadAll(respuesta.Body)
		t.Fatalf("cambiar perillas: status = %d (%s)", respuesta.StatusCode, datos)
	}
}

// diferenciaBase lee los dos lados por fuera de CompararCustodia para fijar la línea base del escenario.
func diferenciaBase(t *testing.T, pool *pgxpool.Pool, base string, corte time.Time) int64 {
	t.Helper()

	externo, err := custodiosim.NuevoLectorExtracto(base+"/extracto", nil).Extracto(t.Context(), corte)
	if err != nil {
		t.Fatalf("leer el extracto del simulador real: %v", err)
	}
	return externo.Saldo.Int64() - saldoDeCustodiaHasta(t, pool, corte)
}

func TestHU_NF_09_Criterio15ContraElSimuladorReal(t *testing.T) {
	base := os.Getenv("SIMULADOR_CUSTODIA_URL")
	if base == "" {
		t.Skip("SIMULADOR_CUSTODIA_URL no definida: la prueba necesita el simulador de custodia corriendo")
	}
	pool := abrirPoolDeIntegracion(t)
	servicio := servicioDeCustodia(pool, base)

	t.Run("sano: los dos lados coinciden", func(t *testing.T) {
		corteAntes := time.Now().UTC()
		time.Sleep(2 * time.Millisecond)
		ocurrido := time.Now().UTC()
		antes := diferenciaBase(t, pool, base, corteAntes)

		monto := int64(123456)
		notificarDeposito(t, base, "e2e-sano-"+uuid.NewString(), monto, ocurrido)
		asentarCustodia(t, pool, monto, ocurrido)

		time.Sleep(2 * time.Millisecond)
		resultado := registrarYComparar(t, servicio, time.Now().UTC())
		if resultado.Diferencia != nucleo.Centavos(antes) {
			t.Errorf("Diferencia = %d, quiero %d (los dos lados suman lo mismo)", resultado.Diferencia, antes)
		}
	})

	t.Run("notificacion perdida: el ledger asienta y el simulador no", func(t *testing.T) {
		corteAntes := time.Now().UTC()
		time.Sleep(2 * time.Millisecond)
		ocurrido := time.Now().UTC()
		antes := diferenciaBase(t, pool, base, corteAntes)

		monto := int64(234567)
		ponerPerillas(t, base, `{"p_notificacion_perdida":1}`)
		notificarDeposito(t, base, "e2e-perdida-"+uuid.NewString(), monto, ocurrido)
		ponerPerillas(t, base, `{"p_notificacion_perdida":0}`)
		asentarCustodia(t, pool, monto, ocurrido)

		time.Sleep(2 * time.Millisecond)
		resultado := registrarYComparar(t, servicio, time.Now().UTC())
		if resultado.Diferencia != nucleo.Centavos(antes-monto) {
			t.Errorf("Diferencia = %d, quiero %d (el externo quedó corto en el depósito perdido)", resultado.Diferencia, antes-monto)
		}
	})

	t.Run("inverso: el simulador registra y el ledger no asienta", func(t *testing.T) {
		corteAntes := time.Now().UTC()
		time.Sleep(2 * time.Millisecond)
		ocurrido := time.Now().UTC()
		antes := diferenciaBase(t, pool, base, corteAntes)

		monto := int64(345678)
		notificarDeposito(t, base, "e2e-inverso-"+uuid.NewString(), monto, ocurrido)

		time.Sleep(2 * time.Millisecond)
		resultado := registrarYComparar(t, servicio, time.Now().UTC())
		if resultado.Diferencia != nucleo.Centavos(antes+monto) {
			t.Errorf("Diferencia = %d, quiero %d (el interno quedó corto en el movimiento externo)", resultado.Diferencia, antes+monto)
		}
	})

	t.Run("movimiento posterior al corte no altera la comparacion", func(t *testing.T) {
		corteAntes := time.Now().UTC()
		time.Sleep(2 * time.Millisecond)
		corte := time.Now().UTC()
		antes := diferenciaBase(t, pool, base, corteAntes)

		futuro := corte.Add(time.Minute)
		monto := int64(456789)
		notificarDeposito(t, base, "e2e-futuro-"+uuid.NewString(), monto, futuro)
		asentarCustodia(t, pool, monto, futuro)

		resultado := registrarYComparar(t, servicio, corte)
		if resultado.Diferencia != nucleo.Centavos(antes) {
			t.Errorf("Diferencia = %d, quiero %d (el movimiento posterior al corte no cuenta)", resultado.Diferencia, antes)
		}
	})

	t.Run("simulador caido no guarda ni fabrica descuadre", func(t *testing.T) {
		antes := contarExtractos(t, pool)
		caido := servicioDeCustodia(pool, "http://127.0.0.1:1")
		if _, err := caido.RegistrarExtractoCustodio(t.Context(), time.Now().UTC()); err == nil {
			t.Fatal("RegistrarExtractoCustodio con el simulador caído = nil, quiero error")
		}
		if despues := contarExtractos(t, pool); despues != antes {
			t.Errorf("filas en extracto_custodio = %d, quiero %d", despues, antes)
		}
	})
}
