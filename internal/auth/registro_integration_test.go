//go:build integration

package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth/internal/repositorio"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// contadoresDeRegistro es el puerto Contadores en memoria que el racionamiento necesita.
type contadoresDeRegistro struct {
	mu      sync.Mutex
	valores map[string]int64
}

func (c *contadoresDeRegistro) Incrementar(_ context.Context, clave string, _ time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valores[clave]++
	return c.valores[clave], nil
}

func (c *contadoresDeRegistro) Bloquear(context.Context, string, time.Duration) error { return nil }
func (c *contadoresDeRegistro) Bloqueada(context.Context, string) (bool, error)       { return false, nil }

// creadorDesafioFalso devuelve un desafio fijo sin tocar la base ni Redis.
type creadorDesafioFalso struct {
	desafio Desafio
	err     error
}

func (c creadorDesafioFalso) CrearDesafioRacionado(context.Context, Solicitud) (Desafio, error) {
	if c.err != nil {
		return Desafio{}, c.err
	}
	return c.desafio, nil
}

// abridorDeSubcuentasFalso registra las llamadas sin abrir nada en el ledger.
type abridorDeSubcuentasFalso struct {
	err      error
	llamadas []string
}

func (a *abridorDeSubcuentasFalso) AbrirSubcuentasDeUsuario(_ context.Context, usuarioID string) (int64, error) {
	a.llamadas = append(a.llamadas, usuarioID)
	return 2, a.err
}

// celularUnicoDeRegistro genera un movil colombiano valido y distinto por llamada.
func celularUnicoDeRegistro() string {
	id := uuid.New()
	n := int64(id[0])<<24 | int64(id[1])<<16 | int64(id[2])<<8 | int64(id[3])
	return fmt.Sprintf("+57300%07d", n%10000000)
}

// solicitudValidaDeRegistro arma el alta con los dos consentimientos obligatorios aceptados.
func solicitudValidaDeRegistro(correo, celular string) SolicitudDeRegistro {
	return SolicitudDeRegistro{
		Correo:          correo,
		Contrasena:      "contrasena-de-prueba",
		Celular:         celular,
		NombreCompleto:  "Ana Centinela",
		TipoDocumento:   "CC",
		NumeroDocumento: "DOC-" + uuid.NewString(),
		Consentimientos: []SolicitudDeConsentimiento{
			{Tipo: TipoConsentimientoTratamiento, VersionTexto: "v1", Aceptado: true},
			{Tipo: TipoConsentimientoWhatsApp, VersionTexto: "v1", Aceptado: true},
		},
		Origen:    "127.0.0.1",
		UserAgent: "prueba-integracion",
	}
}

// derivadorDeRegistro arma Argon2id con los parametros de prueba.
func derivadorDeRegistro(tb testing.TB) Derivador {
	tb.Helper()
	derivador, err := NuevoDerivadorArgon2(ParametrosArgon2DePrueba())
	if err != nil {
		tb.Fatalf("NuevoDerivadorArgon2: %v", err)
	}
	return derivador
}

// registradorConFakes usa desafios y subcuentas falsos para aislar la unicidad.
func registradorConFakes(tb testing.TB, pool *pgxpool.Pool) *RegistradorDePersonas {
	tb.Helper()
	registrador, err := NuevoRegistradorDePersonas(pool, derivadorDeRegistro(tb), NuevoRegistradorEventos(pool),
		creadorDesafioFalso{desafio: Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoRegistro}},
		&abridorDeSubcuentasFalso{})
	if err != nil {
		tb.Fatalf("NuevoRegistradorDePersonas: %v", err)
	}
	return registrador
}

// gestorDeRegistro arma el gestor real de A2-06 con racionamiento en memoria.
func gestorDeRegistro(tb testing.TB, pool *pgxpool.Pool, canal CanalOTP) *GestorDesafios {
	tb.Helper()
	claves, err := NuevasClaves(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		tb.Fatalf("NuevasClaves: %v", err)
	}
	gestor, err := NuevoGestorDesafios(pool, canal, NuevoRegistradorEventos(pool), NuevoLectorOptInDeConsentimientos(pool), pepperDeDesafio,
		ConRacionamiento(&contadoresDeRegistro{valores: map[string]int64{}}, claves))
	if err != nil {
		tb.Fatalf("NuevoGestorDeDesafios: %v", err)
	}
	return gestor
}

// leerCredencialDeRegistro lee como rol_auth el estado y el PHC de la credencial de la parte.
func leerCredencialDeRegistro(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) (string, string) {
	tb.Helper()
	var estado, phc string
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT estado, valor_derivado FROM auth.credenciales WHERE parte_id = $1`, uuidOPgtype(parteID)).Scan(&estado, &phc)
		}); err != nil {
		tb.Fatalf("leer la credencial de %s: %v", parteID, err)
	}
	return estado, phc
}

// leerConsentimientosDeParte devuelve el texto persistido por tipo.
func leerConsentimientosDeParte(tb testing.TB, pool *pgxpool.Pool, parteID uuid.UUID) map[string]string {
	tb.Helper()
	filas := map[string]string{}
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			rows, err := tx.Query(ctx, `SELECT tipo, texto FROM auth.consentimientos WHERE parte_id = $1`, uuidOPgtype(parteID))
			if err != nil {
				return struct{}{}, err
			}
			defer rows.Close()
			for rows.Next() {
				var tipo, texto string
				if err := rows.Scan(&tipo, &texto); err != nil {
					return struct{}{}, err
				}
				filas[tipo] = texto
			}
			return struct{}{}, rows.Err()
		}); err != nil {
		tb.Fatalf("leer los consentimientos: %v", err)
	}
	return filas
}

// leerTextoDeCatalogo lee el texto vigente de un tipo y version.
func leerTextoDeCatalogo(tb testing.TB, pool *pgxpool.Pool, tipo, version string) string {
	tb.Helper()
	var texto string
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT texto FROM auth.textos_consentimiento WHERE tipo = $1 AND version = $2`, tipo, version).Scan(&texto)
		}); err != nil {
		tb.Fatalf("leer el texto del catalogo: %v", err)
	}
	return texto
}

// contarCredencialesPorCorreo cuenta como rol_auth las credenciales con ese correo.
func contarCredencialesPorCorreo(tb testing.TB, pool *pgxpool.Pool, correo string) int {
	tb.Helper()
	var total int
	if _, err := enTransaccion(tb.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `SELECT count(*) FROM auth.credenciales WHERE correo = $1`, correo).Scan(&total)
		}); err != nil {
		tb.Fatalf("contar credenciales: %v", err)
	}
	return total
}

// contarSubcuentasDeUsuario lee nucleo.cuenta como rol_nucleo, solo lectura.
func contarSubcuentasDeUsuario(tb testing.TB, pool *pgxpool.Pool, usuarioID uuid.UUID) int {
	tb.Helper()
	tx, err := pool.Begin(tb.Context())
	if err != nil {
		tb.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(tb.Context(), "SET LOCAL ROLE rol_nucleo"); err != nil {
		tb.Fatalf("SET LOCAL ROLE rol_nucleo: %v", err)
	}
	var total int
	if err := tx.QueryRow(tb.Context(), `SELECT count(*) FROM nucleo.cuenta WHERE titular_id = $1`, usuarioID.String()).Scan(&total); err != nil {
		tb.Fatalf("contar subcuentas: %v", err)
	}
	return total
}

// exigirConsentimientosCopiados comprueba que el texto persistido salio del catalogo del servidor.
func exigirConsentimientosCopiados(t *testing.T, pool *pgxpool.Pool, parteID uuid.UUID) {
	t.Helper()
	filas := leerConsentimientosDeParte(t, pool, parteID)
	if len(filas) != 2 {
		t.Fatalf("consentimientos = %d, quiero 2", len(filas))
	}
	for tipo, texto := range filas {
		catalogo := leerTextoDeCatalogo(t, pool, tipo, "v1")
		if texto != catalogo || !strings.Contains(texto, "MARCADOR PENDIENTE") {
			t.Errorf("texto de %s = %q, quiero el del catalogo %q", tipo, texto, catalogo)
		}
	}
	var otorgadas int
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `SELECT count(*) FROM auth.consentimientos WHERE parte_id = $1 AND accion = 'otorgar' AND aceptado`,
				uuidOPgtype(parteID)).Scan(&otorgadas)
		}); err != nil {
		t.Fatalf("contar las acciones otorgar: %v", err)
	}
	if otorgadas != 2 {
		t.Errorf("filas con accion otorgar = %d, quiero 2 (vocabulario del contrato)", otorgadas)
	}
}

func TestRegistroValidoCreaLaCuentaPendiente(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	canal := &canalConFijo{codigo: "123456"}
	servicio := nucleo.NewServicio(pool, nil, nil, nil, nil)
	registrador, err := NuevoRegistradorDePersonas(pool, derivadorDeRegistro(t), NuevoRegistradorEventos(pool), gestorDeRegistro(t, pool, canal), servicio)
	if err != nil {
		t.Fatalf("NuevoRegistradorDePersonas: %v", err)
	}

	correo, celular := "reg-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro()
	registro, err := registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro(correo, celular))
	if err != nil {
		t.Fatalf("RegistrarPersona: %v", err)
	}

	if registro.Estado != EstadoPendienteVerificacion || registro.Correo != correo || registro.Celular != celular {
		t.Errorf("registro = %+v, quiero la cuenta pendiente con correo %q y celular %q", registro, correo, celular)
	}
	if registro.Desafio.ID == uuid.Nil || registro.Desafio.Proposito != PropositoRegistro {
		t.Errorf("desafio = %+v, quiero uno de proposito registro", registro.Desafio)
	}
	if canal.ultimoCelular != celular || canal.ultimoTexto != "123456" {
		t.Errorf("simulador recibio (%q, %q), quiero (%q, 123456)", canal.ultimoCelular, canal.ultimoTexto, celular)
	}

	estado, phc := leerCredencialDeRegistro(t, pool, registro.ParteID)
	if estado != string(EstadoPendienteVerificacion) {
		t.Errorf("estado persistido = %q, quiero %q", estado, EstadoPendienteVerificacion)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Errorf("valor_derivado = %q, quiero la cabecera PHC de argon2id", phc)
	}
	if contarEventosDeParte(t, pool, registro.ParteID, TipoEventoRegistro, resultadoRegistroCreado) != 1 {
		t.Error("no quedo el evento registro/creado")
	}
	exigirConsentimientosCopiados(t, pool, registro.ParteID)
	if total := contarSubcuentasDeUsuario(t, pool, registro.ParteID); total != 2 {
		t.Errorf("subcuentas en nucleo.cuenta = %d, quiero 2", total)
	}
}

func TestRegistroConCorreoRepetidoEsGenerico(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	registrador := registradorConFakes(t, pool)
	correo := "dup-correo-" + uuid.NewString() + "@centinela.test"

	if _, err := registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro(correo, celularUnicoDeRegistro())); err != nil {
		t.Fatalf("primer registro: %v", err)
	}
	_, err := registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro(correo, celularUnicoDeRegistro()))
	exigirConflictoGenerico(t, err)
	if total := contarCredencialesPorCorreo(t, pool, correo); total != 1 {
		t.Errorf("credenciales con el correo = %d, quiero 1", total)
	}
}

// exigirConflictoGenerico exige ErrIdentificadorEnUso y su mapeo a 409 DuplicateIdentifier.
func exigirConflictoGenerico(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrIdentificadorEnUso) {
		t.Fatalf("err = %v, quiero ErrIdentificadorEnUso", err)
	}
	var apiErr *platformerrors.APIError
	if !errors.As(MapearError(err), &apiErr) || apiErr.Status != 409 || apiErr.Code != "DuplicateIdentifier" {
		t.Fatalf("mapeo = %v, quiero 409 DuplicateIdentifier", MapearError(err))
	}
}

func TestRegistroConCelularRepetidoEsGenerico(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	registrador := registradorConFakes(t, pool)
	celular := celularUnicoDeRegistro()

	if _, err := registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro("dup-cel-"+uuid.NewString()+"@centinela.test", celular)); err != nil {
		t.Fatalf("primer registro: %v", err)
	}
	_, err := registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro("dup-cel-"+uuid.NewString()+"@centinela.test", celular))
	exigirConflictoGenerico(t, err)
}

func TestRegistroConcurrenteConElMismoCorreo(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	registrador := registradorConFakes(t, pool)
	correo := "conc-" + uuid.NewString() + "@centinela.test"

	resultados := make([]error, 2)
	var wg sync.WaitGroup
	for i := range resultados {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, resultados[i] = registrador.RegistrarPersona(context.Background(), solicitudValidaDeRegistro(correo, celularUnicoDeRegistro()))
		}(i)
	}
	wg.Wait()

	exitos, repetidos := 0, 0
	for _, err := range resultados {
		switch {
		case err == nil:
			exitos++
		case errors.Is(err, ErrIdentificadorEnUso):
			repetidos++
		default:
			t.Errorf("error inesperado: %v", err)
		}
	}
	if exitos != 1 || repetidos != 1 {
		t.Fatalf("exitos = %d, repetidos = %d; quiero 1 y 1", exitos, repetidos)
	}
	if total := contarCredencialesPorCorreo(t, pool, correo); total != 1 {
		t.Errorf("credenciales con el correo = %d, quiero 1", total)
	}
}

func TestConsentimientoRechazaUpdateYDelete(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	ctx := context.Background()
	parteID := uuid.New()
	if _, err := enTransaccion(ctx, poolDeAuth{pool: pool},
		func(ctx context.Context, _ transaccionDeAuth, q *repositorio.Queries) (struct{}, error) {
			if _, err := q.CrearParte(ctx, repositorio.CrearParteParams{ID: uuidOPgtype(parteID), Tipo: "persona"}); err != nil {
				return struct{}{}, err
			}
			_, err := q.CrearConsentimiento(ctx, repositorio.CrearConsentimientoParams{
				ID: uuidOPgtype(uuid.New()), ParteID: uuidOPgtype(parteID), Tipo: TipoConsentimientoWhatsApp,
				VersionTexto: "v1", Texto: "marcador", Aceptado: true, Accion: AccionConsentimientoOtorgar, Origen: "127.0.0.1",
				UserAgent: "test", Metodo: metodoRegistro, OcurridoEn: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
			})
			return struct{}{}, err
		}); err != nil {
		t.Fatalf("sembrar el consentimiento: %v", err)
	}

	exigirPermisoDenegado(t, intentarEscrituraDeConsentimiento(t, ctx, pool, "UPDATE auth.consentimientos SET texto = 'reescrito'"),
		"permission denied for table consentimientos")
	exigirPermisoDenegado(t, intentarEscrituraDeConsentimiento(t, ctx, pool, "DELETE FROM auth.consentimientos"),
		"permission denied for table consentimientos")
}

// intentarEscrituraDeConsentimiento ejecuta una escritura como rol_auth en su propia transaccion.
func intentarEscrituraDeConsentimiento(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sql string) error {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, sentenciaCambioDeRol); err != nil {
		t.Fatalf("SET LOCAL ROLE %s: %v", RolAuth, err)
	}
	_, err = tx.Exec(ctx, sql)
	return err
}

func TestRegistroEncolaCompensacionSiFallaLaApertura(t *testing.T) {
	pool := abrirPoolDeAuth(t)
	derivador := derivadorDeRegistro(t)
	subcuentas := &abridorDeSubcuentasFalso{err: errors.New("ledger caido")}
	registrador, err := NuevoRegistradorDePersonas(pool, derivador, NuevoRegistradorEventos(pool),
		creadorDesafioFalso{desafio: Desafio{ID: uuid.New(), ExpiraEnSegundos: 300, Proposito: PropositoRegistro}}, subcuentas)
	if err != nil {
		t.Fatalf("NuevoRegistradorDePersonas: %v", err)
	}

	registro, err := registrador.RegistrarPersona(t.Context(), solicitudValidaDeRegistro("comp-"+uuid.NewString()+"@centinela.test", celularUnicoDeRegistro()))
	if err != nil {
		t.Fatalf("RegistrarPersona: %v", err)
	}
	if len(subcuentas.llamadas) != 1 || subcuentas.llamadas[0] != registro.ParteID.String() {
		t.Fatalf("llamadas a subcuentas = %v, quiero la parte %s", subcuentas.llamadas, registro.ParteID)
	}
	var pendientes int
	if _, err := enTransaccion(t.Context(), poolDeAuth{pool: pool},
		func(ctx context.Context, tx transaccionDeAuth, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx,
				`SELECT count(*) FROM auth.outbox WHERE tipo = $1 AND despachado_en IS NULL`, TipoAvisoAbrirSubcuentas).Scan(&pendientes)
		}); err != nil {
		t.Fatalf("contar avisos del outbox: %v", err)
	}
	if pendientes != 1 {
		t.Errorf("avisos abrir_subcuentas pendientes = %d, quiero 1", pendientes)
	}
}
