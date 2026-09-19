package httpserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// servicioConsentimientosDePrueba fija las filas y el error sin base de datos.
type servicioConsentimientosDePrueba struct {
	filas        []auth.Consentimiento
	errListar    error
	errRegistrar error
	recibido     auth.SolicitudDeConsentimientoNuevo
}

// Listar devuelve las filas fijadas.
func (s *servicioConsentimientosDePrueba) Listar(context.Context, uuid.UUID) ([]auth.Consentimiento, error) {
	return s.filas, s.errListar
}

// Registrar guarda la solicitud y devuelve el error fijado.
func (s *servicioConsentimientosDePrueba) Registrar(_ context.Context, solicitud auth.SolicitudDeConsentimientoNuevo) (auth.Consentimiento, error) {
	s.recibido = solicitud
	return auth.Consentimiento{Tipo: solicitud.Tipo}, s.errRegistrar
}

// routerConsentimientosDePrueba monta el router real con el handler sobre el servicio falso.
func routerConsentimientosDePrueba(servicio ServicioDeConsentimientos) http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: auth.UsuarioDePrueba{},
		Consent:   NuevoConsentimientosHandler(servicio),
	})
}

// pedirConsentimientos manda la petición por el router real, con o sin la credencial de prueba.
func pedirConsentimientos(t *testing.T, r http.Handler, metodo, ruta, cuerpo string, conCredencial bool) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if cuerpo != "" {
		body = strings.NewReader(cuerpo)
	}
	req := httptest.NewRequest(metodo, ruta, body)
	if conCredencial {
		req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// detalleDeConsentimiento devuelve el primer detalle de validación del envelope.
func detalleDeConsentimiento(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	env := decodeEnvelope(t, rec)
	apiErr, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error ausente en %q", rec.Body.String())
	}
	detalles, ok := apiErr["details"].([]any)
	if !ok || len(detalles) == 0 {
		t.Fatalf("details ausente en %q", rec.Body.String())
	}
	detalle, ok := detalles[0].(map[string]any)
	if !ok {
		t.Fatalf("detalle con forma rara en %q", rec.Body.String())
	}
	return detalle
}

// Los dos endpoints de consentimientos viven en el grupo autenticado.
func TestConsentimientosRequierenCredencial(t *testing.T) {
	r := routerConsentimientosDePrueba(&servicioConsentimientosDePrueba{})
	casos := []struct{ metodo, ruta, cuerpo string }{
		{http.MethodGet, "/v1/auth/consents", ""},
		{http.MethodPost, "/v1/auth/consents", `{"tipo":"tratamiento_datos","version_texto":"v1","aceptado":true,"accion":"otorgar"}`},
	}
	for _, caso := range casos {
		rec := pedirConsentimientos(t, r, caso.metodo, caso.ruta, caso.cuerpo, false)
		assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
	}
}

// Criterio 5: la lista devuelve el texto exacto, el origen y el orden ya resuelto por el servicio.
func TestListarConsentimientosDevuelveTextoYOrigen(t *testing.T) {
	metodo := "app"
	servicio := &servicioConsentimientosDePrueba{filas: []auth.Consentimiento{
		{Tipo: auth.TipoConsentimientoWhatsApp, VersionTexto: "v1", Texto: "[M] Autorizo WhatsApp.", Aceptado: true,
			Accion: auth.AccionConsentimientoOtorgar, Origen: "10.0.0.1", Metodo: metodo, Fecha: time.Now().UTC()},
		{Tipo: auth.TipoConsentimientoTratamiento, VersionTexto: "v1", Texto: "[M] Autorizo datos.", Aceptado: true,
			Accion: auth.AccionConsentimientoOtorgar, Origen: "10.0.0.1", Fecha: time.Now().UTC()},
	}}
	r := routerConsentimientosDePrueba(servicio)

	rec := pedirConsentimientos(t, r, http.MethodGet, "/v1/auth/consents", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	data, ok := decodeEnvelope(t, rec)["data"].([]any)
	if !ok || len(data) != 2 {
		t.Fatalf("data = %v, quiero dos filas", decodeEnvelope(t, rec)["data"])
	}
	primera, _ := data[0].(map[string]any)
	if primera["texto"] != "[M] Autorizo WhatsApp." || primera["origen"] != "10.0.0.1" || primera["accion"] != "otorgar" {
		t.Fatalf("primera fila = %+v, quiero el texto y el origen exactos", primera)
	}
}

// La lista vacía se serializa como data:[] y nunca como null.
func TestListarConsentimientosVacioEsArreglo(t *testing.T) {
	r := routerConsentimientosDePrueba(&servicioConsentimientosDePrueba{})

	rec := pedirConsentimientos(t, r, http.MethodGet, "/v1/auth/consents", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	data, ok := env["data"].([]any)
	if !ok || len(data) != 0 {
		t.Fatalf("data = %v, quiero []", env["data"])
	}
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Fatalf("cuerpo = %q, quiero data:[]", rec.Body.String())
	}
}

// Criterio 1: aceptado ausente no es autorización, la validación del contrato lo corta.
func TestCrearConsentimientoAceptadoAusenteEs422(t *testing.T) {
	r := routerConsentimientosDePrueba(&servicioConsentimientosDePrueba{})

	rec := pedirConsentimientos(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"tratamiento_datos","version_texto":"v1","accion":"otorgar"}`, true)
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	detalle := detalleDeConsentimiento(t, rec)
	if detalle["field"] != "aceptado" || detalle["rule"] != "required" {
		t.Fatalf("detalle = %+v, quiero aceptado/required", detalle)
	}
}

// Un tipo fuera del enum lo corta la validación del contrato.
func TestCrearConsentimientoTipoFueraDelEnum(t *testing.T) {
	r := routerConsentimientosDePrueba(&servicioConsentimientosDePrueba{})

	rec := pedirConsentimientos(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"inventado","version_texto":"v1","aceptado":true,"accion":"otorgar"}`, true)
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	detalle := detalleDeConsentimiento(t, rec)
	if detalle["field"] != "tipo" || detalle["rule"] != "oneof" {
		t.Fatalf("detalle = %+v, quiero tipo/oneof", detalle)
	}
}

// Criterio 1: el silencio no autoriza y la accion debe ser coherente con aceptado.
func TestCrearConsentimientoIncoherenteConAccion(t *testing.T) {
	r := routerConsentimientosDePrueba(&servicioConsentimientosDePrueba{})

	rec := pedirConsentimientos(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"tratamiento_datos","version_texto":"v1","aceptado":true,"accion":"revocar"}`, true)
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	detalle := detalleDeConsentimiento(t, rec)
	if detalle["field"] != "aceptado" || detalle["rule"] != "coherente_con_accion" {
		t.Fatalf("detalle = %+v, quiero aceptado/coherente_con_accion", detalle)
	}
}

// Una version inexistente para el tipo responde 422 vigente.
func TestCrearConsentimientoVersionInexistente(t *testing.T) {
	r := routerConsentimientosDePrueba(&servicioConsentimientosDePrueba{errRegistrar: auth.ErrVersionDeConsentimientoInexistente})

	rec := pedirConsentimientos(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"biometricos","version_texto":"v9","aceptado":true,"accion":"otorgar"}`, true)
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	detalle := detalleDeConsentimiento(t, rec)
	if detalle["field"] != "version_texto" || detalle["rule"] != "vigente" {
		t.Fatalf("detalle = %+v, quiero version_texto/vigente", detalle)
	}
}

// El camino feliz responde 201 con el ok del envelope y delega la solicitud con el método por defecto.
func TestCrearConsentimientoOk(t *testing.T) {
	servicio := &servicioConsentimientosDePrueba{}
	r := routerConsentimientosDePrueba(servicio)

	rec := pedirConsentimientos(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"biometricos","version_texto":"v1","aceptado":true,"accion":"otorgar"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quiero 201 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if servicio.recibido.Tipo != auth.TipoConsentimientoBiometricos || !servicio.recibido.Aceptado {
		t.Fatalf("solicitud = %+v, quiero biometricos aceptado", servicio.recibido)
	}
	if servicio.recibido.Origen == "" {
		t.Fatalf("origen = %q, quiero la IP canonicalizada", servicio.recibido.Origen)
	}
}

// P-13: revocar whatsapp_optin mientras es el unico canal responde 409 y no persiste.
func TestCrearConsentimientoRevocarWhatsappUnicoCanal(t *testing.T) {
	r := routerConsentimientosDePrueba(&servicioConsentimientosDePrueba{errRegistrar: auth.ErrWhatsappUnicoCanal})

	rec := pedirConsentimientos(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"whatsapp_optin","version_texto":"v1","aceptado":false,"accion":"revocar"}`, true)
	assertErrorEnvelope(t, rec, http.StatusConflict, "WhatsappUnicoCanal")
}

// Una accion ausente toma el default otorgar del contrato: con aceptado true registra la fila.
func TestCrearConsentimientoAccionAusenteEsOtorgar(t *testing.T) {
	servicio := &servicioConsentimientosDePrueba{}
	r := routerConsentimientosDePrueba(servicio)

	rec := pedirConsentimientos(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"biometricos","version_texto":"v1","aceptado":true}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quiero 201 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if !servicio.recibido.Aceptado {
		t.Fatalf("solicitud = %+v, quiero aceptado", servicio.recibido)
	}
}

// Una accion ausente es otorgar, y otorgar con aceptado false es incoherente.
func TestCrearConsentimientoAccionAusenteConAceptadoFalseEs422(t *testing.T) {
	servicio := &servicioConsentimientosDePrueba{}
	r := routerConsentimientosDePrueba(servicio)

	rec := pedirConsentimientos(t, r, http.MethodPost, "/v1/auth/consents",
		`{"tipo":"biometricos","version_texto":"v1","aceptado":false}`, true)
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	detalle := detalleDeConsentimiento(t, rec)
	if detalle["field"] != "aceptado" || detalle["rule"] != "coherente_con_accion" {
		t.Fatalf("detalle = %+v, quiero aceptado/coherente_con_accion", detalle)
	}
	if servicio.recibido.Tipo != "" {
		t.Fatalf("el servicio recibio %+v, quiero que no se llame", servicio.recibido)
	}
}

// Un error de listado desconocido sale como 500 genérico.
func TestListarConsentimientosErrorGenerico(t *testing.T) {
	r := routerConsentimientosDePrueba(&servicioConsentimientosDePrueba{errListar: errors.New("boom")})

	rec := pedirConsentimientos(t, r, http.MethodGet, "/v1/auth/consents", "", true)
	assertErrorEnvelope(t, rec, http.StatusInternalServerError, "InternalError")
}
