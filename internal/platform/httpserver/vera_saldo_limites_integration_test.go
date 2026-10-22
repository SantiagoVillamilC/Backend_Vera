//go:build integration

package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestVeraSaldoLimitesContraEndpoints(t *testing.T) {
	abrir := func(env string) *pgxpool.Pool {
		t.Helper()
		raw := os.Getenv(env)
		if raw == "" {
			t.Fatalf("falta %s", env)
		}
		p, err := pgxpool.New(t.Context(), raw)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	admin, app := abrir("DATABASE_URL"), abrir("APP_DATABASE_URL")
	servicio := nucleo.NewServicio(app, nucleo.LectorNivelKYCFijo{}, nil, nil, nil)
	catalogo, err := copilot.NuevoCatalogo(map[string]copilot.ManejadorHerramienta{"consultar_saldo": copilot.ConsultaSaldoIA(servicio), "consultar_limites": copilot.ConsultaLimitesIA(servicio)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	huella := func() string {
		t.Helper()
		var huella string
		if err := admin.QueryRow(t.Context(), `SELECT
 (SELECT md5(COALESCE(jsonb_agg(to_jsonb(c) ORDER BY id)::text,'')) FROM nucleo.cuenta c) ||
 (SELECT md5(COALESCE(jsonb_agg(to_jsonb(j) ORDER BY id)::text,'')) FROM nucleo.journal_entry j) ||
 (SELECT md5(COALESCE(jsonb_agg(to_jsonb(p) ORDER BY id)::text,'')) FROM nucleo.posting p)`).Scan(&huella); err != nil {
			t.Fatal(err)
		}
		return huella
	}
	for _, caso := range []struct {
		nombre                     string
		saldo, retenido, acumulado nucleo.Centavos
		gmf                        bool
	}{{"sinMovimientos", 0, 0, 0, false}, {"saldoDisponibleYRetenido", 100000000, 5000, 0, false}, {"conGMF", 100000000, 7000, 400000000, true}} {
		t.Run(caso.nombre, func(t *testing.T) {
			usuario, otro := uuid.NewString(), uuid.NewString()
			for _, u := range []string{usuario, otro} {
				if _, err := servicio.AbrirSubcuentasDeUsuario(t.Context(), u); err != nil {
					t.Fatal(err)
				}
			}
			fondear := func(u string, disponible, retenido nucleo.Centavos) {
				t.Helper()
				if disponible == 0 && retenido == 0 {
					return
				}
				tx, err := admin.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(context.Background()) }()
				var entry int64
				if err := tx.QueryRow(t.Context(), "INSERT INTO nucleo.journal_entry(tipo,ocurrido_en) VALUES ('prueba',clock_timestamp()) RETURNING id").Scan(&entry); err != nil {
					t.Fatal(err)
				}
				total, err := disponible.Sumar(retenido)
				if err != nil {
					t.Fatal(err)
				}
				for cuenta, monto := range map[string]nucleo.Centavos{nucleo.NombreCuentaDisponible(u): -disponible, nucleo.NombreCuentaRetenido(u): -retenido, nucleo.CuentaCustodiaBanco: total} {
					if monto != 0 {
						if _, err := tx.Exec(t.Context(), "INSERT INTO nucleo.posting(entry_id,cuenta_id,monto,moneda) VALUES ($1,$2,$3,'COP')", entry, cuenta, monto.Int64()); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := tx.Commit(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			fondear(usuario, caso.saldo, caso.retenido)
			fondear(otro, 999999999, 0)
			if caso.acumulado > 0 {
				exec("INSERT INTO nucleo.transferencia(id,usuario_id,cuenta_origen,llave_destino,monto,moneda,idempotency_key,estado,creada_en) VALUES ($1,$2,$3,$4,$5,'COP',$6,'reservada',$7)", uuid.New(), usuario, nucleo.NombreCuentaDisponible(usuario), "intra:"+otro, caso.acumulado.Int64(), uuid.NewString(), time.Now().UTC())
			}
			id := identidad.Identidad{UsuarioID: usuario, ParteID: uuid.NewString(), Contexto: auth.ContextoUsuario}
			ctx := identidad.ConIdentidad(t.Context(), id)
			router := NewRouter(app, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: id}})
			endpoint := func(path string) nucleo.Centavos {
				t.Helper()
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
				if w.Code != 200 {
					t.Fatalf("%s: %d %s", path, w.Code, w.Body)
				}
				var body struct {
					Data struct {
						Monto nucleo.Centavos `json:"monto"`
					}
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				return body.Data.Monto
			}
			previo := huella()
			saldo, err := catalogo.Ejecutar(ctx, copilot.LlamadaIA{Nombre: "consultar_saldo", Argumentos: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			var s struct {
				Datos struct {
					Disponible, Retenido struct {
						Centavos nucleo.Centavos `json:"centavos"`
						Pesos    string          `json:"pesos"`
					}
					Moneda string `json:"moneda"`
				} `json:"datos"`
			}
			if err := json.Unmarshal(saldo.Datos, &s); err != nil {
				t.Fatal(err)
			}
			if s.Datos.Disponible.Centavos != endpoint("/v1/saldo") || s.Datos.Disponible.Centavos != caso.saldo || s.Datos.Retenido.Centavos != caso.retenido || s.Datos.Moneda != "COP" || s.Datos.Disponible.Pesos != caso.saldo.PresentarUnidadesMayores()+" COP" {
				t.Fatal(string(saldo.Datos))
			}
			limites, err := catalogo.Ejecutar(ctx, copilot.LlamadaIA{Nombre: "consultar_limites", Argumentos: []byte(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			var l struct {
				Datos struct {
					Maximo struct {
						Centavos nucleo.Centavos `json:"centavos"`
					} `json:"maximo_enviable"`
					GMF struct {
						Centavos nucleo.Centavos `json:"centavos"`
					} `json:"gmf"`
					Aplica bool `json:"aplica_gmf"`
				} `json:"datos"`
			}
			if err := json.Unmarshal(limites.Datos, &l); err != nil {
				t.Fatal(err)
			}
			if l.Datos.Maximo.Centavos != endpoint("/v1/transferencias/maximo") || l.Datos.Aplica != caso.gmf || (l.Datos.GMF.Centavos > 0) != caso.gmf {
				t.Fatal(string(limites.Datos))
			}
			if previo != huella() {
				t.Fatal("una consulta modificó el ledger")
			}
			for _, bytes := range [][]byte{saldo.Datos, limites.Datos} {
				if strings.Contains(string(bytes), usuario) || strings.Contains(string(bytes), otro) || strings.Contains(string(bytes), id.ParteID) {
					t.Fatal("identidad expuesta")
				}
			}
		})
	}
	// Configuración incompleta: el catálogo comunica indisponibilidad, el endpoint responde 503.
	var anio int
	var valor int64
	var fuente string
	anio = time.Now().In(time.FixedZone("Colombia", -5*3600)).Year()
	if err := admin.QueryRow(t.Context(), "SELECT valor_centavos,fuente FROM nucleo.valor_uvt WHERE anio=$1", anio).Scan(&valor, &fuente); err != nil {
		t.Fatal(err)
	}
	exec("DELETE FROM nucleo.valor_uvt WHERE anio=$1", anio)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "INSERT INTO nucleo.valor_uvt(anio,valor_centavos,fuente) VALUES ($1,$2,$3)", anio, valor, fuente)
	})
	id := identidad.Identidad{UsuarioID: uuid.NewString(), ParteID: uuid.NewString(), Contexto: auth.ContextoUsuario}
	ctx := identidad.ConIdentidad(t.Context(), id)
	r, err := catalogo.Ejecutar(ctx, copilot.LlamadaIA{Nombre: "consultar_limites", Argumentos: []byte(`{}`)})
	if err != nil || !strings.Contains(string(r.Datos), "FunctionUnavailable") {
		t.Fatalf("configuración incompleta: %s %v", r.Datos, err)
	}
	w := httptest.NewRecorder()
	NewRouter(app, servicio, custodioDePrueba, DependenciasAuth{Identidad: identidadDeContextoFijo{id: id}}).ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/transferencias/maximo", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "LimitsUnavailable") {
		t.Fatalf("configuración HTTP: %d %s", w.Code, w.Body)
	}
}
