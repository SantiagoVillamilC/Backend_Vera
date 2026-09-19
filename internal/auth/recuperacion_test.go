package auth

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// contadoresDeRecuperacion registra las claves y devuelve el fallo o el bloqueo configurados.
type contadoresDeRecuperacion struct {
	falla         error
	bloqueada     bool
	incrementadas []string
}

func (c *contadoresDeRecuperacion) Incrementar(_ context.Context, clave string, _ time.Duration) (int64, error) {
	if c.falla != nil {
		return 0, c.falla
	}
	c.incrementadas = append(c.incrementadas, clave)
	return 1, nil
}

func (c *contadoresDeRecuperacion) Bloquear(_ context.Context, _ string, _ time.Duration) error {
	return c.falla
}

func (c *contadoresDeRecuperacion) Bloqueada(_ context.Context, _ string) (bool, error) {
	if c.falla != nil {
		return false, c.falla
	}
	return c.bloqueada, nil
}

// repoDeRecuperacion sirve la credencial configurada y cuenta las lecturas.
type repoDeRecuperacion struct {
	credencial   repositorio.Credencial
	existe       bool
	err          error
	llamadas     int
	ultimoCorreo string
}

func (r *repoDeRecuperacion) PorCorreo(_ context.Context, correo string) (repositorio.Credencial, bool, error) {
	r.llamadas++
	r.ultimoCorreo = correo
	return r.credencial, r.existe, r.err
}

// PorCelular satisface el puerto del login; la recuperación no lo usa.
func (r *repoDeRecuperacion) PorCelular(context.Context, string) (repositorio.Credencial, bool, error) {
	return r.credencial, r.existe, r.err
}

func (r *repoDeRecuperacion) Rehash(context.Context, pgtype.UUID, pgtype.Text, string) (int64, error) {
	return 0, nil
}

// desafiosDeRecuperacion devuelve el desafío o el error configurados.
type desafiosDeRecuperacion struct {
	desafio  Desafio
	err      error
	llamadas int
}

func (d *desafiosDeRecuperacion) CrearDesafio(context.Context, Solicitud) (Desafio, error) {
	d.llamadas++
	return d.desafio, d.err
}

// rellenoDeRecuperacion registra la inserción del relleno y su evento.
type rellenoDeRecuperacion struct {
	desafio  Desafio
	err      error
	llamadas int
	eventos  *eventosDeRecuperacion
}

func (r *rellenoDeRecuperacion) InsertarRelleno(context.Context, string, string) (Desafio, error) {
	r.llamadas++
	if r.eventos != nil {
		r.eventos.registrados = append(r.eventos.registrados, Evento{Tipo: TipoEventoRecuperacion, Resultado: resultadoRecuperacionRelleno})
	}
	return r.desafio, r.err
}

// eventosDeRecuperacion captura los eventos publicados.
type eventosDeRecuperacion struct{ registrados []Evento }

func (e *eventosDeRecuperacion) Registrar(_ context.Context, evento Evento) error {
	e.registrados = append(e.registrados, evento)
	return nil
}

// servicioDeRecuperacionDePrueba arma el servicio con los puertos falsos.
func servicioDeRecuperacionDePrueba(t *testing.T, repo *repoDeRecuperacion, relleno *rellenoDeRecuperacion, desafios *desafiosDeRecuperacion, contadores *contadoresDeRecuperacion, eventos *eventosDeRecuperacion) *ServicioDeRecuperacion {
	t.Helper()

	claves, err := NuevasClaves(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("NuevasClaves: %v", err)
	}
	servicio, err := nuevoServicioDeRecuperacion(repo, relleno, desafios, eventos, contadores, claves, bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		t.Fatalf("nuevoServicioDeRecuperacion: %v", err)
	}
	return servicio
}

// credencialDeRecuperacion arma una credencial con el estado y la verificación pedidos.
func credencialDeRecuperacion(estado string, verificado bool) repositorio.Credencial {
	credencial := repositorio.Credencial{
		ID:      pgtype.UUID{Bytes: uuid.New(), Valid: true},
		ParteID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Correo:  "persona@centinela.test",
		Celular: "+573001112233",
		Estado:  estado,
	}
	if verificado {
		credencial.CelularVerificadoEn = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	return credencial
}

func TestSolicitarRecuperacionCuentaActivaUsaElDesafioReal(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	repo := &repoDeRecuperacion{credencial: credencialDeRecuperacion(string(EstadoActivo), true), existe: true}
	desafios := &desafiosDeRecuperacion{desafio: Desafio{ID: id, ExpiraEnSegundos: 300, Proposito: PropositoRecuperacion}}
	relleno := &rellenoDeRecuperacion{}
	eventos := &eventosDeRecuperacion{}
	servicio := servicioDeRecuperacionDePrueba(t, repo, relleno, desafios, &contadoresDeRecuperacion{}, eventos)

	desafio, err := servicio.SolicitarRecuperacion(t.Context(), "  Persona@Centinela.TEST ", "127.0.0.1")
	if err != nil {
		t.Fatalf("SolicitarRecuperacion: %v", err)
	}
	if desafio.ID != id || desafio.ExpiraEnSegundos != 300 || desafio.Proposito != PropositoRecuperacion {
		t.Errorf("desafío = %+v, quiero el del camino real", desafio)
	}
	if desafios.llamadas != 1 || relleno.llamadas != 0 {
		t.Errorf("desafíos=%d relleno=%d, quiero 1/0", desafios.llamadas, relleno.llamadas)
	}
	if repo.ultimoCorreo != "persona@centinela.test" {
		t.Errorf("correo buscado = %q, quiero normalizado", repo.ultimoCorreo)
	}
	if len(eventos.registrados) != 1 {
		t.Fatalf("eventos = %d, quiero 1", len(eventos.registrados))
	}
	evento := eventos.registrados[0]
	if evento.Tipo != TipoEventoRecuperacion || evento.Resultado != resultadoRecuperacionEnviada {
		t.Errorf("evento = %s/%s, quiero recuperacion/%s", evento.Tipo, evento.Resultado, resultadoRecuperacionEnviada)
	}
	if evento.Contexto == "" || evento.Contexto == "persona@centinela.test" {
		t.Error("el evento no lleva el HMAC del correo en el contexto")
	}
}

func TestSolicitarRecuperacionCuentaNoElegibleUsaElRelleno(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		credencial repositorio.Credencial
		existe     bool
	}{
		"inexistente":            {repositorio.Credencial{}, false},
		"pendiente_verificacion": {credencialDeRecuperacion(string(EstadoPendienteVerificacion), true), true},
		"suspendido":             {credencialDeRecuperacion(string(EstadoSuspendido), true), true},
		"cerrado":                {credencialDeRecuperacion(string(EstadoCerrado), true), true},
		"sin_login":              {credencialDeRecuperacion(string(EstadoSinLogin), true), true},
		"activo_sin_celular":     {credencialDeRecuperacion(string(EstadoActivo), false), true},
	}
	for nombre, caso := range casos {
		t.Run(nombre, func(t *testing.T) {
			t.Parallel()

			repo := &repoDeRecuperacion{credencial: caso.credencial, existe: caso.existe}
			desafios := &desafiosDeRecuperacion{}
			eventos := &eventosDeRecuperacion{}
			relleno := &rellenoDeRecuperacion{desafio: Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoRecuperacion}, eventos: eventos}
			servicio := servicioDeRecuperacionDePrueba(t, repo, relleno, desafios, &contadoresDeRecuperacion{}, eventos)

			desafio, err := servicio.SolicitarRecuperacion(t.Context(), "persona@centinela.test", "127.0.0.1")
			if err != nil {
				t.Fatalf("SolicitarRecuperacion: %v", err)
			}
			if desafio.Proposito != PropositoRecuperacion || desafio.ExpiraEnSegundos != 300 {
				t.Errorf("desafío = %+v, quiero relleno de recuperación", desafio)
			}
			if relleno.llamadas != 1 || desafios.llamadas != 0 {
				t.Errorf("relleno=%d desafíos=%d, quiero 1/0", relleno.llamadas, desafios.llamadas)
			}
			if len(eventos.registrados) != 1 || eventos.registrados[0].Resultado != resultadoRecuperacionRelleno {
				t.Errorf("eventos = %+v, quiero recuperacion/%s", eventos.registrados, resultadoRecuperacionRelleno)
			}
		})
	}
}

func TestSolicitarRecuperacionFailClosedNoLeeLaBase(t *testing.T) {
	t.Parallel()

	repo := &repoDeRecuperacion{existe: true, credencial: credencialDeRecuperacion(string(EstadoActivo), true)}
	desafios := &desafiosDeRecuperacion{}
	relleno := &rellenoDeRecuperacion{}
	servicio := servicioDeRecuperacionDePrueba(t, repo, relleno, desafios,
		&contadoresDeRecuperacion{falla: errors.New("dial tcp 127.0.0.1:1: connect: connection refused")}, &eventosDeRecuperacion{})

	_, err := servicio.SolicitarRecuperacion(t.Context(), "persona@centinela.test", "127.0.0.1")
	if !errors.Is(err, ErrAuthNoDisponible) {
		t.Fatalf("err = %v, quiero ErrAuthNoDisponible", err)
	}
	if repo.llamadas != 0 || relleno.llamadas != 0 {
		t.Errorf("repo=%d relleno=%d, quiero 0/0: Redis va antes de la base", repo.llamadas, relleno.llamadas)
	}
}

func TestSolicitarRecuperacionBloqueadaRespondeGenerico(t *testing.T) {
	t.Parallel()

	repo := &repoDeRecuperacion{existe: true, credencial: credencialDeRecuperacion(string(EstadoActivo), true)}
	servicio := servicioDeRecuperacionDePrueba(t, repo, &rellenoDeRecuperacion{}, &desafiosDeRecuperacion{},
		&contadoresDeRecuperacion{bloqueada: true}, &eventosDeRecuperacion{})

	_, err := servicio.SolicitarRecuperacion(t.Context(), "persona@centinela.test", "127.0.0.1")
	if !errors.Is(err, ErrDemasiadosEnvios) {
		t.Fatalf("err = %v, quiero ErrDemasiadosEnvios", err)
	}
	if repo.llamadas != 0 {
		t.Errorf("repo = %d llamadas, quiero 0", repo.llamadas)
	}
}

func TestSolicitarRecuperacionSinOptInUsaElRelleno(t *testing.T) {
	t.Parallel()

	repo := &repoDeRecuperacion{existe: true, credencial: credencialDeRecuperacion(string(EstadoActivo), true)}
	desafios := &desafiosDeRecuperacion{err: ErrConsentimientoRequerido}
	eventos := &eventosDeRecuperacion{}
	relleno := &rellenoDeRecuperacion{desafio: Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoRecuperacion}, eventos: eventos}
	servicio := servicioDeRecuperacionDePrueba(t, repo, relleno, desafios, &contadoresDeRecuperacion{}, eventos)

	desafio, err := servicio.SolicitarRecuperacion(t.Context(), "persona@centinela.test", "127.0.0.1")
	if err != nil {
		t.Fatalf("SolicitarRecuperacion: %v", err)
	}
	if desafio.Proposito != PropositoRecuperacion {
		t.Errorf("desafío = %+v, quiero el relleno", desafio)
	}
	if relleno.llamadas != 1 {
		t.Errorf("relleno = %d llamadas, quiero 1: sin opt-in no se revela la cuenta", relleno.llamadas)
	}
	if len(eventos.registrados) != 1 || eventos.registrados[0].Resultado != resultadoRecuperacionRelleno {
		t.Errorf("eventos = %+v, quiero recuperacion/%s", eventos.registrados, resultadoRecuperacionRelleno)
	}
}

func TestSolicitarRecuperacionEntregaFallidaDevuelveElDesafio(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	repo := &repoDeRecuperacion{existe: true, credencial: credencialDeRecuperacion(string(EstadoActivo), true)}
	desafios := &desafiosDeRecuperacion{desafio: Desafio{ID: id, ExpiraEnSegundos: 300, Proposito: PropositoRecuperacion}, err: ErrEntregaFallida}
	relleno := &rellenoDeRecuperacion{}
	eventos := &eventosDeRecuperacion{}
	servicio := servicioDeRecuperacionDePrueba(t, repo, relleno, desafios, &contadoresDeRecuperacion{}, eventos)

	desafio, err := servicio.SolicitarRecuperacion(t.Context(), "persona@centinela.test", "127.0.0.1")
	if err != nil {
		t.Fatalf("err = %v, quiero nil para no revelar el fallo de entrega", err)
	}
	if desafio.ID != id {
		t.Errorf("desafío = %+v, quiero el id invalidado por A2-06.1", desafio)
	}
	if len(eventos.registrados) != 1 || eventos.registrados[0].Resultado != resultadoRecuperacionEntregaFallida {
		t.Errorf("eventos = %+v, quiero recuperacion/%s", eventos.registrados, resultadoRecuperacionEntregaFallida)
	}
}

func TestNuevoServicioDeRecuperacionExigePuertos(t *testing.T) {
	t.Parallel()

	if _, err := NuevoServicioDeRecuperacion(nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("NuevoServicioDeRecuperacion sin pool devolvió nil, quiero error")
	}
}
