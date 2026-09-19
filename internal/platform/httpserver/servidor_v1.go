package httpserver

import (
	"net/http"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/api"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/redis/go-redis/v9"
)

// AuthHandler agrupa los endpoints autenticados que el módulo de autenticación cuelga del contrato.
type AuthHandler interface {
	Logout(http.ResponseWriter, *http.Request)
	LogoutAll(http.ResponseWriter, *http.Request)
	ListSessions(http.ResponseWriter, *http.Request)
	DeleteSession(http.ResponseWriter, *http.Request, openapi_types.UUID)
	ListContextos(http.ResponseWriter, *http.Request)
	SetContexto(http.ResponseWriter, *http.Request)
	PhoneChangeRequest(http.ResponseWriter, *http.Request)
	PhoneChangeConfirm(http.ResponseWriter, *http.Request)
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

// RegistroHandler atiende el alta pública de persona.
type RegistroHandler interface {
	Register(http.ResponseWriter, *http.Request)
}

// VerificacionHandler atiende la verificación pública del celular y la activación.
type VerificacionHandler interface {
	VerificarCelular(http.ResponseWriter, *http.Request)
}

// DependenciasAuth agrupa los colaboradores del módulo de autenticación que main inyecta.
type DependenciasAuth struct {
	Identidad    IdentidadDeLaPeticion
	Auth         AuthHandler
	KYC          KYCHandler
	Consent      ConsentHandler
	Redis        *redis.Client
	CanalOTP     auth.CanalOTP
	Contadores   auth.Contadores
	Desafios     DesafiosHandler
	Registro     RegistroHandler
	Verificacion VerificacionHandler
	Subcuentas   auth.AbridorDeSubcuentas
}

// servidorV1 implementa api.ServerInterface delegando cada método al handler de su módulo.
type servidorV1 struct {
	auth    AuthHandler
	kyc     KYCHandler
	consent ConsentHandler
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
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.PhoneChangeConfirm(w, r)
}

func (s servidorV1) PhoneChangeRequest(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.PhoneChangeRequest(w, r)
}

func (s servidorV1) ListSessions(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.ListSessions(w, r)
}

func (s servidorV1) DeleteSession(w http.ResponseWriter, r *http.Request, sessionId openapi_types.UUID) {
	if s.auth == nil {
		sinImplementar(w, r)
		return
	}
	s.auth.DeleteSession(w, r, sessionId)
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
