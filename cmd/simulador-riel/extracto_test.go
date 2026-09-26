package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// relojDePrueba fija el instante que el simulador usa como ahora, sin depender del reloj real.
type relojDePrueba struct {
	mu    sync.Mutex
	ahora time.Time
}

// Ahora devuelve el instante fijado.
func (r *relojDePrueba) Ahora() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ahora
}

// Fijar cambia el instante que el simulador reporta como ahora.
func (r *relojDePrueba) Fijar(t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ahora = t
}

// servidorExtracto levanta el simulador real con su almacén temporal y un reloj controlado.
func servidorExtracto(t *testing.T, reloj *relojDePrueba, perillas Perillas) *httptest.Server {
	t.Helper()

	simulador := NuevoSimulador(almacenDePrueba(t), NuevoControl(perillas), NuevoDirectorio(nil), "simulador-riel")
	simulador.ahora = reloj.Ahora
	servidor := httptest.NewServer(simulador.Rutas())
	t.Cleanup(servidor.Close)
	return servidor
}

// ordenExtracto arma una orden con su clave de idempotencia y su monto explícitos.
func ordenExtracto(id, clave, monto string) string {
	return `{"transferencia_id":"` + id + `","idempotency_key":"` + clave + `","llave":"3001234567","monto_centavos":"` + monto + `","moneda":"COP"}`
}

// extractoDePrueba espeja el cuerpo del extracto, con el monto como cadena.
type extractoDePrueba struct {
	Corte       time.Time                   `json:"corte"`
	Operaciones []operacionExtractoDePrueba `json:"operaciones"`
}

// operacionExtractoDePrueba es una fila del extracto tal como viaja por el cable.
type operacionExtractoDePrueba struct {
	TransferenciaID string    `json:"transferencia_id"`
	Estado          string    `json:"estado"`
	MontoCentavos   string    `json:"monto_centavos"`
	OcurridoEn      time.Time `json:"ocurrido_en"`
}

// pedirExtracto consulta la ventana y devuelve status, cuerpo crudo y extracto decodificado.
func pedirExtracto(t *testing.T, servidor *httptest.Server, desde, hasta string) (int, string, extractoDePrueba) {
	t.Helper()

	destino := servidor.URL + "/extracto?desde=" + url.QueryEscape(desde) + "&hasta=" + url.QueryEscape(hasta)
	respuesta, err := servidor.Client().Get(destino)
	if err != nil {
		t.Fatalf("pedir el extracto: %v", err)
	}
	t.Cleanup(func() { _ = respuesta.Body.Close() })
	datos, err := io.ReadAll(respuesta.Body)
	if err != nil {
		t.Fatalf("leer el extracto: %v", err)
	}
	var extracto extractoDePrueba
	if respuesta.StatusCode == http.StatusOK {
		if err := json.Unmarshal(datos, &extracto); err != nil {
			t.Fatalf("extracto no es JSON: %v (%s)", err, datos)
		}
	}
	return respuesta.StatusCode, string(datos), extracto
}

// idsDe devuelve el conjunto de identificadores de transferencia del extracto.
func idsDe(e extractoDePrueba) map[string]bool {
	ids := make(map[string]bool, len(e.Operaciones))
	for _, o := range e.Operaciones {
		ids[o.TransferenciaID] = true
	}
	return ids
}

func TestExtractoSoloDevuelveLoOcurridoEnLaVentana(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reloj := &relojDePrueba{ahora: base}
	servidor := servidorExtracto(t, reloj, Perillas{ModoCaida: ModoCaidaNinguna})

	antes, dentro, despues := uuid.NewString(), uuid.NewString(), uuid.NewString()
	reloj.Fijar(base.Add(-60 * time.Second))
	statusAntes, _ := enviar(t, servidor.Client(), servidor.URL, ordenExtracto(antes, "k-antes", "100"))
	if statusAntes != http.StatusOK {
		t.Fatalf("depositar antes de la ventana: status = %d", statusAntes)
	}
	reloj.Fijar(base.Add(10 * time.Second))
	statusDentro, _ := enviar(t, servidor.Client(), servidor.URL, ordenExtracto(dentro, "k-dentro", "200"))
	if statusDentro != http.StatusOK {
		t.Fatalf("depositar dentro de la ventana: status = %d", statusDentro)
	}
	reloj.Fijar(base.Add(120 * time.Second))
	statusDespues, _ := enviar(t, servidor.Client(), servidor.URL, ordenExtracto(despues, "k-despues", "300"))
	if statusDespues != http.StatusOK {
		t.Fatalf("depositar despues de la ventana: status = %d", statusDespues)
	}
	reloj.Fijar(base.Add(300 * time.Second))

	status, cuerpo, extracto := pedirExtracto(t, servidor,
		base.Format(time.RFC3339), base.Add(60*time.Second).Format(time.RFC3339))
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (%s)", status, cuerpo)
	}
	if len(extracto.Operaciones) != 1 || extracto.Operaciones[0].TransferenciaID != dentro {
		t.Fatalf("operaciones = %+v, quiero solo %s dentro de la ventana", extracto.Operaciones, dentro)
	}
	if !extracto.Operaciones[0].OcurridoEn.Equal(base.Add(10 * time.Second)) {
		t.Errorf("ocurrido_en = %v, quiero %v", extracto.Operaciones[0].OcurridoEn, base.Add(10*time.Second))
	}
	if !extracto.Corte.Equal(base.Add(300 * time.Second)) {
		t.Errorf("corte = %v, quiero %v", extracto.Corte, base.Add(300*time.Second))
	}
}

func TestExtractoIncluyeLaOrdenRechazada(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reloj := &relojDePrueba{ahora: base}
	servidor := servidorExtracto(t, reloj, Perillas{ModoCaida: ModoCaidaNinguna, PRechazo: 1})

	id := uuid.NewString()
	reloj.Fijar(base.Add(10 * time.Second))
	status, cuerpo := enviar(t, servidor.Client(), servidor.URL, ordenExtracto(id, "k-rechazo", "1000000"))
	if status != http.StatusOK || leerDesenlace(t, cuerpo).Resultado != estadoRechazada {
		t.Fatalf("envío = %d %s, quiero 200 rechazada", status, cuerpo)
	}
	reloj.Fijar(base.Add(60 * time.Second))

	status, salida, extracto := pedirExtracto(t, servidor,
		base.Format(time.RFC3339), base.Add(60*time.Second).Format(time.RFC3339))
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (%s)", status, salida)
	}
	if len(extracto.Operaciones) != 1 || extracto.Operaciones[0].Estado != estadoRechazada {
		t.Errorf("operaciones = %+v, quiero la rechazada incluida", extracto.Operaciones)
	}
}

func TestExtractoEmparejaPorIdGlobalYNoPorIdempotencyKey(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reloj := &relojDePrueba{ahora: base}
	servidor := servidorExtracto(t, reloj, Perillas{ModoCaida: ModoCaidaNinguna})

	usuarioUno, usuarioDos := uuid.NewString(), uuid.NewString()
	const clave = "clave-compartida-por-dos-usuarios"
	reloj.Fijar(base.Add(10 * time.Second))
	enviar(t, servidor.Client(), servidor.URL, ordenExtracto(usuarioUno, clave, "1000000"))
	enviar(t, servidor.Client(), servidor.URL, ordenExtracto(usuarioDos, clave, "1000000"))
	reloj.Fijar(base.Add(60 * time.Second))

	status, salida, extracto := pedirExtracto(t, servidor,
		base.Format(time.RFC3339), base.Add(60*time.Second).Format(time.RFC3339))
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (%s)", status, salida)
	}
	ids := idsDe(extracto)
	if !ids[usuarioUno] || !ids[usuarioDos] || len(ids) != 2 {
		t.Fatalf("ids = %v, quiero los dos transferencia_id globales", ids)
	}
	if strings.Contains(salida, clave) {
		t.Errorf("el extracto expone la clave de idempotencia: %s", salida)
	}
	for _, o := range extracto.Operaciones {
		if o.TransferenciaID == "" || o.TransferenciaID == clave {
			t.Errorf("identificador = %q, quiero el id global de transferencia", o.TransferenciaID)
		}
	}
}

func TestExtractoDevuelveMontosEnterosIdenticos(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reloj := &relojDePrueba{ahora: base}
	servidor := servidorExtracto(t, reloj, Perillas{ModoCaida: ModoCaidaNinguna})

	const grande = "9007199254740993"
	id := uuid.NewString()
	reloj.Fijar(base.Add(10 * time.Second))
	enviar(t, servidor.Client(), servidor.URL, ordenExtracto(id, "k-monto", grande))
	reloj.Fijar(base.Add(60 * time.Second))

	status, salida, extracto := pedirExtracto(t, servidor,
		base.Format(time.RFC3339), base.Add(60*time.Second).Format(time.RFC3339))
	if status != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (%s)", status, salida)
	}
	if !strings.Contains(salida, `"monto_centavos":"`+grande+`"`) {
		t.Errorf("el monto no viajó como cadena entera: %s", salida)
	}
	if len(extracto.Operaciones) != 1 || extracto.Operaciones[0].MontoCentavos != grande {
		t.Errorf("monto = %+v, quiero %s exacto pasado 2^53", extracto.Operaciones, grande)
	}
}

func TestVentanasContiguasNoDuplicanNiPierdenEnElBorde(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	borde := base.Add(60 * time.Second)
	reloj := &relojDePrueba{ahora: base}
	servidor := servidorExtracto(t, reloj, Perillas{ModoCaida: ModoCaidaNinguna})

	primera, enBorde, ultima := uuid.NewString(), uuid.NewString(), uuid.NewString()
	reloj.Fijar(base.Add(10 * time.Second))
	enviar(t, servidor.Client(), servidor.URL, ordenExtracto(primera, "k-1", "100"))
	reloj.Fijar(borde)
	enviar(t, servidor.Client(), servidor.URL, ordenExtracto(enBorde, "k-2", "200"))
	reloj.Fijar(base.Add(90 * time.Second))
	enviar(t, servidor.Client(), servidor.URL, ordenExtracto(ultima, "k-3", "300"))
	reloj.Fijar(base.Add(200 * time.Second))

	status, salida, izquierda := pedirExtracto(t, servidor,
		base.Format(time.RFC3339), borde.Format(time.RFC3339))
	if status != http.StatusOK {
		t.Fatalf("ventana izquierda: status = %d (%s)", status, salida)
	}
	status, salida, derecha := pedirExtracto(t, servidor,
		borde.Format(time.RFC3339), base.Add(200*time.Second).Format(time.RFC3339))
	if status != http.StatusOK {
		t.Fatalf("ventana derecha: status = %d (%s)", status, salida)
	}

	idsIzquierda, idsDerecha := idsDe(izquierda), idsDe(derecha)
	if len(idsIzquierda) != 1 || !idsIzquierda[primera] {
		t.Errorf("ventana izquierda = %v, quiero solo %s", idsIzquierda, primera)
	}
	if !idsDerecha[enBorde] || !idsDerecha[ultima] || len(idsDerecha) != 2 {
		t.Errorf("ventana derecha = %v, quiero %s y %s", idsDerecha, enBorde, ultima)
	}
	if idsIzquierda[enBorde] {
		t.Error("la orden del borde quedó duplicada en las dos ventanas")
	}
	union := map[string]bool{}
	for id := range idsIzquierda {
		union[id] = true
	}
	for id := range idsDerecha {
		union[id] = true
	}
	if len(union) != 3 {
		t.Errorf("la unión de las ventanas = %v, quiero las tres sin perder ninguna", union)
	}
}

func TestExtractoRechazaVentanaInvalida(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	reloj := &relojDePrueba{ahora: base.Add(300 * time.Second)}
	servidor := servidorExtracto(t, reloj, Perillas{ModoCaida: ModoCaidaNinguna})

	tests := map[string]struct{ desde, hasta string }{
		"sin desde":        {desde: "", hasta: base.Format(time.RFC3339)},
		"sin hasta":        {desde: base.Format(time.RFC3339), hasta: ""},
		"desde inválido":   {desde: "ayer", hasta: base.Add(60 * time.Second).Format(time.RFC3339)},
		"ventana vacía":    {desde: base.Format(time.RFC3339), hasta: base.Format(time.RFC3339)},
		"ventana al revés": {desde: base.Add(60 * time.Second).Format(time.RFC3339), hasta: base.Format(time.RFC3339)},
		"hasta futuro":     {desde: base.Format(time.RFC3339), hasta: base.Add(600 * time.Second).Format(time.RFC3339)},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			status, salida, _ := pedirExtracto(t, servidor, tc.desde, tc.hasta)
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, quiero 400 (%s)", status, salida)
			}
		})
	}
}
