package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// autenticadorFalso devuelve la credencial y el error configurados sin tocar Postgres.
type autenticadorFalso struct {
	credencial CredencialAutenticada
	err        error
}

func (a autenticadorFalso) Autenticar(context.Context, string, string, string) (CredencialAutenticada, error) {
	return a.credencial, a.err
}

// desafiosFalso captura las solicitudes y devuelve el desafío o el error configurados.
type desafiosFalso struct {
	solicitudes []Solicitud
	desafio     Desafio
	err         error
}

func (d *desafiosFalso) CrearDesafioRacionado(_ context.Context, solicitud Solicitud) (Desafio, error) {
	d.solicitudes = append(d.solicitudes, solicitud)
	if d.err != nil {
		return Desafio{}, d.err
	}
	return d.desafio, nil
}

// rolesFalso devuelve los roles y el error configurados.
type rolesFalso struct {
	roles []RolVigente
	err   error
}

func (r rolesFalso) RolesVigentesDeParte(context.Context, uuid.UUID) ([]RolVigente, error) {
	return r.roles, r.err
}

// servicioDeLoginDePrueba arma el servicio con los tres dobles.
func servicioDeLoginDePrueba(t *testing.T, autenticador AutenticadorDeCredenciales, desafios CreadorDeDesafiosRacionados, roles LectorDeRolesVigentes) *ServicioDeLogin {
	t.Helper()
	servicio, err := NuevoServicioDeLogin(autenticador, desafios, roles)
	if err != nil {
		t.Fatalf("NuevoServicioDeLogin: %v", err)
	}
	return servicio
}

func TestNuevoServicioDeLoginRechazaDependenciasNulas(t *testing.T) {
	desafios := &desafiosFalso{}
	roles := rolesFalso{}
	if _, err := NuevoServicioDeLogin(nil, desafios, roles); err == nil {
		t.Fatal("aceptó un autenticador nil")
	}
	if _, err := NuevoServicioDeLogin(autenticadorFalso{}, nil, roles); err == nil {
		t.Fatal("aceptó un creador de desafíos nil")
	}
	if _, err := NuevoServicioDeLogin(autenticadorFalso{}, desafios, nil); err == nil {
		t.Fatal("aceptó un lector de roles nil")
	}
}

func TestIniciarSesionPropagaElErrorDeAutenticacion(t *testing.T) {
	desafios := &desafiosFalso{}
	servicio := servicioDeLoginDePrueba(t, autenticadorFalso{err: ErrCredencialesInvalidas}, desafios, rolesFalso{})

	_, err := servicio.IniciarSesion(t.Context(), "persona@centinela.test", "mala", "", "203.0.113.9")
	if !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("error = %v, quiero ErrCredencialesInvalidas", err)
	}
	if len(desafios.solicitudes) != 0 {
		t.Fatalf("se crearon %d desafíos con credenciales inválidas, quiero 0", len(desafios.solicitudes))
	}
}

func TestIniciarSesionCuentaPendienteCreaDesafioDeRegistro(t *testing.T) {
	desafios := &desafiosFalso{desafio: Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoRegistro}}
	credencial := CredencialAutenticada{
		CredencialID: uuid.New(),
		ParteID:      uuid.New(),
		Correo:       "Persona@Centinela.test",
		Celular:      "+573001112233",
		Estado:       EstadoPendienteVerificacion,
	}
	servicio := servicioDeLoginDePrueba(t, autenticadorFalso{credencial: credencial}, desafios, rolesFalso{})

	resultado, err := servicio.IniciarSesion(t.Context(), "persona@centinela.test", "contrasena", "", "203.0.113.9")
	if err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	if resultado.Proposito != PropositoRegistro || !resultado.RequiereSegundoFactor || resultado.Mensaje != MensajeDesafioRegistro {
		t.Fatalf("resultado = %+v, quiero proposito registro con mensaje de verificación", resultado)
	}
	if len(desafios.solicitudes) != 1 {
		t.Fatalf("solicitudes = %d, quiero 1", len(desafios.solicitudes))
	}
	solicitud := desafios.solicitudes[0]
	if solicitud.Proposito != PropositoRegistro || solicitud.Celular != credencial.Celular {
		t.Fatalf("solicitud = %+v, quiero registro al celular registrado", solicitud)
	}
	if solicitud.Contexto != "" {
		t.Fatalf("contexto = %q, quiero vacío en el registro", solicitud.Contexto)
	}
	if solicitud.CorreoNormalizado != "persona@centinela.test" {
		t.Fatalf("correo normalizado = %q, quiero persona@centinela.test", solicitud.CorreoNormalizado)
	}
}

func TestIniciarSesionCuentaActivaPorDefectoUsuario(t *testing.T) {
	desafios := &desafiosFalso{desafio: Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoLogin}}
	credencial := CredencialAutenticada{
		CredencialID: uuid.New(),
		ParteID:      uuid.New(),
		Correo:       "activa@centinela.test",
		Celular:      "+573001112233",
		Estado:       EstadoActivo,
	}
	servicio := servicioDeLoginDePrueba(t, autenticadorFalso{credencial: credencial}, desafios, rolesFalso{})

	resultado, err := servicio.IniciarSesion(t.Context(), "activa@centinela.test", "contrasena", "", "203.0.113.9")
	if err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	if resultado.Proposito != PropositoLogin || resultado.Mensaje != MensajeDesafioLogin {
		t.Fatalf("resultado = %+v, quiero proposito login", resultado)
	}
	if desafios.solicitudes[0].Contexto != ContextoUsuario {
		t.Fatalf("contexto = %q, quiero %q por defecto", desafios.solicitudes[0].Contexto, ContextoUsuario)
	}
}

func TestIniciarSesionEmpresaConRolGuardaElContexto(t *testing.T) {
	desafios := &desafiosFalso{desafio: Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoLogin}}
	credencial := CredencialAutenticada{
		CredencialID: uuid.New(),
		ParteID:      uuid.New(),
		Correo:       "rep@centinela.test",
		Celular:      "+573001112233",
		Estado:       EstadoActivo,
	}
	roles := rolesFalso{roles: []RolVigente{{Rol: "representante_legal", RepresentadaID: uuid.New()}}}
	servicio := servicioDeLoginDePrueba(t, autenticadorFalso{credencial: credencial}, desafios, roles)

	if _, err := servicio.IniciarSesion(t.Context(), "rep@centinela.test", "contrasena", ContextoEmpresa, "203.0.113.9"); err != nil {
		t.Fatalf("IniciarSesion: %v", err)
	}
	if desafios.solicitudes[0].Contexto != ContextoEmpresa {
		t.Fatalf("contexto = %q, quiero %q", desafios.solicitudes[0].Contexto, ContextoEmpresa)
	}
}

func TestIniciarSesionEmpresaSinRolDevuelveErrSinRolDeEmpresa(t *testing.T) {
	desafios := &desafiosFalso{}
	credencial := CredencialAutenticada{
		CredencialID: uuid.New(),
		ParteID:      uuid.New(),
		Correo:       "sin-rol@centinela.test",
		Celular:      "+573001112233",
		Estado:       EstadoActivo,
	}
	servicio := servicioDeLoginDePrueba(t, autenticadorFalso{credencial: credencial}, desafios, rolesFalso{})

	_, err := servicio.IniciarSesion(t.Context(), "sin-rol@centinela.test", "contrasena", ContextoEmpresa, "203.0.113.9")
	if !errors.Is(err, ErrSinRolDeEmpresa) {
		t.Fatalf("error = %v, quiero ErrSinRolDeEmpresa", err)
	}
	if len(desafios.solicitudes) != 0 {
		t.Fatalf("se creó un desafío sin rol de empresa, quiero 0")
	}
}

func TestIniciarSesionPropagaLaEntregaFallida(t *testing.T) {
	desafios := &desafiosFalso{err: ErrEntregaFallida}
	credencial := CredencialAutenticada{CredencialID: uuid.New(), ParteID: uuid.New(), Celular: "+573001112233", Estado: EstadoActivo, Correo: "activa@centinela.test"}
	servicio := servicioDeLoginDePrueba(t, autenticadorFalso{credencial: credencial}, desafios, rolesFalso{})

	_, err := servicio.IniciarSesion(t.Context(), "activa@centinela.test", "contrasena", "", "203.0.113.9")
	if !errors.Is(err, ErrEntregaFallida) {
		t.Fatalf("error = %v, quiero ErrEntregaFallida", err)
	}
}
