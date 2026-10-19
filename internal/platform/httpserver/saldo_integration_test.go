//go:build integration

package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// identidadFija le da a cada prueba su propio usuario, en vez del usuario de prueba compartido.
type identidadFija struct{ usuarioID string }

func (i identidadFija) Identidad(*http.Request) (identidad.Identidad, error) {
	return identidad.Identidad{UsuarioID: i.usuarioID, Contexto: "usuario"}, nil
}

func pedirSaldo(t *testing.T, r http.Handler, query string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/v1/saldo"+query, nil)
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func datosDeSaldo(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	env := decodeEnvelope(t, rec)
	data, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente o con forma rara en %q", rec.Body.String())
	}
	return data
}

type linea struct {
	cuenta string
	monto  int64
}

func sembrarAsiento(t *testing.T, pool *pgxpool.Pool, lineas []linea) {
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
		"INSERT INTO journal_entry (tipo, ocurrido_en) VALUES ($1, now()) RETURNING id",
		nucleo.TipoAsientoDeposito).Scan(&entryID); err != nil {
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
}

func sembrarUsuarioConSaldo(t *testing.T, pool *pgxpool.Pool, s *nucleo.Servicio, deposito, retenido int64) string {
	t.Helper()
	ctx := t.Context()

	usuarioID := auth.SembrarParteDePruebaConID(t, pool, uuid.NewString())
	if _, err := s.AbrirSubcuentasDeUsuario(ctx, usuarioID); err != nil {
		t.Fatalf("abrir subcuentas: %v", err)
	}

	disponible := nucleo.NombreCuentaDisponible(usuarioID)
	if deposito != 0 {
		sembrarAsiento(t, pool, []linea{
			{cuenta: nucleo.CuentaCustodiaBanco, monto: deposito},
			{cuenta: disponible, monto: -deposito},
		})
	}
	if retenido != 0 {
		sembrarAsiento(t, pool, []linea{
			{cuenta: disponible, monto: retenido},
			{cuenta: nucleo.NombreCuentaRetenido(usuarioID), monto: -retenido},
		})
	}
	return usuarioID
}

func contarDiario(t *testing.T, pool *pgxpool.Pool) (asientos, lineas int64) {
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
	if err := tx.QueryRow(ctx,
		"SELECT (SELECT count(*) FROM nucleo.journal_entry), (SELECT count(*) FROM nucleo.posting)").
		Scan(&asientos, &lineas); err != nil {
		t.Fatalf("contar el diario: %v", err)
	}
	return asientos, lineas
}

// HU-NF-04 Criterios 1 y 7: GET /saldo autenticado devuelve el disponible en centavos y COP.
func TestHU_NF_04_Criterio1_SaldoDevuelveCentavosYCOP(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 123456, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	rec := pedirSaldo(t, r, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	data := datosDeSaldo(t, rec)
	if got := data["monto"]; got != "123456" {
		t.Errorf("monto = %v, quiero %q", got, "123456")
	}
	if got := data["moneda"]; got != "COP" {
		t.Errorf("moneda = %v, quiero COP", got)
	}
	marca, ok := data["calculado_en"].(string)
	if !ok {
		t.Fatalf("calculado_en = %v, quiero una cadena RFC3339", data["calculado_en"])
	}
	instante, err := time.Parse(time.RFC3339Nano, marca)
	if err != nil {
		t.Fatalf("calculado_en %q no es RFC3339: %v", marca, err)
	}
	if delta := time.Since(instante); delta < 0 || delta > time.Minute {
		t.Errorf("calculado_en = %s, quiero un instante reciente (delta %s)", instante, delta)
	}
}

// HU-NF-04 Criterio 1: sin la credencial, el saldo no se responde.
func TestHU_NF_04_Criterio1_SaldoSinCredencialDa401(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: auth.UsuarioDePrueba{}})

	req := httptest.NewRequest(http.MethodGet, "/v1/saldo", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
}

// HU-NF-04 Criterio 2: apuntar a otra cuenta se rechaza y no devuelve datos ajenos.
func TestHU_NF_04_Criterio2_SaldoDeOtroUsuarioSeRechaza(t *testing.T) {
	tests := map[string]string{
		"usuario_id ajeno": "?usuario_id=00000000-0000-4000-8000-000000000002",
		"cuenta_id ajena":  "?cuenta_id=pasivo%3Ausuario%3A00000000-0000-4000-8000-000000000002%3Adisponible",
	}

	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 1000000, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	for name, query := range tests {
		t.Run(name, func(t *testing.T) {
			rec := pedirSaldo(t, r, query)

			assertErrorEnvelope(t, rec, http.StatusBadRequest, "TargetNotAllowed")
			if env := decodeEnvelope(t, rec); env["data"] != nil {
				t.Errorf("data = %v, quiero null en el rechazo", env["data"])
			}
		})
	}
}

// HU-NF-04 Criterio 3: un usuario sin movimientos recibe cero y no un error ni una respuesta vacía.
func TestHU_NF_04_Criterio3_SaldoSinMovimientosEsCero(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 0, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	rec := pedirSaldo(t, r, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	data := datosDeSaldo(t, rec)
	if got := data["monto"]; got != "0" {
		t.Errorf("monto = %v, quiero %q", got, "0")
	}
}

// HU-NF-04 Criterio 8: el dinero retenido en otra operación no cuenta en el saldo mostrado.
func TestHU_NF_04_Criterio8_DisponibleNoIncluyeLaRetenida(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 1000000, 250000)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	rec := pedirSaldo(t, r, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	data := datosDeSaldo(t, rec)
	if got := data["monto"]; got != "750000" {
		t.Errorf("monto = %v, quiero %q (depósito menos lo retenido)", got, "750000")
	}
}

// HU-NF-04 Criterios 5 y 6: lo que está a favor se ve positivo y el monto viaja exacto, sin redondeo.
func TestHU_NF_04_Criterio5_Criterio6_SaldoAFavorPositivoYExacto(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 9007199254740993, 0)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	rec := pedirSaldo(t, r, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	data := datosDeSaldo(t, rec)
	if got := data["monto"]; got != "9007199254740993" {
		t.Errorf("monto = %v, quiero %q (positivo, cadena y exacto más allá de 2^53)", got, "9007199254740993")
	}
}

// HU-NF-04 Criterio 11.1: la consulta no modifica el diario.
func TestHU_NF_04_Criterio11_LaConsultaNoEscribeEnElDiario(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	usuarioID := sembrarUsuarioConSaldo(t, pool, servicio, 1000000, 250000)
	r := NewRouter(pool, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadFija{usuarioID}})

	asientosAntes, lineasAntes := contarDiario(t, pool)
	rec := pedirSaldo(t, r, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	asientosDespues, lineasDespues := contarDiario(t, pool)

	if asientosAntes != asientosDespues || lineasAntes != lineasDespues {
		t.Errorf("el diario cambió: journal_entry %d->%d, posting %d->%d",
			asientosAntes, asientosDespues, lineasAntes, lineasDespues)
	}
}
