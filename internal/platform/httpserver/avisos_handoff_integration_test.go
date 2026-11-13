//go:build integration

package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAvisosHandoffHTTPReal(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	usuario, otro := uuid.NewString(), uuid.NewString()
	// rol_api no tiene USAGE sobre el esquema inteligencia (migración 0021): el único camino es
	// SET LOCAL ROLE rol_inteligencia dentro de una transacción, igual que en producción.
	sembrar, e := pool.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = sembrar.Rollback(t.Context()) }()
	if _, e = sembrar.Exec(t.Context(), "SET LOCAL ROLE "+inteligencia.RolInteligencia); e != nil {
		t.Fatal(e)
	}
	// Esta prueba siembra el periodo con día 1 (el de presupuesto_anticipado y categoria_sobre_media).
	// Los periodos que guardan los otros tipos, con la fecha del pago, se cubren en las pruebas de abajo.
	// Se alterna el tipo para no chocar con la clave de deduplicación y se deja la clave repetida para
	// forzar el desempate por id.
	for i := range 5 {
		tipo := "cobro_nuevo"
		if i%2 == 1 {
			tipo = "caida_ingreso"
		}
		_, e = sembrar.Exec(t.Context(), `INSERT INTO inteligencia.aviso(id,usuario_id,tipo,clave,periodo,severidad_centavos,prioridad,texto) VALUES($1,$2,$3,$4,'2026-07-01'::date,100,1,'aviso HTTP')`, uuid.New(), usuario, tipo, fmt.Sprintf("empate|%d", i/2))
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = sembrar.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	// La bandeja pagina el mes en curso del reloj del handler: se fija en julio de 2026 para que
	// coincida con el periodo sembrado y la prueba no dependa de la fecha real.
	handler := inteligencia.NuevoHandlerConReloj(inteligencia.NuevoServicio(pool), func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) })
	montar := func(u string) *httptest.Server {
		return httptest.NewServer(NewRouterConVera(pool, nucleo.NewServicio(pool, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: identidad.Identidad{UsuarioID: u, ParteID: u}}, Inteligencia: handler}, DependenciasVera{}))
	}
	dueno := montar(usuario)
	defer dueno.Close()
	ajeno := montar(otro)
	defer ajeno.Close()
	vistos := map[uuid.UUID]bool{}
	ruta := "/v1/inteligencia/avisos?limite=2"
	for {
		res, e := dueno.Client().Get(dueno.URL + ruta)
		if e != nil {
			t.Fatal(e)
		}
		var env struct {
			Data []api.InteligenciaAviso `json:"data"`
			Meta any                     `json:"meta"`
		}
		e = json.NewDecoder(res.Body).Decode(&env)
		_ = res.Body.Close()
		if e != nil || res.StatusCode != 200 || env.Meta != nil {
			t.Fatal(res.StatusCode, e)
		}
		for _, a := range env.Data {
			if vistos[a.Id] {
				t.Fatal("aviso repetido por HTTP")
			}
			vistos[a.Id] = true
			// El código estable viaja junto a la etiqueta y sale del tipo al leer.
			codigoEsperado := map[api.InteligenciaAvisoTipo]api.InteligenciaAvisoAccionCodigo{
				"cobro_nuevo": "revisar_cobro", "caida_ingreso": "revisar_ingreso",
			}[a.Tipo]
			if a.Accion == "" || a.AccionCodigo == nil || *a.AccionCodigo != codigoEsperado {
				t.Fatalf("accion/accion_codigo del aviso %s (%s) = %q/%v", a.Id, a.Tipo, a.Accion, a.AccionCodigo)
			}
		}
		cursor := res.Header.Get("X-Vera-Siguiente-Cursor")
		if cursor == "" {
			break
		}
		ruta = "/v1/inteligencia/avisos?limite=2&cursor=" + cursor
	}
	if len(vistos) != 5 {
		t.Fatal("avisos omitidos por HTTP")
	}

	pedir := func(srv *httptest.Server, metodo, ruta string) (int, string) {
		t.Helper()
		r, e := http.NewRequestWithContext(t.Context(), metodo, srv.URL+ruta, nil)
		if e != nil {
			t.Fatal(e)
		}
		res, e := srv.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, string(b)
	}
	for id := range vistos {
		codigo, _ := pedir(dueno, "GET", "/v1/inteligencia/avisos/"+id.String())
		if codigo != 200 {
			t.Fatal(codigo)
		}
		var esperado string
		for _, suffix := range []string{"", "/descartar"} {
			metodo := "GET"
			if suffix != "" {
				metodo = "POST"
			}
			for _, identificador := range []string{id.String(), uuid.NewString()} {
				codigo, cuerpo := pedir(ajeno, metodo, "/v1/inteligencia/avisos/"+identificador+suffix)
				if codigo != 404 {
					t.Fatal(codigo, cuerpo)
				}
				if esperado != "" && cuerpo != esperado {
					t.Fatal("404 diferentes")
				}
				esperado = cuerpo
			}
		}
		codigo, _ = pedir(dueno, "GET", "/v1/inteligencia/avisos/"+id.String())
		if codigo != 200 {
			t.Fatal("descarte ajeno modificó la tarjeta")
		}
		break
	}
	for _, query := range []string{"limite=0", "limite=51", "limite=abc", "estado=otro", "cursor=invalido", "mes=2026-07&limite=2"} {
		codigo, cuerpo := pedir(dueno, "GET", "/v1/inteligencia/avisos?"+query)
		if codigo != 400 {
			t.Fatal(query, codigo, cuerpo)
		}
	}
}

type avisoHTTPSembrado struct {
	tipo, clave, periodo, creadoEn string
	prioridad                      int
}

// sembrarAvisosHTTP inserta con el rol de inteligencia, igual que producción, y devuelve los ids por
// tipo y periodo. creadoEn vacío usa el valor por defecto de la tabla.
func sembrarAvisosHTTP(t *testing.T, pool *pgxpool.Pool, usuario string, filas []avisoHTTPSembrado) map[string]uuid.UUID {
	t.Helper()
	tx, e := pool.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, e = tx.Exec(t.Context(), "SET LOCAL ROLE "+inteligencia.RolInteligencia); e != nil {
		t.Fatal(e)
	}
	ids := make(map[string]uuid.UUID, len(filas))
	for _, f := range filas {
		id := uuid.New()
		ids[f.tipo+"@"+f.periodo] = id
		creado := "now()"
		if f.creadoEn != "" {
			creado = "'" + f.creadoEn + "'::timestamptz"
		}
		if _, e = tx.Exec(t.Context(), `INSERT INTO inteligencia.aviso(id,usuario_id,tipo,clave,periodo,severidad_centavos,prioridad,texto,creado_en) VALUES($1,$2,$3,$4,$5::date,100,$6,'aviso HTTP',`+creado+`)`,
			id, usuario, f.tipo, f.clave, f.periodo, f.prioridad); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	return ids
}

func servidorDeAvisos(t *testing.T, pool *pgxpool.Pool, usuario string, reloj func() time.Time) *httptest.Server {
	t.Helper()
	handler := inteligencia.NuevoHandlerConReloj(inteligencia.NuevoServicio(pool), reloj)
	srv := httptest.NewServer(NewRouterConVera(pool, nucleo.NewServicio(pool, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: identidad.Identidad{UsuarioID: usuario, ParteID: usuario}}, Inteligencia: handler}, DependenciasVera{}))
	t.Cleanup(srv.Close)
	return srv
}

func llamarAvisos(t *testing.T, srv *httptest.Server, metodo, ruta string) (int, []byte) {
	t.Helper()
	r, e := http.NewRequestWithContext(t.Context(), metodo, srv.URL+ruta, nil)
	if e != nil {
		t.Fatal(e)
	}
	res, e := srv.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, b
}

func tiposDeLaBandeja(t *testing.T, cuerpo []byte) []string {
	t.Helper()
	var env struct {
		Data []api.InteligenciaAviso `json:"data"`
	}
	if e := json.Unmarshal(cuerpo, &env); e != nil {
		t.Fatal(e, string(cuerpo))
	}
	tipos := make([]string, 0, len(env.Data))
	for _, a := range env.Data {
		tipos = append(tipos, string(a.Tipo))
	}
	return tipos
}

// La bandeja del mes en curso devuelve los tipos cuyo periodo cae dentro del mes, no solo los de
// día 1. caida_ingreso guarda el mes anterior: sale en la bandeja de ese mes (?mes=) y no en la
// del mes en curso, igual que en el cupo y el resumen.
func TestAvisosBandejaPorDefectoConPeriodosDeCadaTipoHTTP(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	usuario := uuid.NewString()
	ids := sembrarAvisosHTTP(t, pool, usuario, []avisoHTTPSembrado{
		{"pago_no_alcanza", "pago-proximo", "2026-07-20", "2026-07-10T00:00:00Z", 1},
		{"caida_ingreso", "ingreso-mensual", "2026-06-01", "2026-07-10T00:00:00Z", 2},
		{"cobro_nuevo", "cobro-nuevo", "2026-07-28", "2026-07-10T00:00:00Z", 3},
		{"presupuesto_anticipado", "gasto-total", "2026-07-01", "2026-07-10T00:00:00Z", 4},
		{"categoria_sobre_media", "transporte", "2026-07-01", "2026-07-10T00:00:00Z", 5},
	})
	srv := servidorDeAvisos(t, pool, usuario, func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) })
	esperado := []string{"pago_no_alcanza", "cobro_nuevo", "presupuesto_anticipado", "categoria_sobre_media"}

	for _, ruta := range []string{"/v1/inteligencia/avisos", "/v1/inteligencia/avisos?limite=50", "/v1/inteligencia/avisos?estado=vigentes"} {
		codigo, cuerpo := llamarAvisos(t, srv, "GET", ruta)
		if codigo != 200 {
			t.Fatal(ruta, codigo, string(cuerpo))
		}
		if got := tiposDeLaBandeja(t, cuerpo); !slices.Equal(got, esperado) {
			t.Fatalf("%s = %v, want %v", ruta, got, esperado)
		}
	}
	codigo, cuerpo := llamarAvisos(t, srv, "GET", "/v1/inteligencia/avisos?mes=2026-06")
	if codigo != 200 || !slices.Equal(tiposDeLaBandeja(t, cuerpo), []string{"caida_ingreso"}) {
		t.Fatal("?mes=2026-06", codigo, string(cuerpo))
	}

	// Descartar uno con fecha de pago lo mueve a estado=descartados.
	if codigo, cuerpo = llamarAvisos(t, srv, "POST", "/v1/inteligencia/avisos/"+ids["pago_no_alcanza@2026-07-20"].String()+"/descartar"); codigo != 200 {
		t.Fatal(codigo, string(cuerpo))
	}
	codigo, cuerpo = llamarAvisos(t, srv, "GET", "/v1/inteligencia/avisos?estado=descartados")
	if codigo != 200 || !slices.Equal(tiposDeLaBandeja(t, cuerpo), []string{"pago_no_alcanza"}) {
		t.Fatal("descartados", codigo, string(cuerpo))
	}
}

// Descartar un aviso cuyo creado_en es posterior al reloj viola aviso_descarte_no_anterior_al_alta.
// Es un conflicto del cliente con el estado del aviso, no un fallo del servidor: 409 con el sobre
// estándar, sin tocar el aviso.
func TestAvisosDescartarConAltaFuturaResponde409HTTP(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	usuario := uuid.NewString()
	ids := sembrarAvisosHTTP(t, pool, usuario, []avisoHTTPSembrado{
		{"presupuesto_anticipado", "gasto-total", "2026-07-01", "2026-07-19T12:00:00-05:00", 4},
	})
	id := ids["presupuesto_anticipado@2026-07-01"]
	srv := servidorDeAvisos(t, pool, usuario, func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) })

	codigo, cuerpo := llamarAvisos(t, srv, "POST", "/v1/inteligencia/avisos/"+id.String()+"/descartar")
	if codigo != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", codigo, cuerpo)
	}
	var env struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if e := json.Unmarshal(cuerpo, &env); e != nil {
		t.Fatal(e)
	}
	if env.Success || env.Error == nil || env.Error.Code != "DescarteAnteriorAlAlta" || env.Error.Message == "" {
		t.Fatalf("sobre de error inesperado: %s", cuerpo)
	}
	// El aviso sigue vigente y sin marca de descarte.
	codigo, cuerpo = llamarAvisos(t, srv, "GET", "/v1/inteligencia/avisos?mes=2026-07")
	if codigo != 200 || !slices.Equal(tiposDeLaBandeja(t, cuerpo), []string{"presupuesto_anticipado"}) {
		t.Fatal("el aviso dejó de estar vigente", codigo, string(cuerpo))
	}
	var descartado *time.Time
	lectura, e := pool.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = lectura.Rollback(t.Context()) }()
	if _, e = lectura.Exec(t.Context(), "SET LOCAL ROLE "+inteligencia.RolInteligencia); e != nil {
		t.Fatal(e)
	}
	if e = lectura.QueryRow(t.Context(), `SELECT descartado_en FROM inteligencia.aviso WHERE id=$1`, id).Scan(&descartado); e != nil {
		t.Fatal(e)
	}
	if descartado != nil {
		t.Fatalf("descartado_en = %v, want NULL", descartado)
	}
}
