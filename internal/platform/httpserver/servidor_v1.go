// Package httpserver monta la capa HTTP de autenticación sobre chi: el router, sus middleware y los
// handlers que implementan el contrato OpenAPI.
package httpserver

import (
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/redis/go-redis/v9"
)

// AuthHandler agrupa los endpoints autenticados de sesiones y contextos que el módulo cuelga del contrato.
type AuthHandler interface {
	Logout(http.ResponseWriter, *http.Request)
	LogoutAll(http.ResponseWriter, *http.Request)
	ListSessions(http.ResponseWriter, *http.Request)
	DeleteSession(http.ResponseWriter, *http.Request, openapi_types.UUID)
	ListContextos(http.ResponseWriter, *http.Request)
	SetContexto(http.ResponseWriter, *http.Request)
}

// KYCHandler agrupa los dos endpoints autenticados del módulo de KYC.
type KYCHandler interface {
	KycEstado(http.ResponseWriter, *http.Request)
	KycVerificacion(http.ResponseWriter, *http.Request)
}

// ConsentHandler agrupa los dos endpoints autenticados del módulo de consentimientos.
type ConsentHandler interface {
	ListConsents(http.ResponseWriter, *http.Request, api.ListConsentsParams)
	CreateConsent(http.ResponseWriter, *http.Request)
}

// DesafiosHandler agrupa el handler público del reenvío de OTP.
type DesafiosHandler interface {
	ResendOtp(http.ResponseWriter, *http.Request)
}

// RegistroHandler atiende el alta pública de persona y de empresa.
type RegistroHandler interface {
	Register(http.ResponseWriter, *http.Request)
}

// VerificacionHandler atiende la verificación pública del celular y la activación.
type VerificacionHandler interface {
	VerificarCelular(http.ResponseWriter, *http.Request)
}

// LoginHandler atiende el desenlace público del inicio de sesión.
type LoginHandler interface {
	Login(http.ResponseWriter, *http.Request)
}

// RefreshHandler atiende la rotación pública del refresh.
type RefreshHandler interface {
	Refresh(http.ResponseWriter, *http.Request)
}

// PhoneChangeHandler atiende la solicitud y la confirmación autenticadas del cambio de número.
type PhoneChangeHandler interface {
	PhoneChangeRequest(http.ResponseWriter, *http.Request)
	PhoneChangeConfirm(http.ResponseWriter, *http.Request)
}

// CompaniesHandler atiende la creación autenticada de empresa.
type CompaniesHandler interface {
	CreateCompany(http.ResponseWriter, *http.Request)
}

// NucleoHandler agrupa las siete operaciones del núcleo que van al server generado. *nucleo.Handler
// ya las implementa con esta misma firma, así que el adaptador es un pass-through delgado.
// ConfirmarDeposito queda fuera a propósito: cuelga a mano bajo RequireCustodio en router.go.
type NucleoHandler interface {
	Saldo(http.ResponseWriter, *http.Request)
	MontoMaximo(http.ResponseWriter, *http.Request)
	Movimientos(http.ResponseWriter, *http.Request)
	CrearDeposito(http.ResponseWriter, *http.Request)
	Deposito(http.ResponseWriter, *http.Request)
	Transferir(http.ResponseWriter, *http.Request)
	TransferirInterbancaria(http.ResponseWriter, *http.Request)
}

// InteligenciaHandler agrupa las operaciones del contexto de inteligencia.
type InteligenciaHandler interface {
	ListarMovimientosInteligencia(http.ResponseWriter, *http.Request, api.ListarMovimientosInteligenciaParams)
	CorregirMovimientoInteligencia(http.ResponseWriter, *http.Request, string)
	ResumenInteligencia(http.ResponseWriter, *http.Request, api.ResumenInteligenciaParams)
	ListarAvisosInteligencia(http.ResponseWriter, *http.Request, api.ListarAvisosInteligenciaParams)
	DescartarAvisoInteligencia(http.ResponseWriter, *http.Request, string)
}

// DependenciasAuth agrupa los colaboradores que main inyecta en el router.
type DependenciasAuth struct {
	Identidad     IdentidadDeLaPeticion
	Auth          AuthHandler
	KYC           KYCHandler
	Consent       ConsentHandler
	Redis         *redis.Client
	CanalOTP      auth.CanalOTP
	Contadores    auth.Contadores
	Desafios      DesafiosHandler
	Registro      RegistroHandler
	Verificacion  VerificacionHandler
	Login         LoginHandler
	Refresh       RefreshHandler
	Recuperacion  RecuperacionHandler
	Firmador      *auth.TokensDeAcceso
	Denylist      auth.RevocadorDeSesiones
	Subcuentas    auth.AbridorDeSubcuentas
	Recuperar2FA  RecuperacionVerifyHandler
	ResetClave    RecuperacionResetHandler
	PhoneChange   PhoneChangeHandler
	Companies     CompaniesHandler
	Destinatarios *DestinatariosHandler
	Inteligencia  InteligenciaHandler
	Metricas      MetricasDelRouter
}

// servidorV1 implementa api.ServerInterface delegando cada método al handler de su módulo.
type servidorV1 struct {
	// Vera reúne los servicios del asistente autenticado.
	Vera          DependenciasVera
	auth          AuthHandler
	kyc           KYCHandler
	consent       ConsentHandler
	phoneChange   PhoneChangeHandler
	companies     CompaniesHandler
	nucleo        NucleoHandler
	destinatarios *DestinatariosHandler
	inteligencia  InteligenciaHandler
}

func (s servidorV1) ConsultarDestinatario(w http.ResponseWriter, r *http.Request, _ api.ConsultarDestinatarioParams) {
	if s.destinatarios == nil {
		sinImplementar(w, r)
		return
	}
	s.destinatarios.ConsultarDestinatario(w, r)
}

func (s servidorV1) ListarMovimientosInteligencia(w http.ResponseWriter, r *http.Request, params api.ListarMovimientosInteligenciaParams) {
	if s.inteligencia == nil {
		sinImplementar(w, r)
		return
	}
	s.inteligencia.ListarMovimientosInteligencia(w, r, params)
}

func (s servidorV1) ResumenInteligencia(w http.ResponseWriter, r *http.Request, params api.ResumenInteligenciaParams) {
	if s.inteligencia == nil {
		sinImplementar(w, r)
		return
	}
	s.inteligencia.ResumenInteligencia(w, r, params)
}

func (s servidorV1) CorregirMovimientoInteligencia(w http.ResponseWriter, r *http.Request, transaccionID string) {
	if s.inteligencia == nil {
		sinImplementar(w, r)
		return
	}
	s.inteligencia.CorregirMovimientoInteligencia(w, r, transaccionID)
}

func (s servidorV1) ListarAvisosInteligencia(w http.ResponseWriter, r *http.Request, params api.ListarAvisosInteligenciaParams) {
	if s.inteligencia == nil {
		sinImplementar(w, r)
		return
	}
	s.inteligencia.ListarAvisosInteligencia(w, r, params)
}

func (s servidorV1) DescartarAvisoInteligencia(w http.ResponseWriter, r *http.Request, id string) {
	if s.inteligencia == nil {
		sinImplementar(w, r)
		return
	}
	s.inteligencia.DescartarAvisoInteligencia(w, r, id)
}

var _ api.ServerInterface = servidorV1{}

// sinImplementar responde 501 dentro del envelope para un método que todavía no tiene handler.
func sinImplementar(w http.ResponseWriter, _ *http.Request) {
	api.WriteError(w, http.StatusNotImplemented, "NotImplemented", "Endpoint not implemented yet.", nil)
}

func (s servidorV1) ListConsents(w http.ResponseWriter, r *http.Request, params api.ListConsentsParams) {
	if s.consent == nil {
		sinImplementar(w, r)
		return
	}
	s.consent.ListConsents(w, r, params)
}

func (s servidorV1) CreateConsent(w http.ResponseWriter, r *http.Request) {
	if s.consent == nil {
		sinImplementar(w, r)
		return
	}
	s.consent.CreateConsent(w, r)
}

func (s servidorV1) CreateCompany(w http.ResponseWriter, r *http.Request) {
	if s.companies == nil {
		sinImplementar(w, r)
		return
	}
	s.companies.CreateCompany(w, r)
}

func (s servidorV1) SetContexto(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.SetContexto(w, r)
}

func (s servidorV1) ListContextos(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.ListContextos(w, r)
}

func (s servidorV1) Logout(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.Logout(w, r)
}

func (s servidorV1) LogoutAll(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.LogoutAll(w, r)
}

func (s servidorV1) PhoneChangeConfirm(w http.ResponseWriter, r *http.Request) {
	if s.phoneChange == nil {
		sinImplementar(w, r)
		return
	}
	s.phoneChange.PhoneChangeConfirm(w, r)
}

func (s servidorV1) PhoneChangeRequest(w http.ResponseWriter, r *http.Request) {
	if s.phoneChange == nil {
		sinImplementar(w, r)
		return
	}
	s.phoneChange.PhoneChangeRequest(w, r)
}

func (s servidorV1) ListSessions(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.ListSessions(w, r)
}

func (s servidorV1) DeleteSession(w http.ResponseWriter, r *http.Request, sessionID openapi_types.UUID) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.DeleteSession(w, r, sessionID)
}

func (s servidorV1) KycEstado(w http.ResponseWriter, r *http.Request) {
	if s.kyc == nil {
		sinImplementar(w, r)
		return
	}
	s.kyc.KycEstado(w, r)
}

func (s servidorV1) KycVerificacion(w http.ResponseWriter, r *http.Request) {
	if s.kyc == nil {
		sinImplementar(w, r)
		return
	}
	s.kyc.KycVerificacion(w, r)
}

func (s servidorV1) Saldo(w http.ResponseWriter, r *http.Request) {
	if s.nucleo == nil {
		sinImplementar(w, r)
		return
	}
	s.nucleo.Saldo(w, r)
}

func (s servidorV1) MontoMaximo(w http.ResponseWriter, r *http.Request) {
	if s.nucleo == nil {
		sinImplementar(w, r)
		return
	}
	s.nucleo.MontoMaximo(w, r)
}

// Movimientos descarta params: el handler ya lee limit y cursor directo de r.URL.Query().
func (s servidorV1) Movimientos(w http.ResponseWriter, r *http.Request, _ api.MovimientosParams) {
	if s.nucleo == nil {
		sinImplementar(w, r)
		return
	}
	s.nucleo.Movimientos(w, r)
}

func (s servidorV1) CrearDeposito(w http.ResponseWriter, r *http.Request) {
	if s.nucleo == nil {
		sinImplementar(w, r)
		return
	}
	s.nucleo.CrearDeposito(w, r)
}

// Deposito descarta id: el handler ya lo lee de chi.URLParam(r, "id").
func (s servidorV1) Deposito(w http.ResponseWriter, r *http.Request, _ string) {
	if s.nucleo == nil {
		sinImplementar(w, r)
		return
	}
	s.nucleo.Deposito(w, r)
}

func (s servidorV1) Transferir(w http.ResponseWriter, r *http.Request) {
	if s.nucleo == nil {
		sinImplementar(w, r)
		return
	}
	s.nucleo.Transferir(w, r)
}

func (s servidorV1) TransferirInterbancaria(w http.ResponseWriter, r *http.Request) {
	if s.nucleo == nil {
		sinImplementar(w, r)
		return
	}
	s.nucleo.TransferirInterbancaria(w, r)
}
