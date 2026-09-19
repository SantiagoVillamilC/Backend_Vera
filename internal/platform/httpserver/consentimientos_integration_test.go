//go:build integration

package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/identidad"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// sembrarParteDeConsentimiento siembra una parte del tipo pedido como rol_auth y devuelve su uuid.
func sembrarParteDeConsentimiento(t *testing.T, pool *pgxpool.Pool, tipo string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+auth.RolAuth); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if _, err := tx.Exec(t.Context(), "INSERT INTO auth.partes (id, tipo) VALUES ($1, $2)", id, tipo); err != nil {
		t.Fatalf("sembrar parte %s: %v", tipo, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id
}

// routerDeConsentimientoReal monta el handler real de consentimientos con la identidad de la parte.
func routerDeConsentimientoReal(pool *pgxpool.Pool, parteID uuid.UUID, contexto string) http.Handler {
	return NewRouter(pool, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: identidadDeContextoFijo{id: identidad.Identidad{
			UsuarioID: parteID.String(),
			SesionID:  uuid.NewString(),
			ParteID:   parteID.String(),
			Contexto:  contexto,
		}},
		Consent: NuevoConsentimientosHandler(auth.NuevoServicioDeConsentimientos(pool)),
	})
}

// pedirConsentimientoReal manda la petición por el router real con la credencial de prueba.
func pedirConsentimientoReal(t *testing.T, r http.Handler, metodo, ruta, cuerpo string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(metodo, ruta, strings.NewReader(cuerpo))
	req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// contarConsentimientosDeParte cuenta las filas de la parte, bajando a rol_auth para leer el esquema.
func contarConsentimientosDeParte(t *testing.T, pool *pgxpool.Pool, parteID uuid.UUID, tipo string) int {
	t.Helper()
	total := 0
	ejecutarComoRolAuth(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM auth.consentimientos WHERE parte_id = $1 AND ($2 = '' OR tipo = $2)`, parteID, tipo).Scan(&total)
	})
	return total
}

// sembrarFilaDeConsentimiento inserta una fila del log como rol_auth, para armar el historial sin pasar por el servicio.
func sembrarFilaDeConsentimiento(t *testing.T, pool *pgxpool.Pool, parteID uuid.UUID, tipo string, aceptado bool) {
	t.Helper()
	accion := auth.AccionConsentimientoRevocar
	if aceptado {
		accion = auth.AccionConsentimientoOtorgar
	}
	ejecutarComoRolAuth(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO auth.consentimientos (id, parte_id, tipo, version_texto, texto, aceptado, accion, actor, origen, user_agent, metodo)
			 VALUES ($1, $2, $3, 'v1', 'texto de prueba', $4, $5, $2, '10.0.0.1', 'test', 'app')`,
			uuid.New(), parteID, tipo, aceptado, accion)
		return err
	})
}

// ejecutarComoRolAuth abre una transaccion, baja a rol_auth, ejecuta el cuerpo y confirma.
func ejecutarComoRolAuth(t *testing.T, pool *pgxpool.Pool, cuerpo func(ctx context.Context, tx pgx.Tx) error) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+auth.RolAuth); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := cuerpo(t.Context(), tx); err != nil {
		t.Fatalf("cuerpo como rol_auth: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// intentarComoRolAuth ejecuta una sentencia como rol_auth y devuelve el error del motor.
func intentarComoRolAuth(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) error {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+auth.RolAuth); err != nil {
		t.Fatalf("set role: %v", err)
	}
	_, err = tx.Exec(t.Context(), sql, args...)
	return err
}

// El GET devuelve el texto exacto sembrado, el origen canonicalizado y el orden más reciente primero.
func TestConsentimientoIntegracionGETTextoYOrden(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	parte := sembrarParteDeConsentimiento(t, pool, "persona")
	r := routerDeConsentimientoReal(pool, parte, auth.ContextoUsuario)

	primera := pedirConsentimientoReal(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"tratamiento_datos","version_texto":"v1","aceptado":true,"accion":"otorgar"}`)
	if primera.Code != http.StatusCreated {
		t.Fatalf("primera alta: status = %d, quiero 201 (cuerpo %q)", primera.Code, primera.Body.String())
	}
	segunda := pedirConsentimientoReal(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"biometricos","version_texto":"v1","aceptado":true,"accion":"otorgar"}`)
	if segunda.Code != http.StatusCreated {
		t.Fatalf("segunda alta: status = %d, quiero 201 (cuerpo %q)", segunda.Code, segunda.Body.String())
	}

	lista := pedirConsentimientoReal(t, r, http.MethodGet, "/v1/auth/consents", "")
	if lista.Code != http.StatusOK {
		t.Fatalf("lista: status = %d, quiero 200 (cuerpo %q)", lista.Code, lista.Body.String())
	}
	data, ok := decodeEnvelope(t, lista)["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("data = %v, quiero dos filas", decodeEnvelope(t, lista)["data"])
	}
	top, _ := data[0].(map[string]any)
	if top["tipo"] != auth.TipoConsentimientoBiometricos {
		t.Fatalf("primera fila = %v, quiero biometricos (más reciente primero)", top["tipo"])
	}
	texto, _ := top["texto"].(string)
	if !strings.Contains(texto, "Autorizo el uso de mis datos biometricos") {
		t.Fatalf("texto = %q, quiero el exacto sembrado en textos_consentimiento", texto)
	}
	if top["origen"] == "" || top["origen"] == "127.0.0.1" {
		t.Fatalf("origen = %v, quiero una IP canonicalizada no vacía", top["origen"])
	}
}

// UPDATE y DELETE sobre auth.consentimientos fallan por permiso de rol_auth y por el trigger de inmutabilidad.
func TestConsentimientoIntegracionInmutable(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	parte := sembrarParteDeConsentimiento(t, pool, "persona")
	r := routerDeConsentimientoReal(pool, parte, auth.ContextoUsuario)

	alta := pedirConsentimientoReal(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"tratamiento_datos","version_texto":"v1","aceptado":true,"accion":"otorgar"}`)
	if alta.Code != http.StatusCreated {
		t.Fatalf("alta: status = %d, quiero 201 (cuerpo %q)", alta.Code, alta.Body.String())
	}

	errorUpdate := intentarComoRolAuth(t, pool, "UPDATE auth.consentimientos SET texto = 'pisado' WHERE parte_id = $1", parte)
	errorDelete := intentarComoRolAuth(t, pool, "DELETE FROM auth.consentimientos WHERE parte_id = $1", parte)
	t.Logf("UPDATE como rol_auth: %v", errorUpdate)
	t.Logf("DELETE como rol_auth: %v", errorDelete)
	if errorUpdate == nil || errorDelete == nil {
		t.Fatal("UPDATE y DELETE deben fallar: el log es append-only por grants y por trigger")
	}
	if !strings.Contains(errorUpdate.Error(), "permission denied") {
		t.Fatalf("UPDATE = %v, quiero permission denied de rol_auth", errorUpdate)
	}
	if !strings.Contains(errorDelete.Error(), "permission denied") {
		t.Fatalf("DELETE = %v, quiero permission denied de rol_auth", errorDelete)
	}
}

// El trigger BEFORE UPDATE OR DELETE aborta tambien para el dueño del esquema, que sí tiene el privilegio.
func TestConsentimientoIntegracionTriggerInmutable(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL no definida: sin el dueño del esquema no se puede aislar el trigger del grant")
	}
	pool := abrirPoolDeIntegracion(t)
	parte := sembrarParteDeConsentimiento(t, pool, "persona")
	sembrarFilaDeConsentimiento(t, pool, parte, auth.TipoConsentimientoTratamiento, true)

	dueno, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("abrir pool del dueño: %v", err)
	}
	t.Cleanup(dueno.Close)

	errorUpdate := ejecutarConsultaDelDueno(t, dueno, "UPDATE auth.consentimientos SET texto = 'pisado' WHERE parte_id = $1", parte)
	errorDelete := ejecutarConsultaDelDueno(t, dueno, "DELETE FROM auth.consentimientos WHERE parte_id = $1", parte)
	t.Logf("UPDATE como dueño: %v", errorUpdate)
	t.Logf("DELETE como dueño: %v", errorDelete)
	if errorUpdate == nil || errorDelete == nil {
		t.Fatal("el trigger debe abortar UPDATE y DELETE incluso para el dueño del esquema")
	}
	if !strings.Contains(errorUpdate.Error(), "append-only") {
		t.Fatalf("UPDATE = %v, quiero el mensaje del trigger append-only", errorUpdate)
	}
	if !strings.Contains(errorDelete.Error(), "append-only") {
		t.Fatalf("DELETE = %v, quiero el mensaje del trigger append-only", errorDelete)
	}
}

// ejecutarConsultaDelDueno ejecuta una sentencia con el rol dueño y devuelve el error del motor.
func ejecutarConsultaDelDueno(t *testing.T, dueno *pgxpool.Pool, sql string, args ...any) error {
	t.Helper()
	tx, err := dueno.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin dueño: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	_, err = tx.Exec(t.Context(), sql, args...)
	return err
}

// Revocar whatsapp_optin responde 409 por P-13 y no persiste la revocación: el opt-in sigue vigente.
func TestConsentimientoIntegracionRevocarWhatsappUnicoCanal(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	parte := sembrarParteDeConsentimiento(t, pool, "persona")
	r := routerDeConsentimientoReal(pool, parte, auth.ContextoUsuario)

	otorgar := pedirConsentimientoReal(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"whatsapp_optin","version_texto":"v1","aceptado":true,"accion":"otorgar"}`)
	if otorgar.Code != http.StatusCreated {
		t.Fatalf("otorgar: status = %d, quiero 201 (cuerpo %q)", otorgar.Code, otorgar.Body.String())
	}
	antes := contarConsentimientosDeParte(t, pool, parte, auth.TipoConsentimientoWhatsApp)

	revocar := pedirConsentimientoReal(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"whatsapp_optin","version_texto":"v1","aceptado":false,"accion":"revocar"}`)
	assertErrorEnvelope(t, revocar, http.StatusConflict, "WhatsappUnicoCanal")

	if despues := contarConsentimientosDeParte(t, pool, parte, auth.TipoConsentimientoWhatsApp); despues != antes {
		t.Fatalf("filas de whatsapp_optin = %d, quiero %d: el 409 no persiste", despues, antes)
	}
	lector := auth.NuevoLectorOptInDeConsentimientos(pool)
	vigente, err := lector.OptInVigente(t.Context(), parte)
	if err != nil {
		t.Fatalf("OptInVigente: %v", err)
	}
	if !vigente {
		t.Fatal("el opt-in debe seguir vigente tras el 409")
	}
}

// Revocar y volver a otorgar deja OptInVigente según la última fila, con el historial sembrado directo.
func TestConsentimientoIntegracionLectorOptInUltimaFila(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	parte := sembrarParteDeConsentimiento(t, pool, "persona")
	lector := auth.NuevoLectorOptInDeConsentimientos(pool)

	sembrarFilaDeConsentimiento(t, pool, parte, auth.TipoConsentimientoWhatsApp, true)
	if vigente, _ := lector.OptInVigente(t.Context(), parte); !vigente {
		t.Fatal("tras otorgar el opt-in debe estar vigente")
	}
	sembrarFilaDeConsentimiento(t, pool, parte, auth.TipoConsentimientoWhatsApp, false)
	if vigente, _ := lector.OptInVigente(t.Context(), parte); vigente {
		t.Fatal("tras revocar el opt-in debe ser false")
	}
	sembrarFilaDeConsentimiento(t, pool, parte, auth.TipoConsentimientoWhatsApp, true)
	if vigente, _ := lector.OptInVigente(t.Context(), parte); !vigente {
		t.Fatal("tras volver a otorgar el opt-in debe ser true")
	}
}

// Revocar biometricos se registra y no cambia nada del login: es explícito, separado y opcional.
func TestConsentimientoIntegracionBiometricosNoAfectaOptIn(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	parte := sembrarParteDeConsentimiento(t, pool, "persona")
	r := routerDeConsentimientoReal(pool, parte, auth.ContextoUsuario)

	otorgar := pedirConsentimientoReal(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"biometricos","version_texto":"v1","aceptado":true,"accion":"otorgar"}`)
	if otorgar.Code != http.StatusCreated {
		t.Fatalf("otorgar biometricos: status = %d (cuerpo %q)", otorgar.Code, otorgar.Body.String())
	}
	revocar := pedirConsentimientoReal(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"biometricos","version_texto":"v1","aceptado":false,"accion":"revocar"}`)
	if revocar.Code != http.StatusCreated {
		t.Fatalf("revocar biometricos: status = %d, quiero 201 (cuerpo %q)", revocar.Code, revocar.Body.String())
	}
	if total := contarConsentimientosDeParte(t, pool, parte, auth.TipoConsentimientoBiometricos); total != 2 {
		t.Fatalf("filas de biometricos = %d, quiero dos (otorgar y revocar)", total)
	}
	lector := auth.NuevoLectorOptInDeConsentimientos(pool)
	vigente, err := lector.OptInVigente(t.Context(), parte)
	if err != nil {
		t.Fatalf("OptInVigente: %v", err)
	}
	if vigente {
		t.Fatal("biometricos no enciende el opt-in de WhatsApp: debe quedar false")
	}
}

// Bajo contexto empresa el consentimiento es de la persona dueña de la sesión, nunca de la empresa.
func TestConsentimientoIntegracionContextoEmpresa(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	persona := sembrarParteDeConsentimiento(t, pool, "persona")
	empresa := sembrarParteDeConsentimiento(t, pool, "empresa")
	r := routerDeConsentimientoReal(pool, persona, auth.ContextoEmpresa)

	alta := pedirConsentimientoReal(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"tratamiento_datos","version_texto":"v1","aceptado":true,"accion":"otorgar"}`)
	if alta.Code != http.StatusCreated {
		t.Fatalf("alta empresa: status = %d, quiero 201 (cuerpo %q)", alta.Code, alta.Body.String())
	}

	partes := make([]uuid.UUID, 0, 2)
	ejecutarComoRolAuth(t, pool, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT parte_id FROM auth.consentimientos WHERE tipo = 'tratamiento_datos' AND parte_id = ANY($1)`,
			[]uuid.UUID{persona, empresa})
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			partes = append(partes, id)
		}
		return rows.Err()
	})
	if len(partes) != 1 || partes[0] != persona {
		t.Fatalf("partes = %v, quiero solo la persona %s", partes, persona)
	}
	if contarConsentimientosDeParte(t, pool, empresa, auth.TipoConsentimientoTratamiento) != 0 {
		t.Fatal("la empresa no consiente: no debe tener filas")
	}
}

// Dos otorgamientos concurrentes dejan dos filas: el log es append-only y no hay unicidad que las junte.
func TestConsentimientoIntegracionConcurrente(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	parte := sembrarParteDeConsentimiento(t, pool, "persona")
	servicio := auth.NuevoServicioDeConsentimientos(pool)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := servicio.Registrar(context.Background(), auth.SolicitudDeConsentimientoNuevo{
				ParteID:      parte,
				Tipo:         auth.TipoConsentimientoTratamiento,
				VersionTexto: "v1",
				Aceptado:     true,
				Metodo:       "app",
				Origen:       "10.0.0.1",
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("registrar concurrente: %v", err)
		}
	}

	if total := contarConsentimientosDeParte(t, pool, parte, auth.TipoConsentimientoTratamiento); total != 2 {
		t.Fatalf("total = %d, quiero dos filas append-only", total)
	}
}
