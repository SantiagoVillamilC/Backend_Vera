//go:build integration

package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/correosim"
	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// contadorCelularDePrueba reparte números únicos entre pruebas para no chocar con la unicidad.
var contadorCelularDePrueba atomic.Int64

// celularDePrueba devuelve un E.164 único dentro del rango de prueba.
func celularDePrueba() string {
	n := contadorCelularDePrueba.Add(1) + time.Now().UnixNano()%1000000
	return fmt.Sprintf("+57300%07d", n%10000000)
}

// contactoSoporteDePrueba es el contacto de soporte de los avisos del cambio en las pruebas.
const contactoSoporteDePrueba = "soporte@sandbox.centinela.test"

// montajeDeCambio agrupa el servicio de cambio con sus colaboradores reales.
type montajeDeCambio struct {
	servicio     *ServicioDeCambioDeNumero
	sesiones     *GestorSesiones
	recuperacion *ServicioDeRecuperacion
	canal        *canalDeCodigoFijo
	denylist     *denylistRedisDeSesion
	derivador    Derivador
	claves       *Claves
	optIn        LectorOptIn
	eventos      RegistradorEventos
}

// opcionMontajeDeCambio ajusta el montaje del cambio antes de construir el servicio.
type opcionMontajeDeCambio func(*montajeDeCambio)

// conLectorOptIn fija el lector de opt-in del servicio de cambio.
func conLectorOptIn(optIn LectorOptIn) opcionMontajeDeCambio {
	return func(m *montajeDeCambio) { m.optIn = optIn }
}

// conRegistradorEventos fija el registrador de eventos del servicio de cambio.
func conRegistradorEventos(eventos RegistradorEventos) opcionMontajeDeCambio {
	return func(m *montajeDeCambio) { m.eventos = eventos }
}

// montajeCambioDePrueba arma el cambio completo sobre Postgres y Redis reales.
func montajeCambioDePrueba(tb testing.TB, pool *pgxpool.Pool, enfriamiento time.Duration, opciones ...opcionMontajeDeCambio) montajeDeCambio {
	tb.Helper()

	m := montajeDeCambio{optIn: SiempreOptIn{}, eventos: NuevoRegistradorEventos(pool)}
	for _, opcion := range opciones {
		opcion(&m)
	}
	derivador := derivadorDeLogin(tb)
	canal := nuevoCanalDeCodigoFijo("123456")
	claves := clavesDeCambio(tb)
	contadores := contadoresDeLogin(tb)
	gestor, err := NuevoGestorDesafios(pool, canal, NuevoRegistradorEventos(pool), SiempreOptIn{}, pepperDeDesafio,
		ConRacionamiento(contadores, claves))
	if err != nil {
		tb.Fatalf("gestor de desafíos: %v", err)
	}
	sesiones := NuevoGestorSesiones(pool, NuevoRegistradorEventos(pool))
	denylist := denylistDeSesionDePrueba(tb)
	servicio, err := NuevoServicioDeCambioDeNumero(pool, gestor, sesiones, derivador, denylist, m.eventos, m.optIn, contadores, claves, pepperDeDesafio, contactoSoporteDePrueba, enfriamiento)
	if err != nil {
		tb.Fatalf("servicio de cambio: %v", err)
	}
	recuperacion, err := NuevoServicioDeRecuperacion(pool, gestor, NuevoRegistradorEventos(pool), contadores, claves, pepperDeDesafio)
	if err != nil {
		tb.Fatalf("servicio de recuperación: %v", err)
	}
	return montajeDeCambio{
		servicio: servicio, sesiones: sesiones, recuperacion: recuperacion,
		canal: canal, denylist: denylist, derivador: derivador, claves: claves, optIn: m.optIn, eventos: m.eventos,
	}
}

// clavesDeCambio arma las claves de contadores con el pepper de los tests.
func clavesDeCambio(tb testing.TB) *Claves {
	tb.Helper()

	claves, err := NuevasClaves([]byte("1111111111111111111111111111111111111111111111111111111111111111"))
	if err != nil {
		tb.Fatalf("claves: %v", err)
	}
	return claves
}

// sesionDeCambio emite una familia de sesión para la persona en el contexto pedido.
func sesionDeCambio(tb testing.TB, m montajeDeCambio, parteID uuid.UUID, contexto string) Sesion {
	tb.Helper()

	_, sesion, err := m.sesiones.EmitirSesion(tb.Context(), parteID, contexto, "dispositivo-de-prueba")
	if err != nil {
		tb.Fatalf("emitir sesión: %v", err)
	}
	return sesion
}

// leerCredencialDeCambio lee la credencial por id como rol_auth.
func leerCredencialDeCambio(tb testing.TB, pool *pgxpool.Pool, credencialID uuid.UUID) repositorio.Credencial {
	tb.Helper()

	credencial, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.Credencial, error) {
			return q.CredencialPorID(ctx, pgUUID(credencialID))
		})
	if err != nil {
		tb.Fatalf("leer la credencial: %v", err)
	}
	return credencial
}

// envejecerCambioDeNumero lleva el último cambio fuera de la ventana como dueño del esquema.
func envejecerCambioDeNumero(tb testing.TB, credencialID uuid.UUID, edad time.Duration) {
	tb.Helper()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		tb.Fatalf("DATABASE_URL es obligatoria para manipular el enfriamiento como dueño del esquema")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parsear DATABASE_URL: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		tb.Fatalf("abrir pool dueño: %v", err)
	}
	tb.Cleanup(pool.Close)
	if _, err := pool.Exec(tb.Context(),
		"UPDATE auth.credenciales SET celular_cambiado_en = $2 WHERE id = $1",
		pgUUID(credencialID), time.Now().Add(-edad)); err != nil {
		tb.Fatalf("envejecer el cambio como dueño: %v", err)
	}
}

// contarEventosDeCambio cuenta los eventos del cambio por resultado para la credencial.
func contarEventosDeCambio(tb testing.TB, pool *pgxpool.Pool, credencialID uuid.UUID, resultado string) int {
	tb.Helper()

	total := 0
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT count(*) FROM auth.eventos_autenticacion
				 WHERE credencial_id = $1 AND tipo = $2 AND resultado = $3`,
				pgUUID(credencialID), TipoEventoCambioNumero, resultado).Scan(&total)
		}); err != nil {
		tb.Fatalf("contar eventos del cambio: %v", err)
	}
	return total
}

// detalleDelEventoDeCambio devuelve el detalle crudo del primer evento del resultado pedido.
func detalleDelEventoDeCambio(tb testing.TB, pool *pgxpool.Pool, credencialID uuid.UUID, resultado string) string {
	tb.Helper()

	detalle := ""
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT detalle::text FROM auth.eventos_autenticacion
				 WHERE credencial_id = $1 AND tipo = $2 AND resultado = $3 ORDER BY id LIMIT 1`,
				pgUUID(credencialID), TipoEventoCambioNumero, resultado).Scan(&detalle)
		}); err != nil {
		tb.Fatalf("leer el detalle del evento: %v", err)
	}
	return detalle
}

// crearCredencialConCelular crea una parte y su credencial activa con el celular pedido.
func crearCredencialConCelular(tb testing.TB, pool *pgxpool.Pool, derivador Derivador, celular string) (uuid.UUID, uuid.UUID) {
	tb.Helper()

	credencialID, parteID, _, _ := sembrarCredencialDeInicioSesion(tb, pool, derivador, "contrasena-correcta", EstadoActivo, true)
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, "UPDATE auth.credenciales SET celular = $2 WHERE id = $1", pgUUID(credencialID), celular)
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("fijar el celular de la credencial: %v", err)
	}
	return credencialID, parteID
}

func TestCambioDeNumeroCaminoACompleto(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour)
	credencialID, parteID, correo, celularViejo := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 3)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)
	nuevo := celularDePrueba()

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, nuevo, "contrasena-correcta", &codigos[0], "10.0.0.1")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	if desafio.Proposito != PropositoCambioNumero {
		t.Fatalf("proposito = %q, quiero %q", desafio.Proposito, PropositoCambioNumero)
	}
	if _, ok := m.canal.UltimoCodigo(nuevo); !ok {
		t.Fatal("el número nuevo no recibió el código")
	}
	if _, ok := m.canal.UltimoCodigo(celularViejo); ok {
		t.Fatal("el número viejo recibió el código en el camino A")
	}

	resultado, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.1")
	if err != nil {
		t.Fatalf("ConfirmarCambioDeNumero: %v", err)
	}
	if !resultado.Confirmado || resultado.CelularViejo != celularViejo || resultado.CelularNuevo != nuevo || resultado.Correo != correo {
		t.Fatalf("resultado = %+v, quiero confirmado con viejo %q y nuevo %q", resultado, celularViejo, nuevo)
	}
	credencial := leerCredencialDeCambio(t, pool, credencialID)
	if credencial.Celular != nuevo || !credencial.CelularVerificadoEn.Valid || !credencial.CelularCambiadoEn.Valid {
		t.Fatalf("credencial = %+v, quiero celular nuevo con verificación y cambio estampados", credencial)
	}
	if !codigoUsado(t, pool, credencialID, codigos[0]) {
		t.Fatal("el código de respaldo no quedó marcado como usado")
	}
	if activas, err := m.sesiones.ListarActivas(t.Context(), parteID); err != nil || len(activas) != 0 {
		t.Fatalf("sesiones activas = %d (err %v), quiero 0", len(activas), err)
	}
	revocado, err := m.denylist.Revocado(t.Context(), sesion.FamiliaID)
	if err != nil || !revocado {
		t.Fatalf("sid revocado = %v (err %v), quiero true", revocado, err)
	}
	detalle := detalleDelEventoDeCambio(t, pool, credencialID, resultadoCambioConfirmado)
	if !strings.Contains(detalle, "****"+nuevo[len(nuevo)-4:]) || strings.Contains(detalle, nuevo) {
		t.Fatalf("detalle = %q, quiero el celular enmascarado y nunca completo", detalle)
	}
	if contarEventosDeCambio(t, pool, credencialID, resultadoCambioConfirmado) != 1 {
		t.Fatalf("eventos confirmados = %d, quiero 1", contarEventosDeCambio(t, pool, credencialID, resultadoCambioConfirmado))
	}
}

func TestCambioDeNumeroCaminoBCompleto(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour)
	credencialID, parteID, _, celularViejo := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)
	nuevo := celularDePrueba()

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, nuevo, "contrasena-correcta", nil, "10.0.0.2")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	if desafio.Proposito != PropositoCambioNumeroViejo {
		t.Fatalf("proposito = %q, quiero %q", desafio.Proposito, PropositoCambioNumeroViejo)
	}
	if codigo, ok := m.canal.UltimoCodigo(celularViejo); !ok || codigo != "123456" {
		t.Fatalf("el número viejo recibió %q/%v, quiero el código del padre", codigo, ok)
	}

	primero, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.2")
	if err != nil {
		t.Fatalf("primer confirm: %v", err)
	}
	if primero.Confirmado {
		t.Fatal("el primer confirm no debe dar el cambio por aplicado")
	}
	if codigo, ok := m.canal.UltimoCodigo(nuevo); !ok || codigo != "123456" {
		t.Fatalf("el número nuevo recibió %q/%v, quiero el código del hijo", codigo, ok)
	}
	if leerCredencialDeCambio(t, pool, credencialID).Celular != celularViejo {
		t.Fatal("la credencial cambió antes de verificar el número nuevo")
	}

	segundo, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.2")
	if err != nil {
		t.Fatalf("segundo confirm: %v", err)
	}
	if !segundo.Confirmado || leerCredencialDeCambio(t, pool, credencialID).Celular != nuevo {
		t.Fatalf("segundo confirm = %+v, quiero la credencial en el número nuevo", segundo)
	}

	if _, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.2"); !errors.Is(err, ErrDesafioInvalido) {
		t.Fatalf("tercer confirm err = %v, quiero ErrDesafioInvalido", err)
	}
}

func TestCambioDeNumeroDejaElViejoFueraDelAncla(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour)
	_, parteID, correo, celularViejo := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)
	nuevo := celularDePrueba()

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, nuevo, "contrasena-correcta", nil, "10.0.0.3")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	if _, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.3"); err != nil {
		t.Fatalf("primer confirm: %v", err)
	}
	if _, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.3"); err != nil {
		t.Fatalf("segundo confirm: %v", err)
	}
	codigoViejo, okViejo := m.canal.UltimoCodigo(celularViejo)
	if !okViejo {
		t.Fatal("el número viejo no recibió el código del padre en el camino B")
	}

	if _, err := m.recuperacion.SolicitarRecuperacion(t.Context(), correo, "10.9.9.9"); err != nil {
		t.Fatalf("SolicitarRecuperacion: %v", err)
	}
	if _, ok := m.canal.UltimoCodigo(nuevo); !ok {
		t.Fatal("la recuperación no entregó el código al número nuevo")
	}
	if actual, _ := m.canal.UltimoCodigo(celularViejo); actual != codigoViejo {
		t.Fatalf("UltimoCodigo(viejo) pasó de %q a %q", codigoViejo, actual)
	}
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (repositorio.Credencial, error) {
			nuevaParte := uuid.New()
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: pgUUID(nuevaParte), Tipo: "persona"}); err != nil {
				return repositorio.Credencial{}, err
			}
			return q.CrearCredencial(ctx, repositorio.CrearCredencialParams{
				ID:            pgUUID(uuid.New()),
				ParteID:       pgUUID(nuevaParte),
				Correo:        uuid.NewString() + "@centinela.test",
				Celular:       celularViejo,
				ValorDerivado: pgtype.Text{},
				Estado:        string(EstadoSinLogin),
			})
		}); err != nil {
		t.Fatalf("el número viejo no quedó libre: %v", err)
	}
}

func TestCambioDeNumeroSextaSolicitudResponde429(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour)
	_, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)

	for i := 0; i < 5; i++ {
		if _, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, celularDePrueba(), "contrasena-correcta", nil, "10.0.0.4"); err != nil {
			t.Fatalf("solicitud %d: %v", i+1, err)
		}
	}
	if _, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, celularDePrueba(), "contrasena-correcta", nil, "10.0.0.4"); !errors.Is(err, ErrDemasiadosEnvios) {
		t.Fatalf("sexta solicitud err = %v, quiero ErrDemasiadosEnvios", err)
	}
}

func TestCambioDeNumeroEnfriamiento(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, time.Hour)
	credencialID, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 2)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)
	nuevo := celularDePrueba()
	segundo := celularDePrueba()

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, nuevo, "contrasena-correcta", &codigos[0], "10.0.0.5")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	if _, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.5"); err != nil {
		t.Fatalf("ConfirmarCambioDeNumero: %v", err)
	}
	if _, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, segundo, "contrasena-correcta", &codigos[1], "10.0.0.5"); !errors.Is(err, ErrCambioNumeroEnEnfriamiento) {
		t.Fatalf("segundo cambio err = %v, quiero ErrCambioNumeroEnEnfriamiento", err)
	}
	envejecerCambioDeNumero(t, credencialID, 2*time.Hour)
	if _, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, segundo, "contrasena-correcta", &codigos[1], "10.0.0.5"); err != nil {
		t.Fatalf("fuera de la ventana err = %v, quiero nil", err)
	}
}

func TestCambioDeNumeroNumeroEnUsoResponde409(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour)
	_, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)
	enUso := celularDePrueba()
	crearCredencialConCelular(t, pool, m.derivador, enUso)

	_, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, enUso, "contrasena-correcta", nil, "10.0.0.6")
	if !errors.Is(err, ErrIdentificadorEnUso) && !errors.Is(MapearError(err), ErrIdentificadorEnUso) {
		t.Fatalf("err = %v, quiero ErrIdentificadorEnUso", err)
	}
}

func TestCambioDeNumeroSeCuelaAntesDelConfirmResponde409(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour)
	credencialID, parteID, _, celularViejo := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 2)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)
	nuevo := celularDePrueba()

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, nuevo, "contrasena-correcta", &codigos[0], "10.0.0.7")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	crearCredencialConCelular(t, pool, m.derivador, nuevo)

	_, err = m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.7")
	var apiErr *platformerrors.APIError
	if mapeado := MapearError(err); !errors.As(mapeado, &apiErr) || apiErr.Code != "DuplicateIdentifier" || apiErr.Status != 409 {
		t.Fatalf("confirm err = %v (mapeado %v), quiero DuplicateIdentifier 409", err, mapeado)
	}
	if leerCredencialDeCambio(t, pool, credencialID).Celular != celularViejo {
		t.Fatal("la credencial cambió pese al conflicto de unicidad")
	}
}

func TestCambioDeNumeroConcurrenteCambiaUnaVez(t *testing.T) {
	pool := abrirPoolConcurrenteDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour)
	credencialID, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 2)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)
	nuevo := celularDePrueba()

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, nuevo, "contrasena-correcta", &codigos[0], "10.0.0.8")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		errores []error
	)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.servicio.ConfirmarCambioDeNumero(context.Background(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.8")
			mu.Lock()
			errores = append(errores, err)
			mu.Unlock()
		}()
	}
	wg.Wait()

	exitos := 0
	for _, err := range errores {
		if err == nil {
			exitos++
		}
	}
	if exitos != 1 {
		t.Fatalf("confirmaciones exitosas = %d (%v), quiero 1", exitos, errores)
	}
	if leerCredencialDeCambio(t, pool, credencialID).Celular != nuevo {
		t.Fatal("la credencial no quedó en el número nuevo")
	}
	if contarEventosDeCambio(t, pool, credencialID, resultadoCambioConfirmado) != 1 {
		t.Fatalf("eventos confirmados = %d, quiero 1", contarEventosDeCambio(t, pool, credencialID, resultadoCambioConfirmado))
	}
	if estado, _ := leerEstadoDesafio(t, pool, desafio.ID); estado != estadoDesafioVerificado {
		t.Fatalf("estado del desafío = %q, quiero %q", estado, estadoDesafioVerificado)
	}
}

func TestCambioDeNumeroBajoContextoEmpresaCambiaLaPersona(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour)
	credencialID, parteID, _, _ := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 2)
	empresaID, _ := crearRolDeEmpresaDeInicioSesion(t, pool, parteID)
	sesion := sesionDeCambio(t, m, parteID, ContextoDeEmpresa(empresaID))
	nuevo := celularDePrueba()

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, nuevo, "contrasena-correcta", &codigos[0], "10.0.0.9")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	if desafio.Proposito != PropositoCambioNumero {
		t.Fatalf("proposito = %q, quiero %q", desafio.Proposito, PropositoCambioNumero)
	}
	if _, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.9"); err != nil {
		t.Fatalf("ConfirmarCambioDeNumero: %v", err)
	}
	if leerCredencialDeCambio(t, pool, credencialID).Celular != nuevo {
		t.Fatal("la credencial de la persona no cambió")
	}
	totalEmpresa := 0
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, "SELECT count(*) FROM auth.credenciales WHERE parte_id = $1", pgUUID(empresaID)).Scan(&totalEmpresa)
		}); err != nil {
		t.Fatalf("contar credenciales de la empresa: %v", err)
	}
	if totalEmpresa != 0 {
		t.Fatalf("la empresa tiene %d credenciales, quiero 0", totalEmpresa)
	}
}

// sembrarOptInDeParte inserta el último consentimiento de WhatsApp de la parte.
func sembrarOptInDeParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID, aceptado bool) {
	tb.Helper()

	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			_, err := q.CrearConsentimiento(ctx, repositorio.CrearConsentimientoParams{
				ID:           pgUUID(uuid.New()),
				ParteID:      pgUUID(parteID),
				Tipo:         TipoConsentimientoWhatsApp,
				VersionTexto: "v1",
				Texto:        "marcador de prueba",
				Aceptado:     aceptado,
				Accion:       "otorgar",
				Origen:       "10.0.0.1",
				Metodo:       "test",
				OcurridoEn:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			})
			return struct{}{}, err
		}); err != nil {
		tb.Fatalf("sembrar el opt-in de la parte: %v", err)
	}
}

// avisoDeCambioEnOutbox es la fila del aviso del cambio que el test inspecciona.
type avisoDeCambioEnOutbox struct {
	destino          string
	correo           string
	celular          string
	nuevoEnmascarado string
	contactoSoporte  string
	ultimoError      string
	entregado        bool
}

// leerAvisosDeCambio lee como rol_auth los avisos del cambio que tocan el correo o el celular.
func leerAvisosDeCambio(tb testing.TB, pool *pgxpool.Pool, correo, celular string) []avisoDeCambioEnOutbox {
	tb.Helper()

	var filas []avisoDeCambioEnOutbox
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx, `SELECT payload->>'destino', coalesce(payload->>'correo',''), coalesce(payload->>'celular',''),
					coalesce(payload->>'numero_nuevo_enmascarado',''), coalesce(payload->>'contacto_soporte',''),
					coalesce(ultimo_error,''), despachado_en IS NOT NULL
				  FROM auth.outbox
				 WHERE tipo = $1 AND (payload->>'correo' = $2 OR payload->>'celular' = $3)
				 ORDER BY id`, TipoAvisoCambioNumero, correo, celular)
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var fila avisoDeCambioEnOutbox
				if err := rows.Scan(&fila.destino, &fila.correo, &fila.celular, &fila.nuevoEnmascarado, &fila.contactoSoporte, &fila.ultimoError, &fila.entregado); err != nil {
					return struct{}{}, err
				}
				filas = append(filas, fila)
			}
			return struct{}{}, rows.Err()
		}); err != nil {
		tb.Fatalf("leer los avisos del cambio: %v", err)
	}
	return filas
}

// canalWhatsAppDeAvisos guarda en memoria el último aviso entregado a cada celular.
type canalWhatsAppDeAvisos struct {
	mu      sync.Mutex
	ultimos map[string]string
}

// nuevoCanalWhatsAppDeAvisos arma el simulador en memoria de avisos por WhatsApp.
func nuevoCanalWhatsAppDeAvisos() *canalWhatsAppDeAvisos {
	return &canalWhatsAppDeAvisos{ultimos: map[string]string{}}
}

// Enviar guarda el aviso del celular y devuelve un id determinista.
func (c *canalWhatsAppDeAvisos) Enviar(_ context.Context, celular, texto string) (string, error) {
	c.mu.Lock()
	c.ultimos[celular] = texto
	c.mu.Unlock()
	return "wa:" + celular, nil
}

// UltimoAviso devuelve el último aviso guardado para el celular y si existe.
func (c *canalWhatsAppDeAvisos) UltimoAviso(celular string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	texto, ok := c.ultimos[celular]
	return texto, ok
}

// registradorEventosQueFalla falla siempre para forzar la reversión del cambio.
type registradorEventosQueFalla struct{}

// Registrar devuelve siempre el error de prueba.
func (registradorEventosQueFalla) Registrar(context.Context, Evento) error {
	return errors.New("fallo de eventos forzado")
}

// TestHUAUT08DobleAvisoDelCambioDeNumero cubre los criterios 3 y 5: los dos avisos y la remisión a soporte.
func TestHUAUT08DobleAvisoDelCambioDeNumero(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour, conLectorOptIn(NuevoLectorOptInDeConsentimientos(pool)))
	credencialID, parteID, correo, celularViejo := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	sembrarOptInDeParte(t, pool, parteID, true)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 2)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)
	nuevo := celularDePrueba()

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, nuevo, "contrasena-correcta", &codigos[0], "10.0.0.20")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	if _, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.20"); err != nil {
		t.Fatalf("ConfirmarCambioDeNumero: %v", err)
	}

	filas := leerAvisosDeCambio(t, pool, correo, celularViejo)
	if len(filas) != 2 {
		t.Fatalf("avisos del cambio = %d, quiero 2: %+v", len(filas), filas)
	}
	porDestino := map[string]avisoDeCambioEnOutbox{}
	enmascarado := "****" + nuevo[len(nuevo)-4:]
	for _, fila := range filas {
		porDestino[fila.destino] = fila
		if fila.nuevoEnmascarado != enmascarado {
			t.Errorf("destino %s: enmascarado = %q, quiero %q", fila.destino, fila.nuevoEnmascarado, enmascarado)
		}
		if fila.contactoSoporte != contactoSoporteDePrueba {
			t.Errorf("destino %s: contacto de soporte = %q, quiero %q", fila.destino, fila.contactoSoporte, contactoSoporteDePrueba)
		}
		if strings.Contains(fila.nuevoEnmascarado, nuevo) || strings.Contains(fila.contactoSoporte, "123456") {
			t.Errorf("el aviso filtra el número completo o un código: %+v", fila)
		}
	}
	whatsapp, okWhatsApp := porDestino[DestinoWhatsApp]
	if !okWhatsApp || whatsapp.celular != celularViejo {
		t.Fatalf("el aviso de WhatsApp no fue al número viejo: %+v", whatsapp)
	}
	correoAviso, okCorreo := porDestino[DestinoCorreo]
	if !okCorreo || correoAviso.correo != correo {
		t.Fatalf("el aviso de correo no fue al correo registrado: %+v", correoAviso)
	}
	if contarEventosDeCambio(t, pool, credencialID, resultadoCambioNotificado) != 1 {
		t.Fatalf("eventos notificado = %d, quiero 1", contarEventosDeCambio(t, pool, credencialID, resultadoCambioNotificado))
	}

	correoSim := correosim.Nuevo()
	whatsappSim := nuevoCanalWhatsAppDeAvisos()
	despachador := NuevoDespachadorDeAvisos(pool, NuevoNotificadorDeAvisos(correoSim, whatsappSim),
		PoliticaReintento{Base: time.Second, Factor: 2, Tope: time.Minute})
	if err := despachador.DespacharAvisos(t.Context()); err != nil {
		t.Fatalf("despachar: %v", err)
	}
	avisoCorreo, ok := correoSim.UltimoAviso(correo)
	if !ok || !strings.Contains(avisoCorreo.Cuerpo, enmascarado) {
		t.Fatalf("el simulador de correo no recibió el aviso correcto: %+v", avisoCorreo)
	}
	avisoWhatsApp, ok := whatsappSim.UltimoAviso(celularViejo)
	if !ok || !strings.Contains(avisoWhatsApp, contactoSoporteDePrueba) {
		t.Fatalf("el simulador de WhatsApp no recibió el aviso del número viejo: %q", avisoWhatsApp)
	}
	if _, ok := whatsappSim.UltimoAviso(nuevo); ok {
		t.Fatal("el número nuevo recibió un aviso, solo debía recibir el OTP")
	}
	for _, fila := range leerAvisosDeCambio(t, pool, correo, celularViejo) {
		if !fila.entregado || fila.ultimoError != "" {
			t.Errorf("el aviso %s no quedó entregado limpio: %+v", fila.destino, fila)
		}
	}
}

// TestHUAUT08ElAvisoNoQuedaSiElCambioReverta demuestra que el aviso se encola en la misma transacción.
func TestHUAUT08ElAvisoNoQuedaSiElCambioReverta(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour,
		conLectorOptIn(NuevoLectorOptInDeConsentimientos(pool)),
		conRegistradorEventos(registradorEventosQueFalla{}))
	credencialID, parteID, correo, celularViejo := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	sembrarOptInDeParte(t, pool, parteID, true)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 2)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, celularDePrueba(), "contrasena-correcta", &codigos[0], "10.0.0.21")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	if _, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.21"); err == nil {
		t.Fatal("el confirm debía fallar por el registrador forzado")
	}
	if filas := leerAvisosDeCambio(t, pool, correo, celularViejo); len(filas) != 0 {
		t.Fatalf("avisos tras revertir = %d, quiero 0: %+v", len(filas), filas)
	}
	if leerCredencialDeCambio(t, pool, credencialID).Celular != celularViejo {
		t.Fatal("la credencial cambió pese a la reversión")
	}
}

// TestHUAUT08SinOptInSoloAvisaPorCorreo cubre el destino de WhatsApp condicionado al opt-in.
func TestHUAUT08SinOptInSoloAvisaPorCorreo(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	m := montajeCambioDePrueba(t, pool, 24*time.Hour, conLectorOptIn(NuevoLectorOptInDeConsentimientos(pool)))
	credencialID, parteID, correo, celularViejo := sembrarCredencialDeInicioSesion(t, pool, m.derivador, "contrasena-correcta", EstadoActivo, true)
	sembrarOptInDeParte(t, pool, parteID, false)
	codigos := sembrarCodigosRespaldo(t, pool, credencialID, 2)
	sesion := sesionDeCambio(t, m, parteID, ContextoUsuario)

	desafio, err := m.servicio.IniciarCambioDeNumero(t.Context(), sesion.FamiliaID, celularDePrueba(), "contrasena-correcta", &codigos[0], "10.0.0.22")
	if err != nil {
		t.Fatalf("IniciarCambioDeNumero: %v", err)
	}
	if _, err := m.servicio.ConfirmarCambioDeNumero(t.Context(), sesion.FamiliaID, desafio.ID, "123456", "10.0.0.22"); err != nil {
		t.Fatalf("ConfirmarCambioDeNumero: %v", err)
	}

	filas := leerAvisosDeCambio(t, pool, correo, celularViejo)
	if len(filas) != 1 || filas[0].destino != DestinoCorreo || filas[0].correo != correo {
		t.Fatalf("avisos sin opt-in = %+v, quiero solo el de correo al correo registrado", filas)
	}
	if contarEventosDeCambio(t, pool, credencialID, resultadoCambioNotificadoSoloCorreo) != 1 {
		t.Fatalf("eventos notificado_solo_correo = %d, quiero 1", contarEventosDeCambio(t, pool, credencialID, resultadoCambioNotificadoSoloCorreo))
	}
	if contarEventosDeCambio(t, pool, credencialID, resultadoCambioNotificado) != 0 {
		t.Fatal("no debía quedar evento notificado con el opt-in revocado")
	}
}
