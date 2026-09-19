package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/jackc/pgx/v5/pgtype"
)

// derivadorFalso cuenta las verificaciones y responde siempre lo que el caso pida.
type derivadorFalso struct {
	mu          sync.Mutex
	adquisicion int
	coincide    bool
	err         error
}

// Derivar devuelve un PHC estable para el relleno precomputado.
func (d *derivadorFalso) Derivar(_ context.Context, _ string) (string, error) {
	return "$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA", nil
}

// Verificar registra la adquisición del semáforo y devuelve la coincidencia configurada.
func (d *derivadorFalso) Verificar(_ context.Context, _, _ string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.adquisicion++
	return d.coincide, d.err
}

// llamadas informa cuántas veces se adquirió el semáforo del derivador.
func (d *derivadorFalso) llamadas() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.adquisicion
}

// contadoresFalsos implementa Contadores con estado en memoria y fallo forzable.
type contadoresFalsos struct {
	mu        sync.Mutex
	valores   map[string]int64
	bloqueos  map[string]time.Time
	ventanas  map[string]time.Duration
	falla     bool
	bloqueada bool
}

// nuevosContadoresFalsos construye el puerto con los mapas listos.
func nuevosContadoresFalsos() *contadoresFalsos {
	return &contadoresFalsos{valores: map[string]int64{}, bloqueos: map[string]time.Time{}, ventanas: map[string]time.Duration{}}
}

func (c *contadoresFalsos) Incrementar(_ context.Context, clave string, ventana time.Duration) (int64, error) {
	if c.falla {
		return 0, errors.New("redis caído")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valores[clave]++
	c.ventanas[clave] = ventana
	return c.valores[clave], nil
}

// clavesIncrementadas devuelve una copia de las claves que se incrementaron.
func (c *contadoresFalsos) clavesIncrementadas() map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	copia := make(map[string]int64, len(c.valores))
	for clave, valor := range c.valores {
		copia[clave] = valor
	}
	return copia
}

// ventanaDe devuelve la ventana registrada en el último incremento de la clave.
func (c *contadoresFalsos) ventanaDe(clave string) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ventanas[clave]
}

// tieneClave informa si la clave recibió algún incremento.
func (c *contadoresFalsos) tieneClave(clave string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.valores[clave]
	return ok
}

func (c *contadoresFalsos) Bloquear(_ context.Context, clave string, hasta time.Duration) error {
	if c.falla {
		return errors.New("redis caído")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bloqueos[clave] = time.Now().Add(hasta)
	return nil
}

func (c *contadoresFalsos) Bloqueada(_ context.Context, clave string) (bool, error) {
	if c.falla {
		return false, errors.New("redis caído")
	}
	if c.bloqueada {
		return true, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expira, ok := c.bloqueos[clave]
	return ok && expira.After(time.Now()), nil
}

// registradorFalso captura los eventos sin persistir.
type registradorFalso struct {
	mu      sync.Mutex
	eventos []Evento
	err     error
}

func (r *registradorFalso) Registrar(_ context.Context, e Evento) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventos = append(r.eventos, e)
	return r.err
}

// repositorioFalso implementa el puerto de credenciales del login sin Postgres.
type repositorioFalso struct {
	credencial repositorio.Credencial
	existe     bool
	err        error
	rehashErr  error
	rehashes   int
}

func (r *repositorioFalso) PorCorreo(context.Context, string) (repositorio.Credencial, bool, error) {
	if r.err != nil {
		return repositorio.Credencial{}, false, r.err
	}
	return r.credencial, r.existe, nil
}

func (r *repositorioFalso) Rehash(context.Context, pgtype.UUID, pgtype.Text, string) (int64, error) {
	r.rehashes++
	if r.rehashErr != nil {
		return 0, r.rehashErr
	}
	return 1, nil
}

// clavesDePrueba arma las claves HMAC con el pepper de sandbox.
func clavesDePrueba(t *testing.T) *Claves {
	t.Helper()
	claves, err := NuevasClaves([]byte(strings.Repeat("1", 64)))
	if err != nil {
		t.Fatalf("claves: %v", err)
	}
	return claves
}

// verificadorDePrueba arma el verificador con los puertos falsos y sin tocar Postgres.
func verificadorDePrueba(t *testing.T, repo RepositorioCredenciales, derivador Derivador, contadores Contadores, eventos RegistradorEventos) *VerificadorDeCredenciales {
	t.Helper()
	if _, err := NuevoVerificadorDeCredenciales(nil, derivador, contadores, clavesDePrueba(t), eventos, ParametrosArgon2DePrueba()); err == nil {
		t.Fatal("NuevoVerificadorDeCredenciales aceptó un repositorio nil")
	}
	v, err := NuevoVerificadorDeCredenciales(repo, derivador, contadores, clavesDePrueba(t), eventos, ParametrosArgon2DePrueba())
	if err != nil {
		t.Fatalf("NuevoVerificadorDeCredenciales: %v", err)
	}
	return v
}

// exigirErrorAutenticacion corre Autenticar y aborta si no devuelve error.
func exigirErrorAutenticacion(t *testing.T, v *VerificadorDeCredenciales, correo, contrasena string) error {
	t.Helper()
	_, err := v.Autenticar(t.Context(), correo, contrasena, "127.0.0.1")
	if err == nil {
		t.Fatal("Autenticar devolvió nil, quiero un error")
	}
	return err
}

func TestAutenticarCorreoInexistenteYContrasenaMalaDanElMismoError(t *testing.T) {
	derivador := &derivadorFalso{coincide: false}
	eventos := &registradorFalso{}

	inexistente := verificadorDePrueba(t, &repositorioFalso{existe: false}, derivador, nuevosContadoresFalsos(), eventos)
	errInexistente := exigirErrorAutenticacion(t, inexistente, "nadie@centinela.test", "mala")

	mala := verificadorDePrueba(t, &repositorioFalso{existe: true, credencial: credencialConPHC("$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA")}, derivador, nuevosContadoresFalsos(), eventos)
	errContrasenaMala := exigirErrorAutenticacion(t, mala, "existe@centinela.test", "mala")

	if errInexistente.Error() != errContrasenaMala.Error() {
		t.Fatalf("mensajes distintos: inexistente=%q mala=%q", errInexistente, errContrasenaMala)
	}
	if !errors.Is(errInexistente, ErrCredencialesInvalidas) || !errors.Is(errContrasenaMala, ErrCredencialesInvalidas) {
		t.Fatalf("quiero ErrCredencialesInvalidas en ambos, obtuve %v y %v", errInexistente, errContrasenaMala)
	}
}

// credencialConPHC arma una credencial falsa con el PHC dado.
func credencialConPHC(phc string) repositorio.Credencial {
	return repositorio.Credencial{
		Correo:        "existe@centinela.test",
		ValorDerivado: pgtype.Text{String: phc, Valid: true},
		Estado:        string(EstadoActivo),
	}
}

func TestAutenticarRellenoPasaPorElSemaforo(t *testing.T) {
	derivador := &derivadorFalso{coincide: false}
	v := verificadorDePrueba(t, &repositorioFalso{existe: false}, derivador, nuevosContadoresFalsos(), &registradorFalso{})

	_ = exigirErrorAutenticacion(t, v, "no-existe@centinela.test", "cualquiera")
	if derivador.llamadas() != 1 {
		t.Fatalf("el relleno pasó por el derivador %d veces, quiero 1", derivador.llamadas())
	}
}

func TestAutenticarFailClosedAntesDeArgon2(t *testing.T) {
	derivador := &derivadorFalso{coincide: true}
	contadores := nuevosContadoresFalsos()
	contadores.falla = true
	v := verificadorDePrueba(t, &repositorioFalso{existe: false}, derivador, contadores, &registradorFalso{})

	err := exigirErrorAutenticacion(t, v, "alguien@centinela.test", "contrasena")
	if !errors.Is(err, ErrAuthNoDisponible) {
		t.Fatalf("error = %v, quiero ErrAuthNoDisponible", err)
	}
	if derivador.llamadas() != 0 {
		t.Fatalf("el derivador se llamó %d veces, quiero 0 con Redis caído", derivador.llamadas())
	}
}

func TestAutenticarBloqueadaRespondeDemasiadosEnvios(t *testing.T) {
	derivador := &derivadorFalso{coincide: true}
	contadores := nuevosContadoresFalsos()
	contadores.bloqueada = true
	v := verificadorDePrueba(t, &repositorioFalso{existe: false}, derivador, contadores, &registradorFalso{})

	err := exigirErrorAutenticacion(t, v, "alguien@centinela.test", "contrasena")
	if !errors.Is(err, ErrDemasiadosEnvios) {
		t.Fatalf("error = %v, quiero ErrDemasiadosEnvios", err)
	}
	if derivador.llamadas() != 0 {
		t.Fatalf("el derivador se llamó %d veces, quiero 0 con la cuenta bloqueada", derivador.llamadas())
	}
}

func TestAutenticarRellenoConCorreoInexistenteCuentaComoFallo(t *testing.T) {
	correo := "nadie@centinela.test"
	derivador := &derivadorFalso{coincide: true}
	contadores := nuevosContadoresFalsos()
	v := verificadorDePrueba(t, &repositorioFalso{existe: false}, derivador, contadores, &registradorFalso{})

	err := exigirErrorAutenticacion(t, v, correo, rellenoContrasena)
	if !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("error = %v, quiero ErrCredencialesInvalidas", err)
	}
	claveCuenta := PrefijoFallosCuenta + clavesDePrueba(t).ClaveDeCorreo(NormalizarCorreo(correo))
	if contadores.clavesIncrementadas()[claveCuenta] != 1 {
		t.Fatalf("el relleno con correo inexistente no incrementó %q: %v", claveCuenta, contadores.clavesIncrementadas())
	}
}

func TestAutenticarPhcVigenteNoRehashea(t *testing.T) {
	repo := &repositorioFalso{existe: true, credencial: credencialConPHC(phcConParametros(ParametrosArgon2DePrueba()))}
	v := verificadorDePrueba(t, repo, &derivadorFalso{coincide: true}, nuevosContadoresFalsos(), &registradorFalso{})

	if _, err := v.Autenticar(t.Context(), "existe@centinela.test", "contrasena", "203.0.113.9"); err != nil {
		t.Fatalf("Autenticar: %v", err)
	}
	if repo.rehashes != 0 {
		t.Fatalf("rehashes = %d, quiero 0 con el PHC vigente", repo.rehashes)
	}
}

func TestAutenticarPhcViejoSiRehashea(t *testing.T) {
	viejos := ParametrosArgon2{MemoriaKiB: 4096, Iteraciones: 1, Paralelismo: 1, LongitudSal: 16, LongitudSalida: 32}
	repo := &repositorioFalso{existe: true, credencial: credencialConPHC(phcConParametros(viejos))}
	v := verificadorDePrueba(t, repo, &derivadorFalso{coincide: true}, nuevosContadoresFalsos(), &registradorFalso{})

	if _, err := v.Autenticar(t.Context(), "existe@centinela.test", "contrasena", "203.0.113.9"); err != nil {
		t.Fatalf("Autenticar: %v", err)
	}
	if repo.rehashes != 1 {
		t.Fatalf("rehashes = %d, quiero 1 con el PHC viejo", repo.rehashes)
	}
}

func TestAutenticarErrorDeRehashNoAbortaElLogin(t *testing.T) {
	viejos := ParametrosArgon2{MemoriaKiB: 4096, Iteraciones: 1, Paralelismo: 1, LongitudSal: 16, LongitudSalida: 32}
	repo := &repositorioFalso{
		existe:     true,
		credencial: credencialConPHC(phcConParametros(viejos)),
		rehashErr:  errors.New("update transitorio caído"),
	}
	v := verificadorDePrueba(t, repo, &derivadorFalso{coincide: true}, nuevosContadoresFalsos(), &registradorFalso{})

	credencial, err := v.Autenticar(t.Context(), "existe@centinela.test", "contrasena", "203.0.113.9")
	if err != nil {
		t.Fatalf("el rehash best-effort no debe abortar el login: %v", err)
	}
	if credencial.Estado != EstadoActivo {
		t.Fatalf("estado = %s, quiero %s", credencial.Estado, EstadoActivo)
	}
}

func TestAutenticarOrigenCuentaYBloquea(t *testing.T) {
	contadores := nuevosContadoresFalsos()
	v := verificadorDePrueba(t, &repositorioFalso{existe: false}, &derivadorFalso{coincide: false}, contadores, &registradorFalso{})

	for i := range maxEnviosPorOrigen {
		correo := fmt.Sprintf("persona-%d@centinela.test", i)
		if err := exigirErrorAutenticacion(t, v, correo, "mala"); !errors.Is(err, ErrCredencialesInvalidas) {
			t.Fatalf("intento %d del origen = %v, quiero ErrCredencialesInvalidas", i, err)
		}
	}
	bloqueada := exigirErrorAutenticacion(t, v, "persona-final@centinela.test", "mala")
	if !errors.Is(bloqueada, ErrDemasiadosEnvios) {
		t.Fatalf("tras %d intentos del mismo origen = %v, quiero ErrDemasiadosEnvios", maxEnviosPorOrigen, bloqueada)
	}
}

func TestAutenticarFalloComparteLaVentanaDeCuenta(t *testing.T) {
	correo := "fallona@centinela.test"
	contadores := nuevosContadoresFalsos()
	repo := &repositorioFalso{existe: true, credencial: credencialConPHC(phcConParametros(ParametrosArgon2DePrueba()))}
	v := verificadorDePrueba(t, repo, &derivadorFalso{coincide: false}, contadores, &registradorFalso{})

	if err := exigirErrorAutenticacion(t, v, correo, "mala"); !errors.Is(err, ErrCredencialesInvalidas) {
		t.Fatalf("error = %v, quiero ErrCredencialesInvalidas", err)
	}
	claveCuenta := PrefijoFallosCuenta + clavesDePrueba(t).ClaveDeCorreo(NormalizarCorreo(correo))
	if got := contadores.ventanaDe(claveCuenta); got != ventanaFallos {
		t.Fatalf("ventana de %q = %s, quiero la de A2-06.3 %s", claveCuenta, got, ventanaFallos)
	}
	claveVieja := PrefijoLoginCuenta + clavesDePrueba(t).ClaveDeCorreo(NormalizarCorreo(correo))
	if contadores.tieneClave(claveVieja) {
		t.Fatalf("el login no debe escribir %q", claveVieja)
	}
}

// phcConParametros serializa un PHC válido con los parámetros de costo dados.
func phcConParametros(p ParametrosArgon2) string {
	return p.phc([]byte("salt-1234567890"), make([]byte, p.LongitudSalida))
}
