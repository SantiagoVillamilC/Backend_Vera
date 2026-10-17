// Package httpserver monta la capa HTTP de autenticación sobre chi: el router, sus middleware y los
// handlers que implementan el contrato OpenAPI.
package httpserver

import (
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/copilot"
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
	ListConsents(http.ResponseWriter, *http.Request)
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

// DependenciasAuth agrupa los colaboradores del módulo de autenticación que main inyecta.
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
}

// servidorV1 implementa api.ServerInterface delegando cada método al handler de su módulo.
type servidorV1 struct {
	// ProveedorIA queda reservado al orquestador; los stubs nunca lo invocan.
	ProveedorIA   copilot.ProveedorIA
	auth          AuthHandler
	kyc           KYCHandler
	consent       ConsentHandler
	phoneChange   PhoneChangeHandler
	companies     CompaniesHandler
	nucleo        NucleoHandler
	destinatarios *DestinatariosHandler
}

func (s servidorV1) ConsultarDestinatario(w http.ResponseWriter, r *http.Request, _ api.ConsultarDestinatarioParams) {
	if s.destinatarios == nil {
		sinImplementar(w, r)
		return
	}
	s.destinatarios.ConsultarDestinatario(w, r)
}

// Los metodos del asistente Vera aun no tienen handler: responden 501 dentro del envelope.
// Los implementan las tarjetas de la epica del asistente.

func (s servidorV1) EnviarMensajeVera(w http.ResponseWriter, r *http.Request) {
	sinImplementar(w, r)
}

func (s servidorV1) EnviarMensajeVozVera(w http.ResponseWriter, r *http.Request) {
	sinImplementar(w, r)
}

func (s servidorV1) ListarConversacionesVera(w http.ResponseWriter, r *http.Request, _ api.ListarConversacionesVeraParams) {
	sinImplementar(w, r)
}

func (s servidorV1) BorrarConversacionesVera(w http.ResponseWriter, r *http.Request) {
	sinImplementar(w, r)
}

func (s servidorV1) BorrarConversacionVera(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	sinImplementar(w, r)
}

func (s servidorV1) ListarMensajesConversacionVera(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID, _ api.ListarMensajesConversacionVeraParams) {
	sinImplementar(w, r)
}

func (s servidorV1) SolicitarCodigoAccionVera(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	sinImplementar(w, r)
}

func (s servidorV1) ConfirmarAccionVera(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	sinImplementar(w, r)
}

func (s servidorV1) CancelarAccionVera(w http.ResponseWriter, r *http.Request, _ openapi_types.UUID) {
	sinImplementar(w, r)
}

var _ api.ServerInterface = servidorV1{}

// sinImplementar responde 501 dentro del envelope para un método que todavía no tiene handler.
func sinImplementar(w http.ResponseWriter, _ *http.Request) {
	api.WriteError(w, http.StatusNotImplemented, "NotImplemented", "Endpoint not implemented yet.", nil)
}

func (s servidorV1) ListConsents(w http.ResponseWriter, r *http.Request) {
	if s.consent == nil {
		sinImplementar(w, r)
		return
	}
	s.consent.ListConsents(w, r)
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
