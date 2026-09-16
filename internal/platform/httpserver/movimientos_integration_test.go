//go:build integration

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type lineaConMotivo struct {
	monto  string
	motivo string
}

// sembrarAsientoDeHistorial registra un asiento completo con su tipo y su transferencia opcional.
func sembrarAsientoDeHistorial(t *testing.T, pool *pgxpool.Pool, tipo string, transferencia pgtype.UUID, ocurrido, registrado time.Time, lineas []linea) int64 {
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
	if _, err := tx.Exec(ctx, "SET LOCAL search_path = nucleo"); err != nil {
		t.Fatalf("search_path: %v", err)
	}

	var entryID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO journal_entry (tipo, transferencia_id, ocurrido_en, registrado_en)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		tipo, transferencia, ocurrido, registrado).Scan(&entryID); err != nil {
		t.Fatalf("insertar asiento: %v", err)
	}
	for _, l := range lineas {
		if _, err := tx.Exec(ctx,
			"INSERT INTO posting (entry_id, cuenta_id, monto, moneda) VALUES ($1, $2, $3, 'COP')",
			entryID, l.cuenta, l.monto); err != nil {
			t.Fatalf("insertar línea: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return entryID
}

// sembrarDeposito registra el depósito de un usuario: activo contra su subcuenta disponible.
func sembrarDeposito(t *testing.T, pool *pgxpool.Pool, usuarioID string, monto int64) int64 {
	t.Helper()
	ahora := time.Now().UTC()
	return sembrarAsientoDeHistorial(t, pool, nucleo.TipoAsientoDeposito, pgtype.UUID{}, ahora, ahora, []linea{
		{cuenta: nucleo.CuentaCustodiaBanco, monto: monto},
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: -monto},
	})
}

func transferenciaDe(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pedirMovimientos(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/movimientos"+query, nil)
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func movimientosDe(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()

	env := decodeEnvelope(t, rec)
	crudos, ok := env["data"].([]any)
	if !ok {
		t.Fatalf("data ausente o no es una lista: %v (cuerpo %q)", env["data"], rec.Body.String())
	}
	movimientos := make([]map[string]any, 0, len(crudos))
	for _, crudo := range crudos {
		m, ok := crudo.(map[string]any)
		if !ok {
			t.Fatalf("movimiento con forma rara: %v", crudo)
		}
		movimientos = append(movimientos, m)
	}
	return movimientos
}

func metaDeMovimientos(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	meta, ok := decodeEnvelope(t, rec)["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta ausente o con forma rara en %q", rec.Body.String())
	}
	return meta
}

func lineasDe(t *testing.T, movimiento map[string]any) []lineaConMotivo {
	t.Helper()

	crudas, ok := movimiento["lineas"].([]any)
	if !ok {
		t.Fatalf("lineas ausente o con forma rara en %v", movimiento)
	}
	lineas := make([]lineaConMotivo, 0, len(crudas))
	for _, cruda := range crudas {
		l, ok := cruda.(map[string]any)
		if !ok {
			t.Fatalf("línea con forma rara: %v", cruda)
		}
		monto, _ := l["monto"].(string)
		motivo, _ := l["motivo"].(string)
		lineas = append(lineas, lineaConMotivo{monto: monto, motivo: motivo})
	}
	return lineas
}

func entryIDDe(t *testing.T, movimiento map[string]any) int64 {
	t.Helper()

	id, ok := movimiento["entry_id"].(float64)
	if !ok {
		t.Fatalf("entry_id ausente o con forma rara en %v", movimiento)
	}
	return int64(id)
}

// HU-NF-05 Criterios 1 y 2: el historial propio se devuelve y apuntar al ajeno se rechaza.
func TestHU_NF_05_Criterio1y2_HistorialPropioYRechazoDeAjeno(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	propio := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	ajeno := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	sembrarDeposito(t, pool, propio, 100000)
	sembrarDeposito(t, pool, ajeno, 200000)
	r := NewRouter(pool, identidadFija{propio}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	if len(movimientos) != 1 {
		t.Fatalf("movimientos = %d, quiero solo el propio, no el del otro usuario", len(movimientos))
	}
	if got := lineasDe(t, movimientos[0]); len(got) != 1 || got[0].monto != "100000" {
		t.Fatalf("lineas = %+v, quiero el depósito propio de 100000", got)
	}

	rechazos := map[string]string{
		"usuario_id ajeno": "?usuario_id=" + ajeno,
		"cuenta_id ajena":  "?cuenta_id=pasivo%3Ausuario%3A" + ajeno + "%3Adisponible",
	}
	for name, query := range rechazos {
		t.Run(name, func(t *testing.T) {
			rec := pedirMovimientos(t, r, query)
			assertErrorEnvelope(t, rec, http.StatusBadRequest, "TargetNotAllowed")
			if env := decodeEnvelope(t, rec); env["data"] != nil {
				t.Errorf("data = %v, quiero null en el rechazo", env["data"])
			}
		})
	}
}

// HU-NF-05 Criterio 3: una cuenta sin movimientos devuelve una lista vacía, no un error.
func TestHU_NF_05_Criterio3_CuentaSinMovimientosListaVacia(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, identidadFija{usuarioID}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if movimientos := movimientosDe(t, rec); len(movimientos) != 0 {
		t.Fatalf("movimientos = %d, quiero lista vacía", len(movimientos))
	}
	meta := metaDeMovimientos(t, rec)
	if meta["has_more"] != false {
		t.Errorf("has_more = %v, quiero false", meta["has_more"])
	}
	if meta["next_cursor"] != nil {
		t.Errorf("next_cursor = %v, quiero null", meta["next_cursor"])
	}
}

// HU-NF-05 Criterio 4.1 y 4.6: el depósito con el tipo canónico que escribe el repo se muestra con motivo deposito.
func TestHU_NF_05_Criterio4_1_DepositoSeClasificaConElTipoCanonico(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	sembrarAsiento(t, pool, []linea{
		{cuenta: nucleo.CuentaCustodiaBanco, monto: 100000},
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: -100000},
	})
	r := NewRouter(pool, identidadFija{usuarioID}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	if len(movimientos) != 1 {
		t.Fatalf("movimientos = %d, quiero 1", len(movimientos))
	}
	got := lineasDe(t, movimientos[0])
	if len(got) != 1 || got[0].monto != "100000" || got[0].motivo != "deposito" {
		t.Fatalf("lineas = %+v, quiero una línea positiva con motivo deposito", got)
	}
}

// HU-NF-05 Criterio 4.3: un envío sin GMF aparece como una sola línea con un solo monto.
func TestHU_NF_05_Criterio4_3_EnvioSinGmfUnaSolaLinea(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	emisor := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	receptor := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	transferencia := uuid.New()
	ahora := time.Now().UTC()
	sembrarAsientoDeHistorial(t, pool, "retencion", transferenciaDe(transferencia), ahora, ahora, []linea{
		{cuenta: nucleo.NombreCuentaDisponible(emisor), monto: 500000},
		{cuenta: nucleo.NombreCuentaRetenido(emisor), monto: -500000},
	})
	sembrarAsientoDeHistorial(t, pool, "transferencia_interna", transferenciaDe(transferencia), ahora, ahora, []linea{
		{cuenta: nucleo.NombreCuentaRetenido(emisor), monto: 500000},
		{cuenta: nucleo.NombreCuentaDisponible(receptor), monto: -500000},
	})
	r := NewRouter(pool, identidadFija{emisor}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	if len(movimientos) != 1 {
		t.Fatalf("movimientos = %d, quiero 1 (los dos asientos son una sola operación)", len(movimientos))
	}
	lineas := lineasDe(t, movimientos[0])
	if len(lineas) != 1 || lineas[0].monto != "-500000" || lineas[0].motivo != "envio" {
		t.Fatalf("lineas = %+v, quiero una sola línea con el envío de -500000", lineas)
	}
}

// HU-NF-05 Criterio 4.4: un envío con GMF muestra dos líneas propias, el monto y el gravamen.
func TestHU_NF_05_Criterio4_4_EnvioConGmfDosLineas(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	emisor := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	transferencia := uuid.New()
	ahora := time.Now().UTC()
	sembrarAsientoDeHistorial(t, pool, "retencion", transferenciaDe(transferencia), ahora, ahora, []linea{
		{cuenta: nucleo.NombreCuentaDisponible(emisor), monto: 500000},
		{cuenta: nucleo.NombreCuentaDisponible(emisor), monto: 2000},
		{cuenta: nucleo.NombreCuentaRetenido(emisor), monto: -500000},
		{cuenta: nucleo.CuentaGMFPorPagar, monto: -2000},
	})
	r := NewRouter(pool, identidadFija{emisor}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	if len(movimientos) != 1 {
		t.Fatalf("movimientos = %d, quiero 1", len(movimientos))
	}
	got := lineasDe(t, movimientos[0])
	want := []lineaConMotivo{
		{monto: "-500000", motivo: "envio"},
		{monto: "-2000", motivo: "gravamen"},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("lineas = %+v, quiero %+v (monto y gravamen, sin combinarlas)", got, want)
	}
}

// HU-NF-05 Criterio 4.5: una transferencia compensada muestra salida, devolución y gravamen.
func TestHU_NF_05_Criterio4_5_CompensacionMuestraSalidaDevolucionYGravamen(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	emisor := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	transferencia := uuid.New()
	ahora := time.Now().UTC()
	sembrarAsientoDeHistorial(t, pool, "retencion", transferenciaDe(transferencia), ahora, ahora, []linea{
		{cuenta: nucleo.NombreCuentaDisponible(emisor), monto: 500000},
		{cuenta: nucleo.NombreCuentaDisponible(emisor), monto: 2000},
		{cuenta: nucleo.NombreCuentaRetenido(emisor), monto: -500000},
		{cuenta: nucleo.CuentaGMFPorPagar, monto: -2000},
	})
	sembrarAsientoDeHistorial(t, pool, "compensacion", transferenciaDe(transferencia), ahora, ahora, []linea{
		{cuenta: nucleo.NombreCuentaDisponible(emisor), monto: -500000},
		{cuenta: nucleo.NombreCuentaDisponible(emisor), monto: -2000},
		{cuenta: nucleo.CuentaLiquidacionPendienteRiel, monto: 500000},
		{cuenta: nucleo.CuentaGMFPorPagar, monto: 2000},
	})
	r := NewRouter(pool, identidadFija{emisor}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	if len(movimientos) != 1 {
		t.Fatalf("movimientos = %d, quiero 1 (los dos asientos son una sola operación)", len(movimientos))
	}
	got := lineasDe(t, movimientos[0])
	want := []lineaConMotivo{
		{monto: "-500000", motivo: "envio"},
		{monto: "-2000", motivo: "gravamen"},
		{monto: "500000", motivo: "devolucion"},
		{monto: "2000", motivo: "devolucion_gravamen"},
	}
	if len(got) != len(want) {
		t.Fatalf("lineas = %+v, quiero %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lineas = %+v, quiero %+v (no un neto de cero)", got, want)
		}
	}
}

// HU-NF-05 Criterio 8: orden del más reciente al más antiguo y estable con fechas iguales.
func TestHU_NF_05_Criterio8_OrdenRecienteYEstable(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	ahora := time.Now().UTC()
	primero := sembrarAsientoDeHistorial(t, pool, nucleo.TipoAsientoDeposito, pgtype.UUID{}, ahora, ahora, []linea{
		{cuenta: nucleo.CuentaCustodiaBanco, monto: 100000},
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: -100000},
	})
	segundo := sembrarAsientoDeHistorial(t, pool, nucleo.TipoAsientoDeposito, pgtype.UUID{}, ahora, ahora, []linea{
		{cuenta: nucleo.CuentaCustodiaBanco, monto: 200000},
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: -200000},
	})
	tercero := sembrarAsientoDeHistorial(t, pool, nucleo.TipoAsientoDeposito, pgtype.UUID{}, ahora, ahora, []linea{
		{cuenta: nucleo.CuentaCustodiaBanco, monto: 300000},
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: -300000},
	})
	r := NewRouter(pool, identidadFija{usuarioID}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	got := []int64{entryIDDe(t, movimientos[0]), entryIDDe(t, movimientos[1]), entryIDDe(t, movimientos[2])}
	want := []int64{tercero, segundo, primero}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orden = %v, quiero %v (más reciente primero, estable con fechas iguales)", got, want)
		}
	}
}

// HU-NF-05 Criterio 10.2: un movimiento insertado durante la paginación no repite ni pierde los ya confirmados.
func TestHU_NF_05_Criterio10_2_InsercionDuranteLaPaginacion(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	_, esperados := sembrarHistorialDePaginacion(t, pool, usuarioID)
	r := NewRouter(pool, identidadFija{usuarioID}, servicio, custodioDePrueba)

	pagina1, meta1 := consumirPaginaDeHistorial(t, r, "página 1", "?limit=2", 2, true, esperados)
	cursor := int64(meta1["next_cursor"].(float64))
	nuevo := sembrarDeposito(t, pool, usuarioID, 700000)
	if nuevo <= cursor {
		t.Fatalf("el movimiento insertado (%d) no quedó por encima del cursor (%d)", nuevo, cursor)
	}
	pagina2, meta2 := consumirPaginaDeHistorial(t, r, "página 2", "?limit=2&cursor="+itoa(cursor), 2, true, esperados)
	cursor2 := int64(meta2["next_cursor"].(float64))
	pagina3, _ := consumirPaginaDeHistorial(t, r, "página 3", "?limit=2&cursor="+itoa(cursor2), 1, false, esperados)

	if len(esperados) != 0 {
		t.Fatalf("quedaron %d movimientos sin paginar: la paginación los perdió", len(esperados))
	}
	vistos := map[int64]bool{}
	for _, m := range append(append(pagina1, pagina2...), pagina3...) {
		id := entryIDDe(t, m)
		if vistos[id] {
			t.Fatalf("el movimiento %d se repitió entre páginas", id)
		}
		vistos[id] = true
	}
	if vistos[nuevo] {
		t.Fatalf("el movimiento insertado durante la paginación apareció fuera de su página")
	}
}

// HU-NF-05 Criterios 5, 6 y 7: entra positivo, sale negativo, en centavos exactos y COP.
func TestHU_NF_05_Criterio5y6y7_SignosCentavosYCOP(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	sembrarDeposito(t, pool, usuarioID, 9007199254740993)
	r := NewRouter(pool, identidadFija{usuarioID}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	if len(movimientos) != 1 {
		t.Fatalf("movimientos = %d, quiero 1", len(movimientos))
	}
	if moneda := movimientos[0]["moneda"]; moneda != "COP" {
		t.Errorf("moneda = %v, quiero COP", moneda)
	}
	got := lineasDe(t, movimientos[0])
	if len(got) != 1 || got[0].monto != "9007199254740993" {
		t.Fatalf("lineas = %+v, quiero la entrada 9007199254740993 positiva, en cadena y exacta", got)
	}
}

// HU-NF-05 Criterio 12: el retraso del registro muestra las dos marcas de tiempo.
func TestHU_NF_05_Criterio12_DosMarcasDeTiempo(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	ocurrido := time.Now().UTC().Add(-3 * time.Hour)
	registrado := time.Now().UTC()
	sembrarAsientoDeHistorial(t, pool, nucleo.TipoAsientoDeposito, pgtype.UUID{}, ocurrido, registrado, []linea{
		{cuenta: nucleo.CuentaCustodiaBanco, monto: 100000},
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: -100000},
	})
	r := NewRouter(pool, identidadFija{usuarioID}, servicio, custodioDePrueba)

	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	if len(movimientos) != 1 {
		t.Fatalf("movimientos = %d, quiero 1", len(movimientos))
	}
	fechaHecho, err := time.Parse(time.RFC3339Nano, movimientos[0]["ocurrido_en"].(string))
	if err != nil {
		t.Fatalf("ocurrido_en no es RFC3339: %v", err)
	}
	fechaRegistro, err := time.Parse(time.RFC3339Nano, movimientos[0]["registrado_en"].(string))
	if err != nil {
		t.Fatalf("registrado_en no es RFC3339: %v", err)
	}
	if !fechaHecho.Before(fechaRegistro) || fechaRegistro.Sub(fechaHecho) < 2*time.Hour {
		t.Errorf("ocurrido_en = %s y registrado_en = %s, quiero dos marcas distintas y separadas", fechaHecho, fechaRegistro)
	}
}

// HU-NF-05 Criterio 10: la paginación no repite ni pierde movimientos ni parte uno entre páginas.
func TestHU_NF_05_Criterio10_PaginacionSinRepetirNiPerderNiPartir(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	retencion, esperados := sembrarHistorialDePaginacion(t, pool, usuarioID)
	r := NewRouter(pool, identidadFija{usuarioID}, servicio, custodioDePrueba)

	pagina1, meta1 := consumirPaginaDeHistorial(t, r, "página 1", "?limit=2", 2, true, esperados)
	cursor := int64(meta1["next_cursor"].(float64))
	pagina2, meta2 := consumirPaginaDeHistorial(t, r, "página 2", "?limit=2&cursor="+itoa(cursor), 2, true, esperados)
	cursor2 := int64(meta2["next_cursor"].(float64))
	pagina3, _ := consumirPaginaDeHistorial(t, r, "página 3", "?limit=2&cursor="+itoa(cursor2), 1, false, esperados)

	if len(esperados) != 0 {
		t.Fatalf("quedaron %d movimientos sin paginar: la paginación los perdió", len(esperados))
	}

	for _, m := range append(append(pagina1, pagina2...), pagina3...) {
		if entryIDDe(t, m) == retencion {
			if got := len(lineasDe(t, m)); got != 4 {
				t.Fatalf("el movimiento de la transferencia tiene %d líneas, quiero 4: se partió entre páginas", got)
			}
			return
		}
	}
	t.Fatal("el movimiento de la transferencia no apareció entero en ninguna página")
}

func sembrarHistorialDePaginacion(t *testing.T, pool *pgxpool.Pool, usuarioID string) (int64, map[int64]bool) {
	t.Helper()
	ahora := time.Now().UTC()
	esperados := map[int64]bool{}
	for _, monto := range []int64{100000, 200000, 300000} {
		esperados[sembrarDeposito(t, pool, usuarioID, monto)] = true
	}
	transferencia := uuid.New()
	retencion := sembrarAsientoDeHistorial(t, pool, "retencion", transferenciaDe(transferencia), ahora, ahora, []linea{
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: 500000},
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: 2000},
		{cuenta: nucleo.NombreCuentaRetenido(usuarioID), monto: -500000},
		{cuenta: nucleo.CuentaGMFPorPagar, monto: -2000},
	})
	esperados[retencion] = true
	sembrarAsientoDeHistorial(t, pool, "compensacion", transferenciaDe(transferencia), ahora, ahora, []linea{
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: -500000},
		{cuenta: nucleo.NombreCuentaDisponible(usuarioID), monto: -2000},
		{cuenta: nucleo.CuentaLiquidacionPendienteRiel, monto: 500000},
		{cuenta: nucleo.CuentaGMFPorPagar, monto: 2000},
	})
	esperados[sembrarDeposito(t, pool, usuarioID, 400000)] = true
	return retencion, esperados
}

func consumirPaginaDeHistorial(t *testing.T, r http.Handler, etiqueta, query string, quiero int, hayMas bool, esperados map[int64]bool) ([]map[string]any, map[string]any) {
	t.Helper()
	rec := pedirMovimientos(t, r, query)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, quiero 200 (cuerpo %q)", etiqueta, rec.Code, rec.Body.String())
	}
	movimientos := movimientosDe(t, rec)
	if len(movimientos) != quiero {
		t.Fatalf("%s: movimientos = %d, quiero %d", etiqueta, len(movimientos), quiero)
	}
	meta := metaDeMovimientos(t, rec)
	if meta["has_more"] != hayMas {
		t.Fatalf("%s: meta = %v, quiero has_more %v", etiqueta, meta, hayMas)
	}
	if (meta["next_cursor"] == nil) == hayMas {
		t.Fatalf("%s: next_cursor = %v, incoherente con has_more %v", etiqueta, meta["next_cursor"], hayMas)
	}
	for _, m := range movimientos {
		delete(esperados, entryIDDe(t, m))
	}
	return movimientos, meta
}

// HU-NF-05 Criterio 11: la consulta del historial no modifica el diario.
func TestHU_NF_05_Criterio11_LaConsultaNoEscribeEnElDiario(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	sembrarDeposito(t, pool, usuarioID, 100000)
	r := NewRouter(pool, identidadFija{usuarioID}, servicio, custodioDePrueba)

	asientosAntes, lineasAntes := contarDiario(t, pool)
	rec := pedirMovimientos(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	asientosDespues, lineasDespues := contarDiario(t, pool)

	if asientosAntes != asientosDespues || lineasAntes != lineasDespues {
		t.Errorf("el diario cambió: journal_entry %d->%d, posting %d->%d",
			asientosAntes, asientosDespues, lineasAntes, lineasDespues)
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
