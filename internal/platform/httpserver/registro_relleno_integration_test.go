//go:build integration

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/otpsim"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	expresionUUID  = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	expresionFecha = regexp.MustCompile(`"fecha_creacion":"[^"]*"`)
)

// montajeDeRelleno reúne el router de registro con el servicio real, el relleno real y el simulador de OTP.
type montajeDeRelleno struct {
	router    http.Handler
	simulador *otpsim.Simulador
}

// montajeDeRellenoDeIntegracion arma solo la ruta pública de registro sobre Postgres y Redis reales.
func montajeDeRellenoDeIntegracion(t *testing.T, pool *pgxpool.Pool) montajeDeRelleno {
	t.Helper()

	pila := construirPilaDeAuthDeEmpresa(t, pool, clienteRedisDeIntegracion(t))
	canal, err := otpsim.Nuevo("123456")
	exigirSinError(t, err, "canal OTP")
	claves, err := auth.NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	exigirSinError(t, err, "claves")
	contadoresAuth, err := contadores.NuevoRedis(clienteRedisDeIntegracion(t))
	exigirSinError(t, err, "contadores")
	gestor, err := auth.NuevoGestorDesafios(pool, canal, pila.eventos, auth.NuevoLectorOptInDeConsentimientos(pool), bytes.Repeat([]byte{0x22}, 32),
		auth.ConRacionamiento(contadoresAuth, claves))
	exigirSinError(t, err, "gestor de desafíos")
	relleno, err := auth.NuevoRellenoDeRegistro(pool, pila.eventos, contadoresAuth, claves, bytes.Repeat([]byte{0x22}, 32))
	exigirSinError(t, err, "relleno del registro")
	registrador, err := auth.NuevoRegistradorDePersonas(pool, pila.derivador, pila.eventos, gestor, subcuentasSinNucleo{}, auth.ConRellenoDeRegistro(relleno))
	exigirSinError(t, err, "registrador")
	activador, err := auth.NuevoActivadorDeCuenta(pool, gestor, auth.NuevoGestorDeEstados(pila.eventos), bytes.Repeat([]byte{0x22}, 32))
	exigirSinError(t, err, "activador de cuenta")
	router := chi.NewRouter()
	router.Post("/v1/auth/register", registroDePersona(NuevoRegistroHandler(registrador)))
	router.Post("/v1/auth/register/verify-phone", verificacionDeCelular(NuevoVerificacionHandler(activador)))
	return montajeDeRelleno{router: conOrigenPropio(router), simulador: canal}
}

// conOrigenPropio da a cada montaje su propia IP de origen para que el tope por origen de Redis no cruce pruebas ni ejecuciones.
func conOrigenPropio(next http.Handler) http.Handler {
	id := uuid.New()
	remoto := fmt.Sprintf("10.%d.%d.%d:4000", id[0], id[1], id[2])
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = remoto
		next.ServeHTTP(w, r)
	})
}

// subcuentasSinNucleo abre las subcuentas sin tocar el ledger.
type subcuentasSinNucleo struct{}

func (subcuentasSinNucleo) AbrirSubcuentasDeUsuario(context.Context, string) (int64, error) {
	return 2, nil
}

// registrarPorHTTP envía el alta por r.ServeHTTP y devuelve la respuesta grabada.
func registrarPorHTTP(router http.Handler, cuerpo []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewReader(cuerpo))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// altaDePersonaJSON serializa un alta de persona válida con el documento recibido.
func altaDePersonaJSON(t *testing.T, correo, celular, documento string) []byte {
	t.Helper()

	cuerpo := cuerpoDeRegistro()
	cuerpo["correo"], cuerpo["celular"], cuerpo["numero_documento"] = correo, celular, documento
	serializado, err := json.Marshal(cuerpo)
	exigirSinError(t, err, "serializar el alta de persona")
	return serializado
}

// normalizarCuerpo reemplaza uuid, fecha y los datos propios de cada solicitud para comparar los bytes.
func normalizarCuerpo(cuerpo string, propios ...string) string {
	normal := cuerpo
	for i, propio := range propios {
		normal = strings.ReplaceAll(normal, propio, "<propio"+string(rune('A'+i))+">")
	}
	normal = expresionUUID.ReplaceAllString(normal, "<uuid>")
	return expresionFecha.ReplaceAllString(normal, `"fecha_creacion":"<fecha>"`)
}

// clavesEnOrden devuelve las claves del objeto data en el orden en que viajan por el cable.
func clavesEnOrden(t *testing.T, cuerpo string) []string {
	t.Helper()

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	exigirSinError(t, json.Unmarshal([]byte(cuerpo), &envelope), "decodificar el envelope")
	decodificador := json.NewDecoder(bytes.NewReader(envelope.Data))
	if _, err := decodificador.Token(); err != nil {
		t.Fatalf("abrir el objeto data: %v", err)
	}
	var claves []string
	for decodificador.More() {
		token, err := decodificador.Token()
		exigirSinError(t, err, "leer la clave")
		claves = append(claves, token.(string))
		var descartado json.RawMessage
		exigirSinError(t, decodificador.Decode(&descartado), "saltar el valor")
	}
	return claves
}

// exigirCuerposIndistinguibles exige status, cabecera, claves en orden y bytes iguales, y ningún token.
func exigirCuerposIndistinguibles(t *testing.T, nueva, repetida *httptest.ResponseRecorder, propiosNueva, propiosRepetida []string) {
	t.Helper()

	if nueva.Code != http.StatusCreated || repetida.Code != http.StatusCreated {
		t.Fatalf("status = %d/%d, quiero 201/201 (cuerpos %q / %q)", nueva.Code, repetida.Code, nueva.Body.String(), repetida.Body.String())
	}
	if nueva.Header().Get("Content-Type") != repetida.Header().Get("Content-Type") {
		t.Errorf("Content-Type = %q/%q, quiero el mismo", nueva.Header().Get("Content-Type"), repetida.Header().Get("Content-Type"))
	}
	clavesNueva, clavesRepetida := clavesEnOrden(t, nueva.Body.String()), clavesEnOrden(t, repetida.Body.String())
	if strings.Join(clavesNueva, ",") != strings.Join(clavesRepetida, ",") {
		t.Errorf("claves = %v / %v, quiero el mismo conjunto en el mismo orden", clavesNueva, clavesRepetida)
	}
	normalNueva, normalRepetida := normalizarCuerpo(nueva.Body.String(), propiosNueva...), normalizarCuerpo(repetida.Body.String(), propiosRepetida...)
	if normalNueva != normalRepetida {
		t.Errorf("cuerpos distintos tras reemplazar uuid, fecha y datos propios:\n nueva:    %s\n repetida: %s", normalNueva, normalRepetida)
	}
	for _, cuerpo := range []string{nueva.Body.String(), repetida.Body.String()} {
		for _, prohibido := range []string{"token", "access", "refresh", "contrasena", "valor_derivado", "codigo"} {
			if strings.Contains(strings.ToLower(cuerpo), prohibido) {
				t.Errorf("el cuerpo %s trae %q", cuerpo, prohibido)
			}
		}
	}
}

func TestRegistroPersonaDocumentoRepetidoEsIndistinguibleDelAltaNueva(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	m := montajeDeRellenoDeIntegracion(t, pool)
	documento := "DOC-" + uuid.NewString()
	correoA, celularA := "nueva-"+uuid.NewString()+"@centinela.test", celularUnicoDeEmpresa()
	correoB, celularB := "repetida-"+uuid.NewString()+"@centinela.test", celularUnicoDeEmpresa()

	nueva := registrarPorHTTP(m.router, altaDePersonaJSON(t, correoA, celularA, documento))
	repetida := registrarPorHTTP(m.router, altaDePersonaJSON(t, correoB, celularB, documento))

	exigirCuerposIndistinguibles(t, nueva, repetida, []string{correoA, celularA}, []string{correoB, celularB})
	if codigo, hay := m.simulador.UltimoCodigo(celularA); !hay || codigo != "123456" {
		t.Errorf("el alta nueva debió enviar el OTP al simulador, recibió (%q, %v)", codigo, hay)
	}
	if codigo, hay := m.simulador.UltimoCodigo(celularB); hay {
		t.Errorf("el documento repetido envió el código %q al simulador: debe quedar sin envío", codigo)
	}
	t.Logf("alta nueva:    %s", nueva.Body.String())
	t.Logf("doc repetido:  %s", repetida.Body.String())
}

func TestRegistroEmpresaDocumentoDelRepresentanteRepetidoEsIndistinguibleDelAltaNueva(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	m := montajeDeRellenoDeIntegracion(t, pool)
	documento := "DOC-" + uuid.NewString()
	datosA := datosDeAltaEmpresa{correo: "emp-nueva-" + uuid.NewString() + "@centinela.test", celular: celularUnicoDeEmpresa(), nit: nitUnicoDeEmpresa(), razonSocial: "Centinela SAS", contrasena: "contrasena-larga"}
	datosB := datosDeAltaEmpresa{correo: "emp-repetida-" + uuid.NewString() + "@centinela.test", celular: celularUnicoDeEmpresa(), nit: nitUnicoDeEmpresa(), razonSocial: "Centinela SAS", contrasena: "contrasena-larga"}

	nueva := registrarPorHTTP(m.router, []byte(cuerpoDeAltaEmpresa(t, datosA, documento)))
	repetida := registrarPorHTTP(m.router, []byte(cuerpoDeAltaEmpresa(t, datosB, documento)))

	exigirCuerposIndistinguibles(t, nueva, repetida, []string{datosA.correo, datosA.celular, datosA.nit}, []string{datosB.correo, datosB.celular, datosB.nit})
	if !strings.Contains(repetida.Body.String(), `"empresa_id"`) {
		t.Errorf("el relleno de empresa no trae empresa_id: %s", repetida.Body.String())
	}
	if _, hay := m.simulador.UltimoCodigo(datosB.celular); hay {
		t.Error("el documento repetido del representante envió un OTP")
	}
	t.Logf("empresa nueva:   %s", nueva.Body.String())
	t.Logf("empresa repetida: %s", repetida.Body.String())
}

// verificarCelularPorHTTP envía verify-phone con un código errado por r.ServeHTTP.
func verificarCelularPorHTTP(router http.Handler, celular string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register/verify-phone", strings.NewReader(`{"celular":"`+celular+`","codigo":"000000"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestVerifyPhoneEsIndistinguiblePorHTTPConDocumentoNuevoOExistente(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	m := montajeDeRellenoDeIntegracion(t, pool)
	documento := "DOC-" + uuid.NewString()
	celularExistente, celularNuevo := celularUnicoDeEmpresa(), celularUnicoDeEmpresa()
	for _, alta := range [][]byte{
		altaDePersonaJSON(t, "duena-"+uuid.NewString()+"@centinela.test", celularUnicoDeEmpresa(), documento),
		altaDePersonaJSON(t, "existente-"+uuid.NewString()+"@centinela.test", celularExistente, documento),
		altaDePersonaJSON(t, "nuevo-"+uuid.NewString()+"@centinela.test", celularNuevo, "DOC-"+uuid.NewString()),
	} {
		if rec := registrarPorHTTP(m.router, alta); rec.Code != http.StatusCreated {
			t.Fatalf("alta = %d, quiero 201 (cuerpo %q)", rec.Code, rec.Body.String())
		}
	}

	for nombre, celular := range map[string]string{"documento existente": celularExistente, "documento nuevo": celularNuevo} {
		for intento := 1; intento <= 3; intento++ {
			if rec := verificarCelularPorHTTP(m.router, celular); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"OtpInvalid"`) {
				t.Fatalf("%s, intento %d: %d %s, quiero 400 OtpInvalid", nombre, intento, rec.Code, rec.Body.String())
			}
		}
		if rec := verificarCelularPorHTTP(m.router, celular); rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), `"OtpTooManyAttempts"`) {
			t.Fatalf("%s, cuarto intento: %d %s, quiero 429 OtpTooManyAttempts", nombre, rec.Code, rec.Body.String())
		}
	}
}

func TestCorreoTomadoDaElMismoConflictoPorHTTPConDocumentoNuevoOExistente(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	m := montajeDeRellenoDeIntegracion(t, pool)
	documento, correoTomado := "DOC-"+uuid.NewString(), "tomado-"+uuid.NewString()+"@centinela.test"
	if rec := registrarPorHTTP(m.router, altaDePersonaJSON(t, correoTomado, celularUnicoDeEmpresa(), documento)); rec.Code != http.StatusCreated {
		t.Fatalf("alta que deja el correo tomado = %d, quiero 201 (cuerpo %q)", rec.Code, rec.Body.String())
	}

	existente := registrarPorHTTP(m.router, altaDePersonaJSON(t, correoTomado, celularUnicoDeEmpresa(), documento))
	nuevo := registrarPorHTTP(m.router, altaDePersonaJSON(t, correoTomado, celularUnicoDeEmpresa(), "DOC-"+uuid.NewString()))

	assertErrorEnvelope(t, existente, http.StatusConflict, "DuplicateIdentifier")
	assertErrorEnvelope(t, nuevo, http.StatusConflict, "DuplicateIdentifier")
	if existente.Body.String() != nuevo.Body.String() {
		t.Errorf("cuerpos distintos:\n documento existente: %s\n documento nuevo:     %s", existente.Body.String(), nuevo.Body.String())
	}
}
