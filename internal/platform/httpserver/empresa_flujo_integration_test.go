//go:build integration

package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/contadores"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/denylist"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/otpsim"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// montajeDeEmpresa reúne el router con los servicios reales, el firmador y el adaptador de identidad.
type montajeDeEmpresa struct {
	router    http.Handler
	tokens    *auth.TokensDeAcceso
	identidad IdentidadDeLaPeticion
}

// pilaDeAuthDeEmpresa agrupa los colaboradores de auth que el flujo de empresa necesita.
type pilaDeAuthDeEmpresa struct {
	derivador      auth.Derivador
	eventos        auth.RegistradorEventos
	gestor         *auth.GestorDesafios
	activador      *auth.ActivadorDeCuenta
	servicioLogin  *auth.ServicioDeLogin
	servicioSesion *auth.ServicioDeSesion
	tokens         *auth.TokensDeAcceso
	denylist       auth.RevocadorDeSesiones
}

// datosDeAltaEmpresa es lo que el alta deja disponible para el resto del flujo.
type datosDeAltaEmpresa struct {
	representanteID string
	correo          string
	celular         string
	nit             string
	razonSocial     string
	contrasena      string
}

// respuestaSaldoDeIntegracion es el data de GET /saldo que afirma el contexto de empresa.
type respuestaSaldoDeIntegracion struct {
	Monto  string `json:"monto"`
	Moneda string `json:"moneda"`
}

// exigirSinError falla la prueba si el constructor devolvió error.
func exigirSinError(t *testing.T, err error, contexto string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", contexto, err)
	}
}

// decodificarData lee el objeto data del envelope exitoso en el tipo pedido.
func decodificarData[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("data no decodificable: %v (cuerpo %q)", err, rec.Body.String())
	}
	return envelope.Data
}

// construirPilaDeAuthDeEmpresa cablea registro, verificación, login y sesión sobre Postgres y Redis reales.
func construirPilaDeAuthDeEmpresa(t *testing.T, pool *pgxpool.Pool, cliente *redis.Client) pilaDeAuthDeEmpresa {
	t.Helper()
	derivador, err := auth.NuevoDerivadorArgon2(auth.ParametrosArgon2DePrueba())
	exigirSinError(t, err, "derivador")
	eventos := auth.NuevoRegistradorEventos(pool)
	canal, err := otpsim.Nuevo("123456")
	exigirSinError(t, err, "canal OTP")
	claves, err := auth.NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	exigirSinError(t, err, "claves")
	contadoresAuth, err := contadores.NuevoRedis(cliente)
	exigirSinError(t, err, "contadores")
	pepper := bytes.Repeat([]byte{0x22}, 32)
	gestor, err := auth.NuevoGestorDesafios(pool, canal, eventos, auth.NuevoLectorOptInDeConsentimientos(pool), pepper,
		auth.ConRacionamiento(contadoresAuth, claves))
	exigirSinError(t, err, "gestor de desafíos")
	activador, err := auth.NuevoActivadorDeCuenta(pool, gestor, auth.NuevoGestorDeEstados(eventos), pepper)
	exigirSinError(t, err, "activador")
	verificador, err := auth.NuevoVerificadorDeCredencialesPostgres(pool, derivador, contadoresAuth, claves, eventos, auth.ParametrosArgon2DePrueba())
	exigirSinError(t, err, "verificador")
	gestorSesiones := auth.NuevoGestorSesiones(pool, eventos)
	tokens := tokensDeIntegracion(t)
	servicioLogin, err := auth.NuevoServicioDeLogin(verificador, pool, gestorSesiones, tokens)
	exigirSinError(t, err, "servicio de login")
	denylistAuth, err := denylist.NuevoRedis(cliente)
	exigirSinError(t, err, "denylist")
	servicioSesion, err := auth.NuevoServicioDeSesion(pool, gestorSesiones, tokens, denylistAuth)
	exigirSinError(t, err, "servicio de sesión")
	return pilaDeAuthDeEmpresa{
		derivador: derivador, eventos: eventos, gestor: gestor, activador: activador,
		servicioLogin: servicioLogin, servicioSesion: servicioSesion, tokens: tokens, denylist: denylistAuth,
	}
}

// montajeDeEmpresaDeIntegracion arma el router completo con los handlers reales de empresa.
func montajeDeEmpresaDeIntegracion(t *testing.T, pool *pgxpool.Pool) montajeDeEmpresa {
	t.Helper()
	pila := construirPilaDeAuthDeEmpresa(t, pool, clienteRedisDeIntegracion(t))
	servicioNucleo := nucleo.NewServicio(pool, nil, nil, nil, nil)
	registrador, err := auth.NuevoRegistradorDePersonas(pool, pila.derivador, pila.eventos, pila.gestor, servicioNucleo)
	exigirSinError(t, err, "registrador")
	identidad := adaptadorJWTDePrueba{tokens: pila.tokens, denylist: pila.denylist}
	router := NewRouter(pool, servicioNucleo, custodioDePrueba, DependenciasAuth{
		Identidad:    identidad,
		Auth:         NuevoDispositivosHandler(pila.servicioSesion),
		Registro:     NuevoRegistroHandler(registrador),
		Verificacion: NuevoVerificacionHandler(pila.activador),
		Login:        NuevoLoginHandler(pila.servicioLogin),
		Refresh:      NuevoSesionHandler(pila.servicioSesion),
	})
	return montajeDeEmpresa{router: router, tokens: pila.tokens, identidad: identidad}
}

// cuerpoDeAltaEmpresa serializa un alta de empresa válida con el documento recibido.
func cuerpoDeAltaEmpresa(t *testing.T, datos datosDeAltaEmpresa, documento string) string {
	t.Helper()
	cuerpo := map[string]any{
		"tipo_cuenta":  "empresa",
		"razon_social": datos.razonSocial,
		"nit":          datos.nit,
		"correo":       datos.correo,
		"contrasena":   datos.contrasena,
		"celular":      datos.celular,
		"representante_legal": map[string]any{
			"nombre_completo":  "Ana Centinela",
			"tipo_documento":   "CC",
			"numero_documento": documento,
		},
		"consentimientos": []map[string]any{
			{"tipo": "tratamiento_datos", "version_texto": "v1", "aceptado": true},
			{"tipo": "whatsapp_optin", "version_texto": "v1", "aceptado": true},
		},
	}
	serializado, err := json.Marshal(cuerpo)
	exigirSinError(t, err, "serializar el alta de empresa")
	return string(serializado)
}

// cuerpoDeLoginEmpresa arma el login por celular con el contexto de empresa.
func cuerpoDeLoginEmpresa(datos datosDeAltaEmpresa) string {
	return `{"celular":"` + datos.celular + `","contrasena":"` + datos.contrasena + `","contexto":"empresa"}`
}

// cuerpoDeVerificacion arma la verificación con el celular y el código fijo del simulador.
func cuerpoDeVerificacion(celular string) string {
	return `{"celular":"` + celular + `","codigo":"123456"}`
}

// altaDeEmpresaCriterio6Y8 registra la empresa, exige el 409 genérico y verifica el celular del representante.
func altaDeEmpresaCriterio6Y8(t *testing.T, m montajeDeEmpresa, pool *pgxpool.Pool) datosDeAltaEmpresa {
	t.Helper()
	datos := datosDeAltaEmpresa{
		correo:      "emp-" + uuid.NewString() + "@centinela.test",
		celular:     celularUnicoDeEmpresa(),
		nit:         nitUnicoDeEmpresa(),
		razonSocial: "Centinela SAS",
		contrasena:  "contrasena-larga",
	}
	rec := pedirDeDispositivos(m.router, http.MethodPost, "/v1/auth/register", "", cuerpoDeAltaEmpresa(t, datos, "DOC-"+uuid.NewString()))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register empresa = %d, quiero 201 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	registro := decodificarData[api.Registro](t, rec)
	if string(registro.TipoCuenta) != "empresa" || string(registro.Estado) != "pendiente_verificacion" ||
		registro.RazonSocial == nil || *registro.RazonSocial != datos.razonSocial ||
		registro.Nit == nil || *registro.Nit != datos.nit || string(registro.Desafio.Proposito) != "registro" {
		t.Fatalf("registro = %+v, quiero empresa pendiente con razón social, NIT y desafío de registro", registro)
	}
	datos.representanteID = registro.Id.String()

	rechazoDeCorreoRepetidoEsGenerico(t, m, datos)
	exigirVerificacionActiva(t, m, pool, datos)
	return datos
}

// rechazoDeCorreoRepetidoEsGenerico prueba otro alta de empresa con el mismo correo y exige el 409 sin filtrar el dato.
func rechazoDeCorreoRepetidoEsGenerico(t *testing.T, m montajeDeEmpresa, datos datosDeAltaEmpresa) {
	t.Helper()
	otra := datosDeAltaEmpresa{
		correo:      datos.correo,
		celular:     celularUnicoDeEmpresa(),
		nit:         nitUnicoDeEmpresa(),
		razonSocial: "Otra SAS",
		contrasena:  "contrasena-larga",
	}
	rec := pedirDeDispositivos(m.router, http.MethodPost, "/v1/auth/register", "", cuerpoDeAltaEmpresa(t, otra, "DOC-"+uuid.NewString()))
	assertErrorEnvelope(t, rec, http.StatusConflict, "DuplicateIdentifier")
	if strings.Contains(rec.Body.String(), datos.correo) || strings.Contains(rec.Body.String(), datos.celular) {
		t.Fatalf("el 409 filtró el identificador repetido: %q", rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	apiErr, _ := env["error"].(map[string]any)
	if mensaje, _ := apiErr["message"].(string); mensaje != "The identifier is already registered." {
		t.Fatalf("mensaje del 409 = %q, quiero el genérico", mensaje)
	}
}

// exigirVerificacionActiva verifica el celular del representante, exige los códigos de respaldo y el rol intacto.
func exigirVerificacionActiva(t *testing.T, m montajeDeEmpresa, pool *pgxpool.Pool, datos datosDeAltaEmpresa) {
	t.Helper()
	rec := pedirDeDispositivos(m.router, http.MethodPost, "/v1/auth/register/verify-phone", "", cuerpoDeVerificacion(datos.celular))
	if rec.Code != http.StatusOK {
		t.Fatalf("verify-phone = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	verificacion := decodificarData[api.Verificacion](t, rec)
	if string(verificacion.Estado) != "activo" || verificacion.Id.String() != datos.representanteID ||
		verificacion.Celular != datos.celular || verificacion.CodigosRespaldo == nil || len(*verificacion.CodigosRespaldo) == 0 {
		t.Fatalf("verificación = %+v, quiero al representante activo con códigos de respaldo", verificacion)
	}
	if total := contarRolesVigentesDeEmpresa(t, pool, datos.representanteID); total != 1 {
		t.Fatalf("roles vigentes del representante = %d, quiero 1 intacto tras verify-phone", total)
	}
}

// sesionDeEmpresaCriterio7 hace login directo con contexto empresa y afirma el access y el usuario.
func sesionDeEmpresaCriterio7(t *testing.T, m montajeDeEmpresa, datos datosDeAltaEmpresa) api.Sesion {
	t.Helper()
	rec := pedirDeDispositivos(m.router, http.MethodPost, "/v1/auth/login", "", cuerpoDeLoginEmpresa(datos))
	if rec.Code != http.StatusOK {
		t.Fatalf("login empresa = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	sesion := decodificarData[api.Sesion](t, rec)
	if string(sesion.Usuario.TipoCuenta) != "empresa" || sesion.Usuario.RazonSocial == nil || *sesion.Usuario.RazonSocial != datos.razonSocial {
		t.Fatalf("usuario = %+v, quiero la empresa con razón social %q", sesion.Usuario, datos.razonSocial)
	}
	if sesion.Usuario.RolActivo == nil || *sesion.Usuario.RolActivo != "representante_legal" || !strings.HasPrefix(sesion.Usuario.ContextoActivo, "empresa:") {
		t.Fatalf("usuario = %+v, quiero rol representante_legal y contexto empresa", sesion.Usuario)
	}
	claims, err := m.tokens.Verificar(sesion.AccessToken)
	if err != nil || !strings.HasPrefix(claims.Contexto, "empresa:") || claims.Rol != "representante_legal" {
		t.Fatalf("claims = %+v (err %v), quiero ctx empresa y rol representante_legal", claims, err)
	}
	return sesion
}

// contextosYCambioDeContextoCriterio7 lista los contextos y alterna a usuario y de vuelta a empresa.
func contextosYCambioDeContextoCriterio7(t *testing.T, m montajeDeEmpresa, sesion api.Sesion, datos datosDeAltaEmpresa) (string, string) {
	t.Helper()
	rec := pedirDeDispositivos(m.router, http.MethodGet, "/v1/auth/contextos", sesion.AccessToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET contextos = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	contextos := decodificarData[[]api.Contexto](t, rec)
	if len(contextos) != 2 || string(contextos[0].Tipo) != "usuario" || string(contextos[1].Tipo) != "empresa" {
		t.Fatalf("contextos = %+v, quiero el personal y la empresa", contextos)
	}
	empresa := contextos[1]
	if empresa.Nit == nil || *empresa.Nit != datos.nit || empresa.Nombre != datos.razonSocial || !strings.HasPrefix(empresa.ContextoId, "empresa:") {
		t.Fatalf("contexto de empresa = %+v, quiero nit y razón social", empresa)
	}

	rec = pedirDeDispositivos(m.router, http.MethodPost, "/v1/auth/contexto", sesion.AccessToken, `{"contexto_id":"usuario"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST contexto usuario = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	activoUsuario := decodificarData[api.ContextoActivo](t, rec)
	claimsUsuario, err := m.tokens.Verificar(activoUsuario.AccessToken)
	if err != nil || claimsUsuario.Contexto != "usuario" || claimsUsuario.Rol != "" {
		t.Fatalf("claims al volver a usuario = %+v (err %v), quiero usuario sin rol", claimsUsuario, err)
	}

	rec = pedirDeDispositivos(m.router, http.MethodPost, "/v1/auth/contexto", activoUsuario.AccessToken, `{"contexto_id":"`+empresa.ContextoId+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST contexto empresa = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	activoEmpresa := decodificarData[api.ContextoActivo](t, rec)
	claimsEmpresa, err := m.tokens.Verificar(activoEmpresa.AccessToken)
	if err != nil || claimsEmpresa.Contexto != empresa.ContextoId || claimsEmpresa.Rol != "representante_legal" {
		t.Fatalf("claims al volver a empresa = %+v (err %v), quiero el contexto de empresa", claimsEmpresa, err)
	}
	return strings.TrimPrefix(empresa.ContextoId, "empresa:"), activoEmpresa.AccessToken
}

// saldoYIdentidadCriterio7 afirma que el núcleo ve el uuid de la empresa bajo el access de empresa.
func saldoYIdentidadCriterio7(t *testing.T, m montajeDeEmpresa, access, empresaID string) {
	t.Helper()
	rec := pedirDeDispositivos(m.router, http.MethodGet, "/saldo", access, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /saldo bajo empresa = %d, quiero 200 (cuerpo %q)", rec.Code, rec.Body.String())
	}
	saldo := decodificarData[respuestaSaldoDeIntegracion](t, rec)
	if saldo.Moneda != "COP" || saldo.Monto != "0" {
		t.Fatalf("saldo = %+v, quiero 0 COP de la empresa recién registrada", saldo)
	}

	req := httptest.NewRequest(http.MethodGet, "/saldo", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	sonda, err := m.identidad.Identidad(req)
	if err != nil {
		t.Fatalf("identidad del access de empresa: %v", err)
	}
	if sonda.UsuarioID != empresaID || sonda.Contexto != "empresa" || sonda.Rol != "representante_legal" {
		t.Fatalf("el núcleo recibió %+v, quiero el uuid de la empresa %s", sonda, empresaID)
	}
}

// contarRolesVigentesDeEmpresa cuenta como rol_auth los roles vigentes del representante.
func contarRolesVigentesDeEmpresa(t *testing.T, pool *pgxpool.Pool, representanteID string) int {
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
	if err := tx.QueryRow(t.Context(),
		`SELECT count(*) FROM auth.roles_de_parte WHERE parte_id = $1 AND rol = 'representante_legal' AND revocado_en IS NULL`,
		representanteID).Scan(&total); err != nil {
		t.Fatalf("contar roles vigentes: %v", err)
	}
	return total
}

// nitUnicoDeEmpresa genera un NIT colombiano válido y distinto por llamada.
func nitUnicoDeEmpresa() string {
	id := uuid.New()
	n := int64(id[0])<<32 | int64(id[1])<<24 | int64(id[2])<<16 | int64(id[3])<<8 | int64(id[4])
	return cuerpoDeNITDeEmpresa(fmt.Sprintf("%09d", n%1000000000))
}

// cuerpoDeNITDeEmpresa agrega el dígito de verificación DIAN al cuerpo recibido.
func cuerpoDeNITDeEmpresa(cuerpo string) string {
	pesos := []int{3, 7, 13, 17, 19, 23, 29, 37, 41, 43, 47, 53, 59, 67, 71}
	suma := 0
	for i := 0; i < len(cuerpo); i++ {
		suma += int(cuerpo[len(cuerpo)-1-i]-'0') * pesos[i]
	}
	digito := suma % 11
	if digito > 1 {
		digito = 11 - digito
	}
	return cuerpo + strconv.Itoa(digito)
}

// celularUnicoDeEmpresa genera un móvil colombiano válido y distinto por llamada.
func celularUnicoDeEmpresa() string {
	id := uuid.New()
	n := int64(id[0])<<24 | int64(id[1])<<16 | int64(id[2])<<8 | int64(id[3])
	return fmt.Sprintf("+57300%07d", n%10000000)
}

func TestHUAUT06FlujoDeEmpresaDePuntaAPunta(t *testing.T) {
	pool := abrirPoolDeIntegracion(t)
	m := montajeDeEmpresaDeIntegracion(t, pool)

	datos := altaDeEmpresaCriterio6Y8(t, m, pool)
	sesion := sesionDeEmpresaCriterio7(t, m, datos)
	empresaID, accessEmpresa := contextosYCambioDeContextoCriterio7(t, m, sesion, datos)
	saldoYIdentidadCriterio7(t, m, accessEmpresa, empresaID)
}
