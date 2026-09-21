package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// registradorDePrueba captura la solicitud y devuelve una cuenta pendiente o el error configurado.
type registradorDePrueba struct {
	err              error
	errEmpresa       error
	capturado        auth.SolicitudDeRegistro
	capturadoEmpresa auth.SolicitudDeRegistroEmpresa
}

func (f *registradorDePrueba) RegistrarPersona(_ context.Context, solicitud auth.SolicitudDeRegistro) (auth.Registro, error) {
	f.capturado = solicitud
	if f.err != nil {
		return auth.Registro{}, f.err
	}
	return auth.Registro{
		ParteID:        uuid.New(),
		Correo:         auth.NormalizarCorreo(solicitud.Correo),
		Celular:        solicitud.Celular,
		NombreCompleto: solicitud.NombreCompleto,
		Estado:         auth.EstadoPendienteVerificacion,
		FechaCreacion:  time.Now().UTC(),
		Desafio:        auth.Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: auth.PropositoRegistro},
	}, nil
}

func (f *registradorDePrueba) RegistrarEmpresa(_ context.Context, solicitud auth.SolicitudDeRegistroEmpresa) (auth.RegistroEmpresa, error) {
	f.capturadoEmpresa = solicitud
	if f.errEmpresa != nil {
		return auth.RegistroEmpresa{}, f.errEmpresa
	}
	return auth.RegistroEmpresa{
		EmpresaParteID:       uuid.New(),
		RepresentanteParteID: uuid.New(),
		CredencialID:         uuid.New(),
		RazonSocial:          solicitud.RazonSocial,
		NIT:                  solicitud.NIT,
		Correo:               auth.NormalizarCorreo(solicitud.Correo),
		Celular:              solicitud.Celular,
		Estado:               auth.EstadoPendienteVerificacion,
		FechaCreacion:        time.Now().UTC(),
		Desafio:              auth.Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: auth.PropositoRegistro},
	}, nil
}

// routerDeRegistro monta solo la ruta pública de registro para probarla con r.ServeHTTP.
func routerDeRegistro(handler RegistroHandler) http.Handler {
	r := chi.NewRouter()
	r.Post("/v1/auth/register", registroDePersona(handler))
	return r
}

// cuerpoDeRegistro arma un alta de persona válida que cada caso ajusta.
func cuerpoDeRegistro() map[string]any {
	return map[string]any{
		"tipo_cuenta":      "persona",
		"correo":           "ana@centinela.test",
		"contrasena":       "contrasena-larga",
		"celular":          "+573001234567",
		"nombre_completo":  "Ana Centinela",
		"tipo_documento":   "CC",
		"numero_documento": "1020304050",
		"consentimientos": []map[string]any{
			{"tipo": "tratamiento_datos", "version_texto": "v1", "aceptado": true},
			{"tipo": "whatsapp_optin", "version_texto": "v1", "aceptado": true},
		},
	}
}

func TestRegistroHandlerCasosDeCriterio(t *testing.T) {
	tests := map[string]struct {
		cuerpo     func() map[string]any
		err        error
		wantStatus int
		wantCode   string
		verificar  func(t *testing.T, fake *registradorDePrueba, rec *httptest.ResponseRecorder)
	}{
		"criterio 1 campos faltantes": {
			cuerpo:     func() map[string]any { return map[string]any{"tipo_cuenta": "persona"} },
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "ValidationError",
		},
		"criterio 2 correo invalido": {
			cuerpo: func() map[string]any {
				c := cuerpoDeRegistro()
				c["correo"] = "no-es-correo"
				return c
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "ValidationError",
		},
		"criterio 3 correo internacionalizado": {
			cuerpo: func() map[string]any {
				c := cuerpoDeRegistro()
				c["correo"] = "José@Ejemplo.com"
				return c
			},
			wantStatus: http.StatusCreated,
			verificar: func(t *testing.T, _ *registradorDePrueba, rec *httptest.ResponseRecorder) {
				t.Helper()
				datos := datosDeRespuesta(t, rec)
				if datos["correo"] != "josé@ejemplo.com" {
					t.Errorf("correo = %v, quiero normalizado josé@ejemplo.com", datos["correo"])
				}
			},
		},
		"criterio 4 nombre invalido": {
			cuerpo:     cuerpoDeRegistro,
			err:        auth.ErrNombreInvalido,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "ValidationError",
			verificar:  exigirDetail(t, "nombre_completo", "nombre"),
		},
		"criterio 5 contrasena corta": {
			cuerpo: func() map[string]any {
				c := cuerpoDeRegistro()
				c["contrasena"] = "corta"
				return c
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "ValidationError",
			verificar:  exigirDetail(t, "contrasena", "min"),
		},
		"criterio 6 contrasena de 64 runas unicode": {
			cuerpo: func() map[string]any {
				c := cuerpoDeRegistro()
				c["contrasena"] = strings.Repeat("á", 64)
				return c
			},
			wantStatus: http.StatusCreated,
			verificar: func(t *testing.T, fake *registradorDePrueba, _ *httptest.ResponseRecorder) {
				t.Helper()
				if runas := utf8.RuneCountInString(fake.capturado.Contrasena); runas != 64 {
					t.Errorf("contrasena capturada con %d runas, quiero 64 sin truncar", runas)
				}
			},
		},
		"criterio 7 campo confirmar_contrasena": {
			cuerpo: func() map[string]any {
				c := cuerpoDeRegistro()
				c["confirmar_contrasena"] = "contrasena-larga"
				return c
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "BadRequest",
		},
		"criterio 14 celular invalido": {
			cuerpo:     cuerpoDeRegistro,
			err:        auth.ErrCelularInvalido,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "ValidationError",
			verificar:  exigirDetail(t, "celular", "e164"),
		},
		"consentimientos ausentes": {
			cuerpo: func() map[string]any {
				c := cuerpoDeRegistro()
				c["consentimientos"] = []map[string]any{{"tipo": "biometricos", "version_texto": "v1", "aceptado": true}}
				return c
			},
			err:        auth.ErrConsentimientoRequerido,
			wantStatus: http.StatusForbidden,
			wantCode:   "ConsentRequired",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &registradorDePrueba{err: tc.err}
			cuerpo, err := json.Marshal(tc.cuerpo())
			if err != nil {
				t.Fatalf("serializar el cuerpo: %v", err)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/register", bytes.NewReader(cuerpo))
			rec := httptest.NewRecorder()

			routerDeRegistro(NuevoRegistroHandler(fake)).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, quiero %d (cuerpo %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" {
				assertErrorEnvelope(t, rec, tc.wantStatus, tc.wantCode)
			}
			if tc.verificar != nil {
				tc.verificar(t, fake, rec)
			}
		})
	}
}

// datosDeRespuesta lee el objeto data del envelope exitoso.
func datosDeRespuesta(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	env := decodeEnvelope(t, rec)
	datos, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente o con forma rara en %q", rec.Body.String())
	}
	return datos
}

// exigirDetail comprueba el primer detalle de validación con su campo y regla.
func exigirDetail(_ *testing.T, campo, regla string) func(*testing.T, *registradorDePrueba, *httptest.ResponseRecorder) {
	return func(t *testing.T, _ *registradorDePrueba, rec *httptest.ResponseRecorder) {
		t.Helper()
		env := decodeEnvelope(t, rec)
		apiErr, _ := env["error"].(map[string]any)
		details, ok := apiErr["details"].([]any)
		if !ok || len(details) == 0 {
			t.Fatalf("quiero details, obtuve %q", rec.Body.String())
		}
		primer, _ := details[0].(map[string]any)
		if primer["field"] != campo || primer["rule"] != regla {
			t.Fatalf("detail = %v, quiero %s/%s", primer, campo, regla)
		}
	}
}

// cuerpoDeRegistroEmpresa arma un alta de empresa válida que cada caso ajusta.
func cuerpoDeRegistroEmpresa() map[string]any {
	return map[string]any{
		"tipo_cuenta":  "empresa",
		"razon_social": "Centinela SAS",
		"nit":          "9001234568",
		"correo":       "empresa@centinela.test",
		"contrasena":   "contrasena-larga",
		"celular":      "+573001234567",
		"representante_legal": map[string]any{
			"nombre_completo":  "Ana Centinela",
			"tipo_documento":   "CC",
			"numero_documento": "1020304050",
		},
		"consentimientos": []map[string]any{
			{"tipo": "tratamiento_datos", "version_texto": "v1", "aceptado": true},
			{"tipo": "whatsapp_optin", "version_texto": "v1", "aceptado": true},
		},
	}
}

// exigirDetailAnidado comprueba que details incluye el campo pedido con su regla.
func exigirDetailAnidado(_ *testing.T, campo, regla string) func(*testing.T, *registradorDePrueba, *httptest.ResponseRecorder) {
	return func(t *testing.T, _ *registradorDePrueba, rec *httptest.ResponseRecorder) {
		t.Helper()
		env := decodeEnvelope(t, rec)
		apiErr, _ := env["error"].(map[string]any)
		details, _ := apiErr["details"].([]any)
		for _, detalle := range details {
			campos, _ := detalle.(map[string]any)
			if campos["field"] == campo && campos["rule"] == regla {
				return
			}
		}
		t.Fatalf("details = %v, quiero %s/%s", details, campo, regla)
	}
}

func TestRegistroHandlerEmpresaCasosDeCriterio(t *testing.T) {
	tests := map[string]struct {
		cuerpo     func() map[string]any
		errEmpresa error
		wantStatus int
		wantCode   string
		verificar  func(t *testing.T, fake *registradorDePrueba, rec *httptest.ResponseRecorder)
	}{
		"1 campos faltantes con details anidados": {
			cuerpo:     func() map[string]any { return map[string]any{"tipo_cuenta": "empresa"} },
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "ValidationError",
			verificar:  exigirDetailAnidado(t, "representante_legal.nombre_completo", "required"),
		},
		"2 nit sin digito de verificacion": {
			cuerpo: func() map[string]any {
				c := cuerpoDeRegistroEmpresa()
				c["nit"] = "900123456"
				return c
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "ValidationError",
			verificar:  exigirDetailAnidado(t, "nit", "nit"),
		},
		"2 nit con digito de verificacion malo": {
			cuerpo: func() map[string]any {
				c := cuerpoDeRegistroEmpresa()
				c["nit"] = "9001234569"
				return c
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "ValidationError",
			verificar:  exigirDetailAnidado(t, "nit", "nit"),
		},
		"empresa valida 201 con razon social nit y desafio": {
			cuerpo:     cuerpoDeRegistroEmpresa,
			wantStatus: http.StatusCreated,
			verificar: func(t *testing.T, fake *registradorDePrueba, rec *httptest.ResponseRecorder) {
				t.Helper()
				datos := datosDeRespuesta(t, rec)
				if datos["tipo_cuenta"] != "empresa" || datos["razon_social"] != "Centinela SAS" || datos["nit"] != "9001234568" {
					t.Fatalf("data = %v, quiero empresa con razón social y NIT", datos)
				}
				if datos["id"] == nil || datos["desafio"] == nil {
					t.Fatalf("data = %v, quiero id y desafío", datos)
				}
				if fake.capturadoEmpresa.RepresentanteNombre != "Ana Centinela" {
					t.Fatalf("representante capturado = %q", fake.capturadoEmpresa.RepresentanteNombre)
				}
			},
		},
		"empresa con NIT repetido responde 409 generico": {
			cuerpo:     cuerpoDeRegistroEmpresa,
			errEmpresa: auth.ErrIdentificadorEnUso,
			wantStatus: http.StatusConflict,
			wantCode:   "DuplicateIdentifier",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &registradorDePrueba{errEmpresa: tc.errEmpresa}
			cuerpo, err := json.Marshal(tc.cuerpo())
			if err != nil {
				t.Fatalf("serializar el cuerpo: %v", err)
			}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/register", bytes.NewReader(cuerpo))
			rec := httptest.NewRecorder()

			routerDeRegistro(NuevoRegistroHandler(fake)).ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, quiero %d (cuerpo %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" {
				assertErrorEnvelope(t, rec, tc.wantStatus, tc.wantCode)
			}
			if tc.verificar != nil {
				tc.verificar(t, fake, rec)
			}
		})
	}
}
