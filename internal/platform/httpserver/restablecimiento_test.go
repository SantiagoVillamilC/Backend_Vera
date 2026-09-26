package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/google/uuid"
)

// verificadorRecuperacionFalso devuelve el token o el error configurados y registra lo recibido.
type verificadorRecuperacionFalso struct {
	token     string
	err       error
	challenge uuid.UUID
	codigos   []string
	respaldos []string
}

func (v *verificadorRecuperacionFalso) VerificarSegundoFactor(_ context.Context, challengeID uuid.UUID, codigo, codigoRespaldo, _ string) (string, error) {
	v.challenge = challengeID
	v.codigos = append(v.codigos, codigo)
	v.respaldos = append(v.respaldos, codigoRespaldo)
	return v.token, v.err
}

// restablecedorFalso devuelve el error configurado y registra el token y la contrasena recibidos.
type restablecedorFalso struct {
	err         error
	tokens      []string
	contrasenas []string
}

func (r *restablecedorFalso) RestablecerContrasena(_ context.Context, resetToken, nuevaContrasena string) error {
	r.tokens = append(r.tokens, resetToken)
	r.contrasenas = append(r.contrasenas, nuevaContrasena)
	return r.err
}

// routerConRestablecimiento arma el router con el handler del cierre inyectado, o el stub si es nil.
func routerConRestablecimiento(verificador *verificadorRecuperacionFalso, restablecedor *restablecedorFalso) http.Handler {
	var (
		verify RecuperacionVerifyHandler
		reset  RecuperacionResetHandler
	)
	if verificador != nil && restablecedor != nil {
		handler := NuevoRestablecimientoHandler(verificador, restablecedor)
		verify, reset = handler, handler
	}
	return NewRouter(nil, nucleo.NewServicio(nil, nil, nil, nil, nil), custodioDePrueba, DependenciasAuth{
		Identidad:    auth.UsuarioDePrueba{},
		Recuperar2FA: verify,
		ResetClave:   reset,
	})
}

// peticionDeVerify arma el POST de verificación del segundo método con el cuerpo dado.
func peticionDeVerify(t *testing.T, cuerpo string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/recovery/verify", strings.NewReader(cuerpo))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// peticionDeReset arma el POST de restablecimiento con el token y la contraseña dados.
func peticionDeReset(t *testing.T, resetToken, contrasena string) *http.Request {
	t.Helper()

	body, err := json.Marshal(map[string]string{"reset_token": resetToken, "nueva_contrasena": contrasena})
	if err != nil {
		t.Fatalf("serializar el cuerpo: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/auth/recovery/reset", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestVerifyRecuperacionSinSegundoMetodoResponde422(t *testing.T) {
	falso := &verificadorRecuperacionFalso{token: "no-deberia-importar"}
	rec := httptest.NewRecorder()

	routerConRestablecimiento(falso, &restablecedorFalso{}).ServeHTTP(rec, peticionDeVerify(t,
		`{"challenge_id":"`+uuid.NewString()+`","codigo":"123456"}`))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "segundo_metodo.tipo", "required")
}

func TestVerifyRecuperacionConTipoAjenoResponde422(t *testing.T) {
	falso := &verificadorRecuperacionFalso{token: "no-deberia-importar"}
	rec := httptest.NewRecorder()

	routerConRestablecimiento(falso, &restablecedorFalso{}).ServeHTTP(rec, peticionDeVerify(t,
		`{"challenge_id":"`+uuid.NewString()+`","codigo":"123456","segundo_metodo":{"tipo":"biometria","codigo":"ABC"}}`))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "segundo_metodo.tipo", "oneof")
}

func TestVerifyRecuperacionMapeaElSegundoFactorMalo(t *testing.T) {
	falso := &verificadorRecuperacionFalso{err: auth.ErrSegundoFactorRequerido}
	rec := httptest.NewRecorder()

	routerConRestablecimiento(falso, &restablecedorFalso{}).ServeHTTP(rec, peticionDeVerify(t,
		`{"challenge_id":"`+uuid.NewString()+`","codigo":"123456","segundo_metodo":{"tipo":"codigo_respaldo","codigo":"ABCD-EFGH-IJKL-MNOP"}}`))

	assertErrorEnvelope(t, rec, http.StatusUnauthorized, "SecondFactorRequired")
}

func TestVerifyRecuperacionCorrectoRespondeResetToken(t *testing.T) {
	falso := &verificadorRecuperacionFalso{token: "token-opaco-de-prueba"}
	rec := httptest.NewRecorder()

	routerConRestablecimiento(falso, &restablecedorFalso{}).ServeHTTP(rec, peticionDeVerify(t,
		`{"challenge_id":"`+uuid.NewString()+`","codigo":"123456","segundo_metodo":{"tipo":"codigo_respaldo","codigo":"ABCD-EFGH-IJKL-MNOP"}}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	datos, ok := env["data"].(map[string]any)
	if !ok {
		t.Fatalf("data ausente en %q", rec.Body.String())
	}
	if datos["reset_token"] != "token-opaco-de-prueba" {
		t.Fatalf("reset_token = %v, quiero token-opaco-de-prueba", datos["reset_token"])
	}
	if len(falso.respaldos) != 1 || falso.respaldos[0] != "ABCD-EFGH-IJKL-MNOP" {
		t.Fatalf("respaldos = %v, quiero el código de respaldo recibido", falso.respaldos)
	}
	if len(falso.codigos) != 1 || falso.codigos[0] != "123456" {
		t.Fatalf("codigos = %v, quiero el OTP recibido", falso.codigos)
	}
}

func TestResetContrasenaCortaResponde422(t *testing.T) {
	rec := httptest.NewRecorder()

	routerConRestablecimiento(&verificadorRecuperacionFalso{}, &restablecedorFalso{}).ServeHTTP(
		rec, peticionDeReset(t, "token", "1234567"))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "nueva_contrasena", "min")
}

func TestResetContrasenaLargaResponde422(t *testing.T) {
	rec := httptest.NewRecorder()

	routerConRestablecimiento(&verificadorRecuperacionFalso{}, &restablecedorFalso{}).ServeHTTP(
		rec, peticionDeReset(t, "token", strings.Repeat("a", 65)))

	assertErrorEnvelope(t, rec, http.StatusUnprocessableEntity, "ValidationError")
	assertDetalleDeValidacion(t, rec, "nueva_contrasena", "max")
}

func TestResetContrasenaUnicodeDe64RunasLlegaCompleta(t *testing.T) {
	larga := strings.Repeat("ñ", 64)
	falso := &restablecedorFalso{}
	rec := httptest.NewRecorder()

	routerConRestablecimiento(&verificadorRecuperacionFalso{}, falso).ServeHTTP(rec, peticionDeReset(t, "token", larga))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	if len(falso.contrasenas) != 1 || falso.contrasenas[0] != larga {
		t.Fatalf("contrasena recibida = %q, quiero las 64 runas completas", falso.contrasenas)
	}
	if utf8.RuneCountInString(falso.contrasenas[0]) != 64 {
		t.Fatalf("runas recibidas = %d, quiero 64", utf8.RuneCountInString(falso.contrasenas[0]))
	}
}

func TestResetTokenInvalidoResponde400(t *testing.T) {
	rec := httptest.NewRecorder()

	routerConRestablecimiento(&verificadorRecuperacionFalso{}, &restablecedorFalso{err: auth.ErrResetInvalido}).ServeHTTP(
		rec, peticionDeReset(t, "token-vencido", "contrasena-valida"))

	assertErrorEnvelope(t, rec, http.StatusBadRequest, "ResetInvalid")
}

func TestResetCorrectoNoEmiteTokens(t *testing.T) {
	falso := &restablecedorFalso{}
	rec := httptest.NewRecorder()

	routerConRestablecimiento(&verificadorRecuperacionFalso{}, falso).ServeHTTP(rec, peticionDeReset(t, "token", "contrasena-nueva"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	cuerpo := rec.Body.String()
	if strings.Contains(cuerpo, "access_token") || strings.Contains(cuerpo, "refresh_token") {
		t.Fatalf("el 200 trae tokens: %q", cuerpo)
	}
	if len(falso.tokens) != 1 || falso.tokens[0] != "token" {
		t.Fatalf("tokens = %v, quiero el reset_token recibido", falso.tokens)
	}
}

func TestResetYSinHandlerResponde501(t *testing.T) {
	casos := map[string]*http.Request{
		"verify": peticionDeVerify(t, `{"challenge_id":"`+uuid.NewString()+`","codigo":"123456","segundo_metodo":{"tipo":"codigo_respaldo","codigo":"ABC"}}`),
		"reset":  peticionDeReset(t, "token", "contrasena-valida"),
	}
	for nombre, req := range casos {
		t.Run(nombre, func(t *testing.T) {
			rec := httptest.NewRecorder()
			routerConRestablecimiento(nil, nil).ServeHTTP(rec, req)
			assertErrorEnvelope(t, rec, http.StatusNotImplemented, "NotImplemented")
		})
	}
}

// manejadorEnMemoria captura los registros de slog para inspeccionarlos.
type manejadorEnMemoria struct{ lineas *bytes.Buffer }

// Enabled acepta todos los niveles.
func (m manejadorEnMemoria) Enabled(context.Context, slog.Level) bool { return true }

// WithAttrs devuelve el mismo manejador: el test no usa atributos de grupo.
func (m manejadorEnMemoria) WithAttrs([]slog.Attr) slog.Handler { return m }

// WithGroup devuelve el mismo manejador: el test no usa grupos.
func (m manejadorEnMemoria) WithGroup(string) slog.Handler { return m }

// Handle escribe cada registro serializado en el buffer.
func (m manejadorEnMemoria) Handle(_ context.Context, r slog.Record) error {
	m.lineas.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		m.lineas.WriteString(a.Key)
		m.lineas.WriteString(a.Value.String())
		return true
	})
	m.lineas.WriteString("\n")
	return nil
}

func TestRestablecimientoNoLogueaSecretos(t *testing.T) {
	const (
		resetToken     = "reset-token-secreto-para-el-log"
		contrasena     = "contrasena-secreta-para-el-log"
		codigoRespaldo = "ABCD-EFGH-IJKL-MNOP"
	)

	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(manejadorEnMemoria{lineas: &buf}))
	defer slog.SetDefault(original)

	verificador := &verificadorRecuperacionFalso{token: resetToken}
	restablecedor := &restablecedorFalso{}
	router := routerConRestablecimiento(verificador, restablecedor)

	verify := httptest.NewRecorder()
	router.ServeHTTP(verify, peticionDeVerify(t,
		`{"challenge_id":"`+uuid.NewString()+`","codigo":"123456","segundo_metodo":{"tipo":"codigo_respaldo","codigo":"`+codigoRespaldo+`"}}`))
	reset := httptest.NewRecorder()
	router.ServeHTTP(reset, peticionDeReset(t, resetToken, contrasena))

	registrado := buf.String()
	for nombre, secreto := range map[string]string{
		"reset_token":     resetToken,
		"contrasena":      contrasena,
		"codigo respaldo": codigoRespaldo,
	} {
		if strings.Contains(registrado, secreto) {
			t.Fatalf("el log contiene el %s: %q", nombre, registrado)
		}
	}
}
