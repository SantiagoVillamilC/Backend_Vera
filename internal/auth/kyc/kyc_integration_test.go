//go:build integration

package kyc

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/auth"
	"github.com/SantiagoVillamilC/Backend_Centinela/internal/nucleo"
	platformerrors "github.com/SantiagoVillamilC/Backend_Centinela/internal/platform/errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// envDeIntegracion lee una variable de entorno del sandbox.
func envDeIntegracion(clave string) string { return os.Getenv(clave) }

// abrirPoolDeKYC abre el pool contra APP_DATABASE_URL, obligatoria bajo el tag integration.
func abrirPoolDeKYC(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := lookupDSN(t)
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("abrir pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	return pool
}

// lookupDSN exige la cadena del sandbox: sin Postgres real el test es un hallazgo, no un skip.
func lookupDSN(t *testing.T) string {
	t.Helper()
	dsn := envDeIntegracion("APP_DATABASE_URL")
	if dsn == "" {
		t.Fatalf("APP_DATABASE_URL es obligatoria bajo el tag integration")
	}
	return dsn
}

// sembrarParteKYC siembra una parte del tipo pedido con rol_auth y la deja para la prueba.
func sembrarParteKYC(t *testing.T, pool *pgxpool.Pool, tipo string) string {
	t.Helper()
	id := uuid.New()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+auth.RolAuth); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if _, err := tx.Exec(t.Context(), "INSERT INTO auth.partes (id, tipo) VALUES ($1, $2)", id, tipo); err != nil {
		t.Fatalf("sembrar parte %s: %v", tipo, err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return id.String()
}

// contarFilasKYC cuenta las filas de la consulta con rol_auth.
func contarFilasKYC(t *testing.T, pool *pgxpool.Pool, consulta string, args ...any) int {
	t.Helper()
	var n int
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+auth.RolAuth); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := tx.QueryRow(t.Context(), consulta, args...).Scan(&n); err != nil {
		t.Fatalf("contar filas: %v", err)
	}
	return n
}

// leerVerificacion devuelve la última evidencia de la parte y cuántas hay.
func leerVerificacion(t *testing.T, pool *pgxpool.Pool, parteID string) (medio, proveedor, referencia, resultado string, filas int) {
	t.Helper()
	var proveedorPtr, referenciaPtr *string
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+auth.RolAuth); err != nil {
		t.Fatalf("set role: %v", err)
	}
	err = tx.QueryRow(t.Context(), `
		SELECT medio, proveedor, referencia_proveedor, resultado
		FROM auth.kyc_verificaciones
		WHERE parte_id = $1
		ORDER BY id DESC
		LIMIT 1`, parteID).Scan(&medio, &proveedorPtr, &referenciaPtr, &resultado)
	if err != nil {
		t.Fatalf("leer evidencia: %v", err)
	}
	if proveedorPtr != nil {
		proveedor = *proveedorPtr
	}
	if referenciaPtr != nil {
		referencia = *referenciaPtr
	}
	return medio, proveedor, referencia, resultado, contarFilasKYC(t, pool, "SELECT count(*) FROM auth.kyc_verificaciones WHERE parte_id = $1", parteID)
}

// HU-AUT-09 / A11: la persona siempre resuelve al nivel del núcleo.
func TestLectorPersonaEsBajoMonto(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	lector := NuevoLectorNivelKYC(pool)
	persona := sembrarParteKYC(t, pool, TipoPersona)

	nivel, err := lector.NivelKYC(t.Context(), persona)
	if err != nil {
		t.Fatalf("NivelKYC(persona): %v", err)
	}
	if nivel != nucleo.NivelKYCDepositoBajoMonto {
		t.Fatalf("nivel = %q, quiero %q", nivel, nucleo.NivelKYCDepositoBajoMonto)
	}
}

// H5: la empresa responde 403 KycRequired tipado, no un nivel.
func TestLectorEmpresaPidePleno(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	lector := NuevoLectorNivelKYC(pool)
	empresa := sembrarParteKYC(t, pool, TipoEmpresa)

	_, err := lector.NivelKYC(t.Context(), empresa)
	if !errors.Is(err, auth.ErrKYCPlenoRequerido) {
		t.Fatalf("err = %v, quiero auth.ErrKYCPlenoRequerido", err)
	}
	var apiErr *platformerrors.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Code != "KycRequired" {
		t.Fatalf("errors.As(*APIError) = %v, quiero 403/KycRequired", err)
	}
}

// Fail closed: una parte inexistente es error, nunca un nivel por defecto.
func TestLectorParteInexistenteEsError(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	lector := NuevoLectorNivelKYC(pool)

	if _, err := lector.NivelKYC(t.Context(), uuid.NewString()); err == nil {
		t.Fatal("NivelKYC de una parte inexistente no falló")
	}
}

// El usuario de prueba del backfill es persona: bajo monto, para no romper el probe.
func TestLectorUsuarioDePruebaEsBajoMonto(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	lector := NuevoLectorNivelKYC(pool)
	parte := auth.SembrarParteDePrueba(t, pool)

	nivel, err := lector.NivelKYC(t.Context(), parte)
	if err != nil {
		t.Fatalf("NivelKYC(usuario de prueba): %v", err)
	}
	if nivel != nucleo.NivelKYCDepositoBajoMonto {
		t.Fatalf("nivel = %q, quiero %q", nivel, nucleo.NivelKYCDepositoBajoMonto)
	}
}

// Criterio 1: sin fila, el estado deriva por tipo y la empresa queda materializada.
func TestEstadoDerivaYMaterializa(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	servicio := NuevoServicio(pool, nil, "simulador", nil)

	persona := sembrarParteKYC(t, pool, TipoPersona)
	estadoPersona, err := servicio.Estado(t.Context(), persona)
	if err != nil {
		t.Fatalf("Estado(persona): %v", err)
	}
	if estadoPersona.Producto != ProductoBajoMonto || estadoPersona.EstadoKYC != EstadoBasico {
		t.Fatalf("estado persona = %+v, quiero bajo_monto/basico", estadoPersona)
	}
	if filas := contarFilasKYC(t, pool, "SELECT count(*) FROM auth.kyc_estados WHERE parte_id = $1", persona); filas != 0 {
		t.Fatalf("la persona no debe materializar fila, hay %d", filas)
	}

	empresa := sembrarParteKYC(t, pool, TipoEmpresa)
	estadoEmpresa, err := servicio.Estado(t.Context(), empresa)
	if err != nil {
		t.Fatalf("Estado(empresa): %v", err)
	}
	if estadoEmpresa.Producto != ProductoDepositoOrdinario || estadoEmpresa.EstadoKYC != EstadoPendientePleno {
		t.Fatalf("estado empresa = %+v, quiero deposito_ordinario/pendiente_pleno", estadoEmpresa)
	}
	if filas := contarFilasKYC(t, pool, "SELECT count(*) FROM auth.kyc_estados WHERE parte_id = $1 AND producto = 'deposito_ordinario'", empresa); filas != 1 {
		t.Fatalf("la empresa debe quedar materializada, hay %d filas", filas)
	}
}

// Criterio 3: pleno deja evidencia con fecha, medio, proveedor y referencia, y el lector no cambia (A11).
func TestPlenoGuardaEvidenciaYNoCambiaElLector(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	simulador := proveedorFijo{resultado: ResultadoVerificado}
	servicio := NuevoServicio(pool, simulador, "simulador", nil)
	persona := sembrarParteKYC(t, pool, TipoPersona)

	if err := servicio.Verificar(t.Context(), persona, NivelPleno, ""); err != nil {
		t.Fatalf("Verificar(pleno): %v", err)
	}
	medio, proveedor, referencia, resultado, filas := leerVerificacion(t, pool, persona)
	if filas != 1 {
		t.Fatalf("filas de evidencia = %d, quiero 1", filas)
	}
	if medio == "" || proveedor != "simulador" || referencia == "" || resultado != ResultadoVerificado {
		t.Fatalf("evidencia = (medio %q, proveedor %q, referencia %q, resultado %q)", medio, proveedor, referencia, resultado)
	}
	if filas := contarFilasKYC(t, pool, "SELECT count(*) FROM auth.kyc_estados WHERE parte_id = $1 AND estado_kyc = 'pleno'", persona); filas != 1 {
		t.Fatalf("kyc_estados debe quedar pleno, hay %d", filas)
	}

	lector := NuevoLectorNivelKYC(pool)
	nivel, err := lector.NivelKYC(t.Context(), persona)
	if err != nil {
		t.Fatalf("NivelKYC tras pleno: %v", err)
	}
	if nivel != nucleo.NivelKYCDepositoBajoMonto {
		t.Fatalf("nivel = %q, quiero bajo_monto aunque el simulador verificó (A11)", nivel)
	}
}

// Bajo monto de persona no llama al proveedor y deja la evidencia de registro.
func TestBajoMontoDePersonaUsaRegistro(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	servicio := NuevoServicio(pool, proveedorQueFalla{t: t}, "simulador", nil)
	persona := sembrarParteKYC(t, pool, TipoPersona)

	if err := servicio.Verificar(t.Context(), persona, NivelBajoMonto, ""); err != nil {
		t.Fatalf("Verificar(bajo_monto): %v", err)
	}
	medio, _, _, resultado, filas := leerVerificacion(t, pool, persona)
	if filas != 1 || medio != MedioRegistro || resultado != ResultadoVerificado {
		t.Fatalf("evidencia = (medio %q, resultado %q, filas %d), quiero registro/verificado/1", medio, resultado, filas)
	}
}

// H5: bajo monto para una empresa no aplica.
func TestBajoMontoDeEmpresaNoAplica(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	servicio := NuevoServicio(pool, nil, "simulador", nil)
	empresa := sembrarParteKYC(t, pool, TipoEmpresa)

	if err := servicio.Verificar(t.Context(), empresa, NivelBajoMonto, ""); !errors.Is(err, ErrNivelNoAplicaEmpresa) {
		t.Fatalf("err = %v, quiero ErrNivelNoAplicaEmpresa", err)
	}
}

// P-26: la materialización asegura subcuentas y repetirla no duplica ni filas ni subcuentas.
func TestMaterializacionDeEmpresaEsIdempotente(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	abridor := nucleo.NewServicio(pool, nucleo.LectorNivelKYCFijo{}, nil, nil, nil)
	servicio := NuevoServicio(pool, nil, "simulador", abridor)
	empresa := sembrarParteKYC(t, pool, TipoEmpresa)

	for i := 0; i < 2; i++ {
		if _, err := servicio.Estado(t.Context(), empresa); err != nil {
			t.Fatalf("Estado intento %d: %v", i, err)
		}
	}
	filas := contarFilasKYC(t, pool, "SELECT count(*) FROM auth.kyc_estados WHERE parte_id = $1", empresa)
	cuentas := contarCuentasDeNucleo(t, pool, empresa)
	if filas != 1 {
		t.Fatalf("filas de kyc_estados = %d, quiero 1", filas)
	}
	if cuentas != 2 {
		t.Fatalf("subcuentas = %d, quiero 2 sin duplicar", cuentas)
	}
}

// contarCuentasDeNucleo cuenta las subcuentas del ledger con el rol de las tablas del núcleo.
func contarCuentasDeNucleo(t *testing.T, pool *pgxpool.Pool, titularID string) int {
	t.Helper()
	var n int
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+nucleo.RolNucleo); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if err := tx.QueryRow(t.Context(), "SELECT count(*) FROM nucleo.cuenta WHERE titular_id = $1", titularID).Scan(&n); err != nil {
		t.Fatalf("contar subcuentas: %v", err)
	}
	return n
}

// La evidencia es append-only: UPDATE y DELETE como rol_auth fallan por permiso y por el trigger.
func TestEvidenciaKYCInmutable(t *testing.T) {
	pool := abrirPoolDeKYC(t)
	servicio := NuevoServicio(pool, nil, "simulador", nil)
	persona := sembrarParteKYC(t, pool, TipoPersona)
	if err := servicio.Verificar(t.Context(), persona, NivelBajoMonto, ""); err != nil {
		t.Fatalf("Verificar(bajo_monto): %v", err)
	}

	for nombre, sentencia := range map[string]string{
		"update": "UPDATE auth.kyc_verificaciones SET resultado = 'falso' WHERE parte_id = $1",
		"delete": "DELETE FROM auth.kyc_verificaciones WHERE parte_id = $1",
	} {
		t.Run(nombre, func(t *testing.T) {
			err := ejecutarComoRolAuth(t, pool, sentencia, persona)
			if err == nil {
				t.Fatalf("rol_auth %s no falló: la evidencia quedó mutable", nombre)
			}
			t.Logf("rol_auth %s rechazado: %v", nombre, err)
			errDueno := ejecutarComoDueno(t, pool, sentencia, persona)
			if errDueno == nil {
				t.Fatalf("dueño %s no falló: el trigger no aborta", nombre)
			}
			t.Logf("dueño %s rechazado: %v", nombre, errDueno)
		})
	}
}

// proveedorQueFalla revienta si se lo llama: el bajo monto no debe consultar al proveedor.
type proveedorQueFalla struct{ t *testing.T }

// Verificar falla la prueba si el servicio lo invoca.
func (p proveedorQueFalla) Verificar(context.Context, Solicitud) (Resultado, error) {
	p.t.Fatal("el bajo monto no debe llamar al proveedor de KYC")
	return Resultado{}, nil
}

// ejecutarComoRolAuth corre la sentencia dentro de una transacción con SET LOCAL ROLE rol_auth.
func ejecutarComoRolAuth(t *testing.T, pool *pgxpool.Pool, sentencia string, args ...any) error {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SET LOCAL ROLE "+auth.RolAuth); err != nil {
		t.Fatalf("set role: %v", err)
	}
	_, err = tx.Exec(t.Context(), sentencia, args...)
	return err
}

// ejecutarComoDueno corre la sentencia con el pool dueño para que el trigger, no el permiso, la aborte.
func ejecutarComoDueno(t *testing.T, pool *pgxpool.Pool, sentencia string, args ...any) error {
	t.Helper()
	dsn := envDeIntegracion("DATABASE_URL")
	if dsn == "" {
		t.Fatalf("DATABASE_URL es obligatoria para probar el trigger como dueño del esquema")
	}
	dueno, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("abrir pool dueño: %v", err)
	}
	defer dueno.Close()
	_, err = dueno.Exec(t.Context(), sentencia, args...)
	return err
}

// proveedorFijo es un doble local del puerto, para no importar kycsim desde el paquete kyc.
type proveedorFijo struct{ resultado string }

// Verificar devuelve el resultado fijado.
func (p proveedorFijo) Verificar(context.Context, Solicitud) (Resultado, error) {
	return Resultado{Estado: p.resultado, Referencia: "sim", Medio: "simulador"}, nil
}
