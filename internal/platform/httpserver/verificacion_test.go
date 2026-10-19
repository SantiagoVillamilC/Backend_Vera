package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/google/uuid"
)

// activadorDePrueba captura la llamada y devuelve la activacion o el error configurados.
type activadorDePrueba struct {
	err              error
	capturadoCelular string
	capturadoCodigo  string
	activacion       auth.Activacion
}

// ActivarCuenta implementa la interfaz que consume el handler.
func (f *activadorDePrueba) ActivarCuenta(_ context.Context, celular, codigo string) (auth.Activacion, error) {
	f.capturadoCelular, f.capturadoCodigo = celular, codigo
	if f.err != nil {
		return auth.Activacion{}, f.err
	}
	return f.activacion, nil
}

// routerConVerificacion arma el router completo con el handler de activacion inyectado.
func routerConVerificacion(handler VerificacionHandler) http.Handler {
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad:    auth.UsuarioDePrueba{},
		Verificacion: handler,
	})
}

// peticionDeVerificacion arma el POST de activacion con el celular y el codigo indicados.
func peticionDeVerificacion(t *testing.T, celular, codigo string) *http.Request {
	t.Helper()

	body, err := json.Marshal(map[string]string{"celular": celular, "codigo": codigo})
	if err != nil {
		t.Fatalf("serializar el cuerpo: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/register/verify-phone", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// codigosDeRespaldoDePrueba emite diez codigos con la forma legible esperada.
func codigosDeRespaldoDePrueba() []string {
	codigos := make([]string, 0, 10)
	for i := range 10 {
		codigos = append(codigos, "ABCD-EFGH-IJKL-"+string(rune('A'+i))+"NOP")
	}
	return codigos
}

func TestVerificarCelularActivaYDevuelveLosCodigos(t *testing.T) {
	parteID := uuid.New()
	codigos := codigosDeRespaldoDePrueba()
	falso := &activadorDePrueba{activacion: auth.Activacion{ParteID: parteID, Celular: "+573001234567", CodigosRespaldo: codigos}}
	rec := httptest.NewRecorder()

	routerConVerificacion(NuevoVerificacionHandler(falso)).ServeHTTP(rec, peticionDeVerificacion(t, "+573001234567", "123456"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if falso.capturadoCelular != "+573001234567" || falso.capturadoCodigo != "123456" {
		t.Errorf("capturado = (%s, %q), quiero (+573001234567, 123456)", falso.capturadoCelular, falso.capturadoCodigo)
	}
	data := datosDeRespuesta(t, rec)
	if data["id"] != parteID.String() {
		t.Errorf("id = %v, quiero %s", data["id"], parteID)
	}
	if data["celular"] != "+573001234567" {
		t.Errorf("celular = %v, quiero +573001234567", data["celular"])
	}
	if _, hayCorreo := data["correo"]; hayCorreo {
		t.Errorf("la respuesta no debe traer correo: %v", data)
	}
	if data["estado"] != string(api.VerificacionEstadoActivo) {
		t.Errorf("estado = %v, quiero activo", data["estado"])
	}
	lista, ok := data["codigos_respaldo"].([]any)
	if !ok || len(lista) != 10 {
		t.Fatalf("codigos_respaldo = %v, quiero 10", data["codigos_respaldo"])
	}
	if lista[0] != codigos[0] {
		t.Errorf("primer codigo = %v, quiero %s", lista[0], codigos[0])
	}
	if strings.Contains(rec.Body.String(), "access_token") || strings.Contains(rec.Body.String(), "refresh_token") {
		t.Error("la activacion no debe emitir tokens")
	}
}

func TestVerificarCelularMapeaErroresAlEnvelope(t *testing.T) {
	casos := map[string]struct {
		err    error
		status int
		code   string
	}{
		"desafio invalido":    {err: auth.ErrDesafioInvalido, status: http.StatusBadRequest, code: "ChallengeInvalid"},
		"codigo invalido":     {err: auth.ErrCodigoInvalido, status: http.StatusBadRequest, code: "OtpInvalid"},
		"codigo expirado":     {err: auth.ErrCodigoExpirado, status: http.StatusBadRequest, code: "OtpExpired"},
		"demasiados intentos": {err: auth.ErrDemasiadosIntentos, status: http.StatusTooManyRequests, code: "OtpTooManyAttempts"},
		"conflicto de estado": {err: auth.ErrConflictoDeEstado, status: http.StatusBadRequest, code: "ChallengeInvalid"},
		"fail closed":         {err: auth.ConFailClosed(context.DeadlineExceeded), status: http.StatusServiceUnavailable, code: "AuthUnavailable"},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			falso := &activadorDePrueba{err: caso.err}
			rec := httptest.NewRecorder()

			routerConVerificacion(NuevoVerificacionHandler(falso)).ServeHTTP(rec, peticionDeVerificacion(t, "+573001234567", "123456"))

			assertErrorEnvelope(t, rec, caso.status, caso.code)
		})
	}
}

func TestVerificarCelularSinDesafioResponde400ChallengeInvalid(t *testing.T) {
	falso := &activadorDePrueba{err: auth.ErrDesafioInvalido}
	rec := httptest.NewRecorder()

	routerConVerificacion(NuevoVerificacionHandler(falso)).ServeHTTP(rec, peticionDeVerificacion(t, "+573009999999", "123456"))

	assertErrorEnvelope(t, rec, http.StatusBadRequest, "ChallengeInvalid")
}

func TestVerificarCelularCodigoMaloTresVecesResponde429(t *testing.T) {
	falso := &activadorDePrueba{}
	router := routerConVerificacion(NuevoVerificacionHandler(falso))

	for intento := 1; intento <= 3; intento++ {
		falso.err = auth.ErrCodigoInvalido
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, peticionDeVerificacion(t, "+573001234567", "000000"))
		assertErrorEnvelope(t, rec, http.StatusBadRequest, "OtpInvalid")
	}
	falso.err = auth.ErrDemasiadosIntentos
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, peticionDeVerificacion(t, "+573001234567", "000000"))
	assertErrorEnvelope(t, rec, http.StatusTooManyRequests, "OtpTooManyAttempts")
}

func TestVerificarCelularMalformadoResponde422ConReglaE164(t *testing.T) {
	falso := &activadorDePrueba{}
	rec := httptest.NewRecorder()

	routerConVerificacion(NuevoVerificacionHandler(falso)).ServeHTTP(rec, peticionDeVerificacion(t, "3001234567", "123456"))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "celular", "e164")
}

func TestVerificarCelularSinCelularResponde422(t *testing.T) {
	falso := &activadorDePrueba{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/register/verify-phone", strings.NewReader(`{"codigo":"123456"}`))
	req.Header.Set("Content-Type", "application/json")

	routerConVerificacion(NuevoVerificacionHandler(falso)).ServeHTTP(rec, req)

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "celular", "required")
}

// capturadorSlog acumula el texto de cada registro de slog para inspeccionarlo.
type capturadorSlog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Enabled acepta todos los niveles.
func (h *capturadorSlog) Enabled(context.Context, slog.Level) bool { return true }

// Handle anota el mensaje y los atributos del registro.
func (h *capturadorSlog) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.buf.WriteString(r.Message)
	h.buf.WriteByte('\n')
	r.Attrs(func(a slog.Attr) bool {
		h.buf.WriteString(a.Value.String())
		h.buf.WriteByte('\n')
		return true
	})
	return nil
}

// WithAttrs ignora los atributos de grupo.
func (h *capturadorSlog) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup ignora el grupo.
func (h *capturadorSlog) WithGroup(string) slog.Handler { return h }

// texto devuelve el contenido acumulado.
func (h *capturadorSlog) texto() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.buf.String()
}

func TestVerificarCelularNoLogueaLosCodigosDeRespaldo(t *testing.T) {
	capturador := &capturadorSlog{}
	previo := slog.Default()
	slog.SetDefault(slog.New(capturador))
	t.Cleanup(func() { slog.SetDefault(previo) })

	codigos := codigosDeRespaldoDePrueba()
	falso := &activadorDePrueba{activacion: auth.Activacion{ParteID: uuid.New(), Celular: "+573001234567", CodigosRespaldo: codigos}}
	rec := httptest.NewRecorder()

	routerConVerificacion(NuevoVerificacionHandler(falso)).ServeHTTP(rec, peticionDeVerificacion(t, "+573001234567", "123456"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	registros := capturador.texto()
	if !strings.Contains(rec.Body.String(), codigos[0]) {
		t.Fatal("el codigo no viajo en la respuesta: la prueba de fuga seria vacia")
	}
	for _, codigo := range codigos {
		if strings.Contains(registros, codigo) {
			t.Errorf("el log contiene el codigo de respaldo %q", codigo)
		}
	}
}
