package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// autenticadorFalso devuelve la credencial y el error configurados sin tocar Postgres.
type autenticadorFalso struct {
	credencial CredencialAutenticada
	err        error
}

func (a autenticadorFalso) Autenticar(context.Context, string, string, string) (CredencialAutenticada, error) {
	return a.credencial, a.err
}

// servicioDeLoginDePrueba arma el servicio con los dobles y el gestor sobre un pool que no se usa.
func servicioDeLoginDePrueba(t *testing.T, autenticador AutenticadorDeCredenciales) *ServicioDeLogin {
	t.Helper()
	tokens, err := NuevoTokensDeAcceso([]byte("clave-de-firma-de-al-menos-32-bytes"), "centinela-auth", "centinela-api")
	if err != nil {
		t.Fatalf("tokens de acceso: %v", err)
	}
	return &ServicioDeLogin{autenticador: autenticador, sesiones: NuevoGestorSesiones(nil, nil), tokens: tokens}
}

func TestNuevoServicioDeLoginRechazaDependenciasNulas(t *testing.T) {
	sesiones := NuevoGestorSesiones(nil, nil)
	tokens, err := NuevoTokensDeAcceso([]byte("clave-de-firma-de-al-menos-32-bytes"), "centinela-auth", "centinela-api")
	if err != nil {
		t.Fatalf("tokens de acceso: %v", err)
	}
	if _, err := NuevoServicioDeLogin(nil, (*pgxpool.Pool)(nil), sesiones, tokens); err == nil {
		t.Fatal("aceptó un autenticador nil")
	}
	if _, err := NuevoServicioDeLogin(autenticadorFalso{}, (*pgxpool.Pool)(nil), sesiones, tokens); err == nil {
		t.Fatal("aceptó un pool nil")
	}
	if _, err := NuevoServicioDeLogin(autenticadorFalso{}, &pgxpool.Pool{}, nil, tokens); err == nil {
		t.Fatal("aceptó un gestor de sesiones nil")
	}
	if _, err := NuevoServicioDeLogin(autenticadorFalso{}, &pgxpool.Pool{}, sesiones, nil); err == nil {
		t.Fatal("aceptó un firmador nil")
	}
}

func TestIniciarSesionPropagaElErrorDeAutenticacion(t *testing.T) {
	servicio := servicioDeLoginDePrueba(t, autenticadorFalso{err: ErrCredencialesInvalidas})

	_, err := servicio.IniciarSesion(t.Context(), "+573001112233", "mala", "", "203.0.113.9", "dispositivo")
	if !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("error = %v, quiero ErrCredencialesInvalidas", err)
	}
}

func TestIniciarSesionCuentaPendienteRespondeCelularSinVerificar(t *testing.T) {
	credencial := CredencialAutenticada{
		CredencialID: uuid.New(),
		ParteID:      uuid.New(),
		Celular:      "+573001112233",
		Estado:       EstadoPendienteVerificacion,
	}
	servicio := servicioDeLoginDePrueba(t, autenticadorFalso{credencial: credencial})

	_, err := servicio.IniciarSesion(t.Context(), "+573001112233", "contrasena", "", "203.0.113.9", "dispositivo")
	if !errors.Is(err, ErrCelularSinVerificar) {
		t.Fatalf("error = %v, quiero ErrCelularSinVerificar", err)
	}
}
