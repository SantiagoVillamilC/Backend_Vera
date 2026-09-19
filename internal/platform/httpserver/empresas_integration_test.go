//go:build integration

package httpserver

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// montajeDeEmpresasDeIntegracion arma el router con el registrador real y el handler de empresas.
func montajeDeEmpresasDeIntegracion(t *testing.T, pool *pgxpool.Pool) montajeDeEmpresa {
	t.Helper()
	pila := construirPilaDeAuthDeEmpresa(t, pool, clienteRedisDeIntegracion(t))
	servicioNucleo := nucleo.NewServicio(pool, nil, nil, nil, nil)
	registrador, err := auth.NuevoRegistradorDePersonas(pool, pila.derivador, pila.eventos, pila.gestor, servicioNucleo)
	exigirSinError(t, err, "registrador")
	identidad := adaptadorJWTDePrueba{tokens: pila.tokens, denylist: pila.denylist}
	router := NewRouter(pool, servicioNucleo, custodioDePrueba, DependenciasAuth{
		Identidad: identidad,
		Companies: NuevoEmpresasHandler(registrador),
	})
	return montajeDeEmpresa{router: router, tokens: pila.tokens, identidad: identidad}
}

// contarFilasDeEmpresa cuenta filas como rol_auth con la consulta y los argumentos recibidos.
func contarFilasDeEmpresa(t *testing.T, pool *pgxpool.Pool, consulta string, args ...any) int {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE rol_auth"); err != nil {
		t.Fatalf("SET LOCAL ROLE rol_auth: %v", err)
	}
	var total int
	if err := tx.QueryRow(t.Context(), consulta, args...).Scan(&total); err != nil {
		t.Fatalf("contar (%s): %v", consulta, err)
	}
	return total
}

// contarSubcuentasDeEmpresa cuenta las subcuentas del ledger con el rol dueno, que si ve el esquema nucleo.
func contarSubcuentasDeEmpresa(t *testing.T, titularID string) int {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatalf("DATABASE_URL es obligatoria para leer las subcuentas del nucleo")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("pool dueno: %v", err)
	}
	defer pool.Close()
	var total int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM nucleo.cuenta WHERE titular_id = $1`, titularID).Scan(&total); err != nil {
		t.Fatalf("contar subcuentas: %v", err)
	}
	return total
}

func TestCrearEmpresaAutenticadaDePuntaAPunta(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	m := montajeDeEmpresasDeIntegracion(t, pool)

	personaID, err := uuid.Parse(auth.SembrarParteDePruebaConID(t, pool, uuid.NewString()))
	if err != nil {
		t.Fatalf("uuid de la persona: %v", err)
	}
	access := firmarAccessDeIntegracion(t, m.tokens, personaID, uuid.New(), auth.ContextoUsuario, "")
	nit := nitUnicoDeEmpresa()

	rec := pedirDeDispositivos(m.router, http.MethodPost, "/v1/auth/companies", access, `{"razon_social":"Centinela SAS","nit":"`+nit+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST companies = %d, quiero 201 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	creada := decodificarData[api.EmpresaCreada](t, rec)
	if creada.Nit != nit || creada.RazonSocial != "Centinela SAS" || creada.EmpresaId == uuid.Nil {
		t.Fatalf("data = %+v, quiero empresa_id, nit y razon_social", creada)
	}
	empresaID := uuid.UUID(creada.EmpresaId)

	if total := contarFilasDeEmpresa(t, pool, `SELECT count(*) FROM auth.empresas WHERE parte_id = $1 AND nit = $2 AND razon_social = $3`,
		empresaID, nit, "Centinela SAS"); total != 1 {
		t.Errorf("empresas con la parte y el NIT = %d, quiero 1", total)
	}
	if total := contarFilasDeEmpresa(t, pool, `SELECT count(*) FROM auth.roles_de_parte WHERE parte_id = $1 AND representada_id = $2 AND rol = 'representante_legal' AND revocado_en IS NULL`,
		personaID, empresaID); total != 1 {
		t.Errorf("roles representante_legal vigentes = %d, quiero 1", total)
	}
	if total := contarSubcuentasDeEmpresa(t, empresaID.String()); total != 2 {
		t.Errorf("subcuentas de la empresa = %d, quiero 2", total)
	}

	antes := contarFilasDeEmpresa(t, pool, `SELECT count(*) FROM auth.partes`)
	rec = pedirDeDispositivos(m.router, http.MethodPost, "/v1/auth/companies", access, `{"razon_social":"Otra SAS","nit":"`+nit+`"}`)
	assertErrorEnvelope(t, rec, http.StatusConflict, "DuplicateIdentifier")
	if total := contarFilasDeEmpresa(t, pool, `SELECT count(*) FROM auth.partes`); total != antes {
		t.Errorf("partes = %d, quiero %d sin filas nuevas del segundo intento", total, antes)
	}
	if total := contarFilasDeEmpresa(t, pool, `SELECT count(*) FROM auth.empresas WHERE nit = $1`, nit); total != 1 {
		t.Errorf("empresas con el NIT = %d, quiero 1", total)
	}
}
