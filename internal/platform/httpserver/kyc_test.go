package httpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/kyc"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
)

// servicioKYCDePrueba permite fijar el estado y el error de verificación sin base de datos.
type servicioKYCDePrueba struct {
	estado    kyc.Estado
	errEstado error
	errVerif  error
	verNivel  string
}

// Estado devuelve el estado fijado.
func (s *servicioKYCDePrueba) Estado(context.Context, string) (kyc.Estado, error) {
	return s.estado, s.errEstado
}

// Verificar registra el nivel pedido y devuelve el error fijado.
func (s *servicioKYCDePrueba) Verificar(_ context.Context, _, nivel, _ string) error {
	s.verNivel = nivel
	return s.errVerif
}

// routerKYCDePrueba monta el router real con el handler de KYC sobre el servicio falso.
func routerKYCDePrueba(servicio ServicioDeKYC) http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad: auth.UsuarioDePrueba{},
		KYC:       NuevoKYC(servicio),
	})
}

// pedirKYC manda la petición por el router real, con o sin la credencial de prueba.
func pedirKYC(t *testing.T, r http.Handler, metodo, ruta, cuerpo string, conCredencial bool) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if cuerpo != "" {
		body = strings.NewReader(cuerpo)
	}
	req := httptest.NewRequestWithContext(t.Context(), metodo, ruta, body)
	if conCredencial {
		req.Header.Set(auth.CabeceraUsuarioDePrueba, "1")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// detalleDeKYC devuelve el primer detalle de validación del envelope.
func detalleDeKYC(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
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

// Los dos endpoints de KYC viven en el grupo autenticado.
func TestKYCRequiereCredencial(t *testing.T) {
	r := routerKYCDePrueba(&servicioKYCDePrueba{})
	casos := []struct{ metodo, ruta, cuerpo string }{
		{http.MethodGet, "/v1/kyc/estado", ""},
		{http.MethodPost, "/v1/kyc/verificacion", `{"nivel":"pleno"}`},
	}
	for _, caso := range casos {
		rec := pedirKYC(t, r, caso.metodo, caso.ruta, caso.cuerpo, false)
		assertErrorEnvelope(t, rec, http.StatusUnauthorized, "Unauthorized")
	}
}

// Criterio 6: el estado responde producto, estado y un objeto de límites vacío porque auth no calcula dinero.
func TestKYCEstadoRespondeProductoYLimitesVacio(t *testing.T) {
	servicio := &servicioKYCDePrueba{estado: kyc.Estado{Producto: kyc.ProductoBajoMonto, EstadoKYC: kyc.EstadoBasico}}
	r := routerKYCDePrueba(servicio)

	rec := pedirKYC(t, r, http.MethodGet, "/v1/kyc/estado", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	data, ok := decodeEnvelope(t, rec)["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente en %q", rec.Body.String())
	}
	if data["producto"] != kyc.ProductoBajoMonto || data["estado_kyc"] != kyc.EstadoBasico {
		t.Fatalf("data = %+v, quiero bajo_monto/basico", data)
	}
	limites, ok := data["limites"].(map[string]any)
	if !ok || len(limites) != 0 {
		t.Fatalf("limites = %v, quiero el objeto vacío", data["limites"])
	}
}

// Un nivel fuera del enum lo corta la validación del contrato.
func TestKYCVerificacionNivelFueraDelEnum(t *testing.T) {
	r := routerKYCDePrueba(&servicioKYCDePrueba{})

	rec := pedirKYC(t, r, http.MethodPost, "/v1/kyc/verificacion", `{"nivel":"inventado"}`, true)
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	detalle := detalleDeKYC(t, rec)
	if detalle["field"] != "nivel" || detalle["rule"] != "oneof" {
		t.Fatalf("detalle = %+v, quiero nivel/oneof", detalle)
	}
}

// H5: el bajo monto es solo de personas naturales.
func TestKYCVerificacionBajoMontoEnEmpresa(t *testing.T) {
	r := routerKYCDePrueba(&servicioKYCDePrueba{errVerif: kyc.ErrNivelNoAplicaEmpresa})

	rec := pedirKYC(t, r, http.MethodPost, "/v1/kyc/verificacion", `{"nivel":"bajo_monto"}`, true)
	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	detalle := detalleDeKYC(t, rec)
	if detalle["field"] != "nivel" || detalle["rule"] != "no_aplica_empresa" {
		t.Fatalf("detalle = %+v, quiero nivel/no_aplica_empresa", detalle)
	}
}

// El camino feliz responde 200 con el ok del envelope y delega el nivel al servicio.
func TestKYCVerificacionOk(t *testing.T) {
	servicio := &servicioKYCDePrueba{}
	r := routerKYCDePrueba(servicio)

	rec := pedirKYC(t, r, http.MethodPost, "/v1/kyc/verificacion", `{"nivel":"pleno"}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if servicio.verNivel != kyc.NivelPleno {
		t.Fatalf("nivel delegado = %q, quiero %q", servicio.verNivel, kyc.NivelPleno)
	}
	data, ok := decodeEnvelope(t, rec)["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente en %q", rec.Body.String())
	}
	if mensaje, _ := data["mensaje"].(string); mensaje == "" {
		t.Fatalf("mensaje = %v, quiero un texto", data["mensaje"])
	}
}
