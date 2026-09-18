package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// corteDePrueba espeja el corte del cable: el saldo como cadena, nunca como número.
type corteDePrueba struct {
	CorteEn       time.Time `json:"corte_en"`
	SaldoCentavos string    `json:"saldo_centavos"`
	Origen        string    `json:"origen"`
}

// movimientoDePrueba espeja la respuesta de una notificación.
type movimientoDePrueba struct {
	ID            int64     `json:"id"`
	Referencia    string    `json:"referencia"`
	Tipo          string    `json:"tipo"`
	MontoCentavos string    `json:"monto_centavos"`
	OcurridoEn    time.Time `json:"ocurrido_en"`
	RegistradoEn  time.Time `json:"registrado_en"`
	Reintento     bool      `json:"reintento"`
}

// simuladorDePrueba levanta el simulador real sobre un SQLite temporal.
func simuladorDePrueba(t *testing.T) (*httptest.Server, *Simulador) {
	t.Helper()

	almacen, err := AbrirAlmacen(filepath.Join(t.TempDir(), "movimiento.db"))
	if err != nil {
		t.Fatalf("abrir almacén: %v", err)
	}
	t.Cleanup(func() { _ = almacen.Cerrar() })

	sim := NuevoSimulador(almacen, "simulador-custodia", NuevoControlPerillas(perillasFieles()))
	servidor := httptest.NewServer(sim.Rutas())
	t.Cleanup(servidor.Close)
	return servidor, sim
}

// pedir hace la petición y devuelve el status con el cuerpo crudo.
func pedir(t *testing.T, servidor *httptest.Server, metodo, ruta, cuerpo string) (int, []byte) {
	t.Helper()

	var lector io.Reader
	if cuerpo != "" {
		lector = strings.NewReader(cuerpo)
	}
	peticion, err := http.NewRequestWithContext(t.Context(), metodo, servidor.URL+ruta, lector)
	if err != nil {
		t.Fatalf("armar petición: %v", err)
	}
	peticion.Header.Set("Content-Type", "application/json")
	respuesta, err := servidor.Client().Do(peticion)
	if err != nil {
		t.Fatalf("hacer petición: %v", err)
	}
	defer func() { _ = respuesta.Body.Close() }()
	datos, err := io.ReadAll(respuesta.Body)
	if err != nil {
		t.Fatalf("leer respuesta: %v", err)
	}
	return respuesta.StatusCode, datos
}

// notificar hace un POST de notificación al endpoint del tipo dado.
func notificar(t *testing.T, servidor *httptest.Server, ruta, referencia, monto, ocurrido string) (int, movimientoDePrueba) {
	t.Helper()

	cuerpo := fmt.Sprintf(`{"referencia":%q,"monto_centavos":%q,"ocurrido_en":%q}`, referencia, monto, ocurrido)
	status, datos := pedir(t, servidor, http.MethodPost, ruta, cuerpo)
	if status != http.StatusOK {
		return status, movimientoDePrueba{}
	}
	var movimiento movimientoDePrueba
	if err := json.Unmarshal(datos, &movimiento); err != nil {
		t.Fatalf("leer movimiento: %v (%s)", err, datos)
	}
	return status, movimiento
}

// extraer pide el extracto y devuelve el status con el corte cuando fue exitoso.
func extraer(t *testing.T, servidor *httptest.Server, corte string) (int, corteDePrueba) {
	t.Helper()

	status, datos := pedir(t, servidor, http.MethodGet, "/extracto?corte_en="+corte, "")
	if status != http.StatusOK {
		return status, corteDePrueba{}
	}
	var corteLeido corteDePrueba
	if err := json.Unmarshal(datos, &corteLeido); err != nil {
		t.Fatalf("leer corte: %v (%s)", err, datos)
	}
	return status, corteLeido
}

// movimientosDe cuenta las filas registradas en el almacén del simulador.
func movimientosDe(t *testing.T, sim *Simulador) []Movimiento {
	t.Helper()

	movimientos, err := sim.almacen.Movimientos(t.Context())
	if err != nil {
		t.Fatalf("leer movimientos: %v", err)
	}
	return movimientos
}

// HU-NF-09 Criterio 2: sin referencia, sin monto o sin instante se rechaza y no queda movimiento.
func TestHU_NF_09_Criterio2_DatosObligatoriosDeUnaNotificacion(t *testing.T) {
	tests := map[string]struct {
		cuerpo   string
		faltante string
	}{
		"sin referencia":       {cuerpo: `{"monto_centavos":"1000","ocurrido_en":"2026-09-17T12:00:00Z"}`, faltante: "referencia"},
		"sin monto":            {cuerpo: `{"referencia":"r","ocurrido_en":"2026-09-17T12:00:00Z"}`, faltante: "monto_centavos"},
		"sin instante":         {cuerpo: `{"referencia":"r","monto_centavos":"1000"}`, faltante: "ocurrido_en"},
		"referencia en blanco": {cuerpo: `{"referencia":"   ","monto_centavos":"1000","ocurrido_en":"2026-09-17T12:00:00Z"}`, faltante: "referencia"},
	}

	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			servidor, sim := simuladorDePrueba(t)

			status, datos := pedir(t, servidor, http.MethodPost, "/notificaciones/depositos", tc.cuerpo)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, quiero 400 (%s)", status, datos)
			}
			if !strings.Contains(string(datos), tc.faltante) {
				t.Errorf("la respuesta no nombra %q: %s", tc.faltante, datos)
			}
			if filas := movimientosDe(t, sim); len(filas) != 0 {
				t.Errorf("movimientos = %d, quiero 0", len(filas))
			}
		})
	}
}

// HU-NF-09 Criterio 3: un monto que no es entero positivo en centavos se rechaza sin registrar.
func TestHU_NF_09_Criterio3_MontoInvalido(t *testing.T) {
	tests := map[string]string{
		"cero":         `{"referencia":"r","monto_centavos":"0","ocurrido_en":"2026-09-17T12:00:00Z"}`,
		"negativo":     `{"referencia":"r","monto_centavos":"-5","ocurrido_en":"2026-09-17T12:00:00Z"}`,
		"decimal":      `{"referencia":"r","monto_centavos":"12.50","ocurrido_en":"2026-09-17T12:00:00Z"}`,
		"texto":        `{"referencia":"r","monto_centavos":"abc","ocurrido_en":"2026-09-17T12:00:00Z"}`,
		"numero JSON":  `{"referencia":"r","monto_centavos":700,"ocurrido_en":"2026-09-17T12:00:00Z"}`,
		"cadena vacía": `{"referencia":"r","monto_centavos":"","ocurrido_en":"2026-09-17T12:00:00Z"}`,
	}

	for nombre, cuerpo := range tests {
		t.Run(nombre, func(t *testing.T) {
			servidor, sim := simuladorDePrueba(t)

			status, datos := pedir(t, servidor, http.MethodPost, "/notificaciones/depositos", cuerpo)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, quiero 400 (%s)", status, datos)
			}
			if !strings.Contains(string(datos), "monto") {
				t.Errorf("la respuesta no menciona el monto: %s", datos)
			}
			if filas := movimientosDe(t, sim); len(filas) != 0 {
				t.Errorf("movimientos = %d, quiero 0", len(filas))
			}
		})
	}
}

// HU-NF-09 Criterio 4: un depósito válido se registra con sus datos y el saldo sube exacto.
func TestHU_NF_09_Criterio4_DepositoValidoSubeElSaldo(t *testing.T) {
	servidor, sim := simuladorDePrueba(t)

	status, movimiento := notificar(t, servidor, "/notificaciones/depositos", "dep-1", "250000", "2026-09-17T12:00:00Z")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200", status)
	}
	if movimiento.ID == 0 || movimiento.Reintento {
		t.Errorf("movimiento = %+v, quiero un id nuevo sin reintento", movimiento)
	}
	if movimiento.MontoCentavos != "250000" || movimiento.Tipo != "deposito" || movimiento.Referencia != "dep-1" {
		t.Errorf("movimiento = %+v, quiero los datos tal como llegaron", movimiento)
	}
	if !movimiento.OcurridoEn.Equal(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("ocurrido_en = %v, quiero el instante que llegó", movimiento.OcurridoEn)
	}
	if movimiento.RegistradoEn.IsZero() {
		t.Error("registrado_en quedó vacío: el simulador no puso su propia marca")
	}

	filas := movimientosDe(t, sim)
	if len(filas) != 1 || filas[0].Tipo != tipoDeposito {
		t.Fatalf("movimientos = %+v, quiero un depósito", filas)
	}

	status, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
	if status != http.StatusOK || corte.SaldoCentavos != "250000" {
		t.Errorf("extracto = %d %+v, quiero saldo 250000", status, corte)
	}
}

// HU-NF-09 Criterio 5: un retiro válido se registra y el saldo baja exacto, aunque hoy nadie lo llame.
func TestHU_NF_09_Criterio5_RetiroValidoBajaElSaldo(t *testing.T) {
	servidor, sim := simuladorDePrueba(t)

	if _, _, err := sim.almacen.Registrar(t.Context(), Movimiento{
		Referencia: "dep-inicial", Tipo: tipoDeposito, MontoCentavos: 250000,
		OcurridoEn: time.Date(2026, 9, 17, 11, 0, 0, 0, time.UTC),
	}, time.Now()); err != nil {
		t.Fatalf("preparar depósito: %v", err)
	}

	status, movimiento := notificar(t, servidor, "/notificaciones/retiros", "ret-1", "50000", "2026-09-17T12:00:00Z")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200", status)
	}
	if movimiento.Tipo != "retiro" || movimiento.MontoCentavos != "50000" {
		t.Errorf("movimiento = %+v, quiero un retiro de 50000", movimiento)
	}

	status, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
	if status != http.StatusOK || corte.SaldoCentavos != "200000" {
		t.Errorf("extracto = %d %+v, quiero saldo 200000", status, corte)
	}
}

// HU-NF-09 Criterio 6: un reenvío deja un solo movimiento y responde igual, con reintento en true.
func TestHU_NF_09_Criterio6_ReenvioDevuelveLaMismaRespuesta(t *testing.T) {
	servidor, sim := simuladorDePrueba(t)

	status, primera := notificar(t, servidor, "/notificaciones/depositos", "dep-1", "250000", "2026-09-17T12:00:00Z")
	if status != http.StatusOK {
		t.Fatalf("primera: status = %d, quiero 200", status)
	}
	status, segunda := notificar(t, servidor, "/notificaciones/depositos", "dep-1", "250000", "2026-09-17T12:00:00Z")
	if status != http.StatusOK {
		t.Fatalf("reenvío: status = %d, quiero 200", status)
	}
	if segunda.Reintento != true {
		t.Error("el reenvío no marcó reintento = true")
	}
	if primera.ID != segunda.ID || primera.RegistradoEn != segunda.RegistradoEn || primera.MontoCentavos != segunda.MontoCentavos {
		t.Errorf("primera = %+v, segunda = %+v, quiero el mismo movimiento", primera, segunda)
	}
	if filas := movimientosDe(t, sim); len(filas) != 1 {
		t.Errorf("movimientos = %d, quiero 1", len(filas))
	}

	status, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
	if status != http.StatusOK || corte.SaldoCentavos != "250000" {
		t.Errorf("extracto = %d %+v, quiero saldo 250000", status, corte)
	}
}

// HU-NF-09 Criterio 6: dos copias simultáneas de la misma notificación dejan una sola fila.
func TestHU_NF_09_Criterio6_CopiasSimultaneasDejanUnaFila(t *testing.T) {
	servidor, sim := simuladorDePrueba(t)

	const copias = 8
	cuerpo := `{"referencia":"dep-simultanea","monto_centavos":"1000","ocurrido_en":"2026-09-17T12:00:00Z"}`
	var wg sync.WaitGroup
	fallos := make(chan string, copias)
	for i := 0; i < copias; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			respuesta, err := http.Post(servidor.URL+"/notificaciones/depositos", "application/json", strings.NewReader(cuerpo))
			if err != nil {
				fallos <- err.Error()
				return
			}
			defer func() { _ = respuesta.Body.Close() }()
			if respuesta.StatusCode != http.StatusOK {
				fallos <- fmt.Sprintf("status %d", respuesta.StatusCode)
			}
		}()
	}
	wg.Wait()
	close(fallos)
	for fallo := range fallos {
		t.Errorf("copia simultánea: %s", fallo)
	}

	if filas := movimientosDe(t, sim); len(filas) != 1 {
		t.Fatalf("movimientos = %d, quiero 1", len(filas))
	}
	status, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
	if status != http.StatusOK || corte.SaldoCentavos != "1000" {
		t.Errorf("extracto = %d %+v, quiero saldo 1000", status, corte)
	}
}

// HU-NF-09 Criterio 7: la misma referencia con otro monto o el tipo contrario es conflicto.
func TestHU_NF_09_Criterio7_MismaReferenciaDistintosDatos(t *testing.T) {
	tests := map[string]struct {
		ruta  string
		monto string
	}{
		"otro monto":     {ruta: "/notificaciones/depositos", monto: "999"},
		"tipo contrario": {ruta: "/notificaciones/retiros", monto: "250000"},
	}

	for nombre, tc := range tests {
		t.Run(nombre, func(t *testing.T) {
			servidor, sim := simuladorDePrueba(t)
			if status, _ := notificar(t, servidor, "/notificaciones/depositos", "dep-1", "250000", "2026-09-17T12:00:00Z"); status != http.StatusOK {
				t.Fatalf("preparar depósito: status = %d", status)
			}

			status, cuerpo := notificar(t, servidor, tc.ruta, "dep-1", tc.monto, "2026-09-17T12:00:00Z")
			if status != http.StatusConflict {
				t.Fatalf("status = %d, quiero 409 (%+v)", status, cuerpo)
			}
			if filas := movimientosDe(t, sim); len(filas) != 1 {
				t.Errorf("movimientos = %d, quiero 1 (sin segundo movimiento)", len(filas))
			}
			status, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
			if status != http.StatusOK || corte.SaldoCentavos != "250000" {
				t.Errorf("extracto = %d %+v, quiero saldo 250000 sin cambio", status, corte)
			}
		})
	}
}

// HU-NF-09 Criterio 8 y 11: el extracto es el corte completo y el saldo viaja como cadena.
func TestHU_NF_09_Criterio8_ExtractoEsElCorteCompleto(t *testing.T) {
	servidor, _ := simuladorDePrueba(t)
	if status, _ := notificar(t, servidor, "/notificaciones/depositos", "dep-1", "9007199254740993", "2026-09-17T12:00:00Z"); status != http.StatusOK {
		t.Fatalf("preparar depósito: status = %d", status)
	}

	status, datos := pedir(t, servidor, http.MethodGet, "/extracto?corte_en=2026-09-17T12:00:01Z", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200", status)
	}
	if !strings.Contains(string(datos), `"saldo_centavos":"9007199254740993"`) {
		t.Errorf("el saldo no viajó como cadena entera: %s", datos)
	}
	_, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
	if corte.Origen != "simulador-custodia" {
		t.Errorf("origen = %q, quiero %q", corte.Origen, "simulador-custodia")
	}
	if !corte.CorteEn.Equal(time.Date(2026, 9, 17, 12, 0, 1, 0, time.UTC)) {
		t.Errorf("corte_en = %v, quiero el instante pedido", corte.CorteEn)
	}
}

// HU-NF-09 Criterio 11: el monto viaja, se guarda y se responde como entero de centavos, nunca decimal.
func TestHU_NF_09_Criterio11_MontoEnteroDeCentavos(t *testing.T) {
	servidor, sim := simuladorDePrueba(t)
	const grande = "9007199254740993"

	var tipo string
	consulta := `SELECT type FROM pragma_table_info('movimiento') WHERE name = 'monto_centavos'`
	if err := sim.almacen.db.QueryRowContext(t.Context(), consulta).Scan(&tipo); err != nil {
		t.Fatalf("leer el tipo de la columna: %v", err)
	}
	if tipo != "INTEGER" {
		t.Errorf("monto_centavos en SQLite = %q, quiero INTEGER", tipo)
	}

	status, datos := pedir(t, servidor, http.MethodPost, "/notificaciones/depositos",
		`{"referencia":"dep-grande","monto_centavos":"`+grande+`","ocurrido_en":"2026-09-17T12:00:00Z"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (%s)", status, datos)
	}
	if !strings.Contains(string(datos), `"monto_centavos":"`+grande+`"`) {
		t.Errorf("el monto no viajó como cadena entera: %s", datos)
	}
	_, corte := extraer(t, servidor, "2026-09-17T12:00:01Z")
	if corte.SaldoCentavos != grande {
		t.Errorf("saldo = %q, quiero %q exacto (entero pasado 2^53)", corte.SaldoCentavos, grande)
	}
}

// HU-NF-09 Criterio 9: un corte futuro se rechaza y el saldo solo suma lo ocurrido hasta el corte.
func TestHU_NF_09_Criterio9_CortePedidoYValidado(t *testing.T) {
	servidor, _ := simuladorDePrueba(t)

	if status, _ := notificar(t, servidor, "/notificaciones/depositos", "dep-1", "1000", "2026-09-17T12:00:00Z"); status != http.StatusOK {
		t.Fatal("no se pudo preparar el primer depósito")
	}
	if status, _ := notificar(t, servidor, "/notificaciones/depositos", "dep-2", "2000", "2026-09-17T12:10:00Z"); status != http.StatusOK {
		t.Fatal("no se pudo preparar el segundo depósito")
	}

	status, datos := pedir(t, servidor, http.MethodGet, "/extracto?corte_en=2099-01-01T00:00:00Z", "")
	if status != http.StatusBadRequest {
		t.Fatalf("corte futuro: status = %d, quiero 400 (%s)", status, datos)
	}
	if strings.Contains(string(datos), "saldo_centavos") {
		t.Errorf("un corte futuro no debe entregar corte: %s", datos)
	}

	_, entre := extraer(t, servidor, "2026-09-17T12:05:00Z")
	if entre.SaldoCentavos != "1000" {
		t.Errorf("saldo al corte intermedio = %q, quiero 1000 (solo lo ocurrido hasta ahí)", entre.SaldoCentavos)
	}
	_, inclusive := extraer(t, servidor, "2026-09-17T12:10:00Z")
	if inclusive.SaldoCentavos != "3000" {
		t.Errorf("saldo al corte inclusive = %q, quiero 3000", inclusive.SaldoCentavos)
	}
}
