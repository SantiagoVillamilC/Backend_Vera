//go:build integration

package inteligencia

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SantiagoVillamilC/Backend_Centinela/internal/inteligencia/internal/repositorio"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type lineaDePrueba struct {
	cuenta string
	monto  int64
}

func sembrarCuentaDeUsuario(t *testing.T, pool *pgxpool.Pool, usuario string) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}
	for _, sufijo := range []string{"disponible", "retenido"} {
		if _, err := tx.Exec(ctx,
			"INSERT INTO nucleo.cuenta (id, naturaleza, clase, titular_id) VALUES ($1, 'pasivo', 'usuario', $2) ON CONFLICT (id) DO NOTHING",
			"pasivo:usuario:"+usuario+":"+sufijo, usuario); err != nil {
			t.Fatalf("crear cuenta de %q: %v", usuario, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("confirmar cuentas: %v", err)
	}
}

func sembrarPersona(t *testing.T, pool *pgxpool.Pool, usuario, nombre string) {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_auth"); err != nil {
		t.Fatalf("bajar a rol_auth: %v", err)
	}
	if _, err := tx.Exec(ctx,
		"INSERT INTO auth.partes (id, tipo) VALUES ($1, 'persona') ON CONFLICT (id) DO NOTHING", usuario); err != nil {
		t.Fatalf("crear parte: %v", err)
	}
	documento := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	if _, err := tx.Exec(ctx,
		"INSERT INTO auth.personas (parte_id, tipo_documento, documento, nombre) VALUES ($1, 'CC', $2, $3)",
		usuario, documento, nombre); err != nil {
		t.Fatalf("crear persona: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("confirmar persona: %v", err)
	}
}

func sembrarAsiento(t *testing.T, pool *pgxpool.Pool, tipo string, transferencia *uuid.UUID,
	descripcion string, ocurrido time.Time, lineas []lineaDePrueba) int64 {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}
	var transferenciaParam any
	if transferencia != nil {
		transferenciaParam = *transferencia
	}
	var id int64
	if err := tx.QueryRow(ctx,
		"INSERT INTO nucleo.journal_entry (tipo, transferencia_id, ocurrido_en, descripcion) VALUES ($1, $2, $3, $4) RETURNING id",
		tipo, transferenciaParam, ocurrido,
		pgtype.Text{String: descripcion, Valid: descripcion != ""}).Scan(&id); err != nil {
		t.Fatalf("crear asiento %q: %v", tipo, err)
	}
	for _, linea := range lineas {
		if _, err := tx.Exec(ctx,
			"INSERT INTO nucleo.posting (entry_id, cuenta_id, monto, moneda) VALUES ($1, $2, $3, 'COP')",
			id, linea.cuenta, linea.monto); err != nil {
			t.Fatalf("crear línea del asiento %d: %v", id, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("confirmar asiento: %v", err)
	}
	return id
}

// saldoEnCentavos lee el saldo con el signo del usuario, igual que nucleo.Saldo: cero menos la suma.
func saldoEnCentavos(t *testing.T, pool *pgxpool.Pool, cuenta string) int64 {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("abrir transacción: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE rol_nucleo"); err != nil {
		t.Fatalf("bajar a rol_nucleo: %v", err)
	}
	var saldo int64
	if err := tx.QueryRow(ctx,
		"SELECT COALESCE(-SUM(monto), 0)::bigint FROM nucleo.posting WHERE cuenta_id = $1",
		cuenta).Scan(&saldo); err != nil {
		t.Fatalf("leer el saldo de %q: %v", cuenta, err)
	}
	return saldo
}

type movimientoLeido struct {
	Direccion   string
	Monto       int64
	Contraparte string
	Resuelta    bool
	Categoria   string
	Origen      string
}

func leerMovimiento(t *testing.T, pool *pgxpool.Pool, usuario string, transaccion uuid.UUID) movimientoLeido {
	t.Helper()
	var leido movimientoLeido
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			return struct{}{}, tx.QueryRow(ctx, `
SELECT direccion, monto_centavos, contraparte, contraparte_resuelta, categoria, origen_categoria
  FROM inteligencia.movimiento_enriquecido
 WHERE usuario_id = $1 AND transaccion_id = $2`,
				usuario, pgtype.UUID{Bytes: transaccion, Valid: true}).
				Scan(&leido.Direccion, &leido.Monto, &leido.Contraparte, &leido.Resuelta,
					&leido.Categoria, &leido.Origen)
		})
	if err != nil {
		t.Fatalf("leer movimiento de %q en %s: %v", usuario, transaccion, err)
	}
	return leido
}

func insertarMemoria(t *testing.T, pool *pgxpool.Pool, usuario, tipo, nivel, valor string, categoria Categoria) {
	t.Helper()
	_, err := enTransaccion(t.Context(), poolDeInteligencia{pool: pool},
		func(ctx context.Context, tx transaccionDeInteligencia, _ *repositorio.Queries) (struct{}, error) {
			_, err := tx.Exec(ctx, `
INSERT INTO inteligencia.memoria_correccion (usuario_id, tipo_destino, nivel, valor, categoria)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (usuario_id, tipo_destino, nivel, valor) DO UPDATE SET categoria = EXCLUDED.categoria`,
				usuario, tipo, nivel, valor, string(categoria))
			return struct{}{}, err
		})
	if err != nil {
		t.Fatalf("insertar memoria: %v", err)
	}
}

func TestEnriquecimientoDeUnaTransferenciaUnFondeoYUnGMF(t *testing.T) {
	pool := abrirPoolDePrueba(t)
	ctx := t.Context()
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	u1, u2 := uuid.NewString(), uuid.NewString()
	sembrarCuentaDeUsuario(t, pool, u1)
	sembrarCuentaDeUsuario(t, pool, u2)
	sembrarPersona(t, pool, u2, "Persona Dos")

	idFondeo := sembrarAsiento(t, pool, "deposito", nil, "fondeo", base,
		[]lineaDePrueba{
			{"activo:custodia:banco", 1000000},
			{"pasivo:usuario:" + u1 + ":disponible", -1000000},
		})

	transferencia := uuid.New()
	sembrarAsiento(t, pool, "retencion", &transferencia, "", base.Add(time.Minute),
		[]lineaDePrueba{
			{"pasivo:usuario:" + u1 + ":disponible", 500000},
			{"pasivo:usuario:" + u1 + ":retenido", -500000},
		})
	sembrarAsiento(t, pool, "despacho_riel", &transferencia, "", base.Add(2*time.Minute),
		[]lineaDePrueba{
			{"pasivo:usuario:" + u1 + ":retenido", 500000},
			{"pasivo:liquidacion_pendiente:riel", -500000},
		})
	sembrarAsiento(t, pool, "liquidacion_confirmada", &transferencia, "", base.Add(3*time.Minute),
		[]lineaDePrueba{
			{"pasivo:liquidacion_pendiente:riel", 500000},
			{"pasivo:usuario:" + u2 + ":disponible", -500000},
		})

	idGMF := sembrarAsiento(t, pool, "retencion", nil, "GMF", base.Add(4*time.Minute),
		[]lineaDePrueba{
			{"pasivo:usuario:" + u1 + ":disponible", 2000},
			{"pasivo:gmf_por_pagar", -2000},
		})

	// El fixture tiene que seguir el convenio del diario: un monto negativo en la subcuenta del
	// usuario es plata que le entró (nucleo.Saldo devuelve -SUM(monto)). Con los signos invertidos
	// esta comprobación falla antes de mirar la copia.
	if saldo := saldoEnCentavos(t, pool, "pasivo:usuario:"+u1+":disponible"); saldo != 498000 {
		t.Fatalf("saldo del disponible de u1 = %d, want 498000", saldo)
	}

	servicio := NuevoServicio(pool)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir: %v", err)
	}

	envio := leerMovimiento(t, pool, u1, transferencia)
	if envio.Direccion != "salida" || envio.Monto != 500000 {
		t.Errorf("envío = %+v", envio)
	}
	if envio.Contraparte != "Persona Dos" || !envio.Resuelta {
		t.Errorf("contraparte del envío = %q, resuelta %v", envio.Contraparte, envio.Resuelta)
	}
	if envio.Categoria != string(SinCategoria) || envio.Origen != "sin_categoria" {
		t.Errorf("categoría del envío = %q, origen %q", envio.Categoria, envio.Origen)
	}

	recibo := leerMovimiento(t, pool, u2, transferencia)
	if recibo.Direccion != "entrada" || recibo.Monto != 500000 {
		t.Errorf("recibo = %+v", recibo)
	}

	fondeo := leerMovimiento(t, pool, u1, transaccionDeGrupo(grupo{clave: fmt.Sprintf("entry:%d", idFondeo)}))
	if fondeo.Direccion != "entrada" || fondeo.Monto != 1000000 || fondeo.Contraparte != textoBancoCustodio {
		t.Errorf("fondeo = %+v", fondeo)
	}

	gmf := leerMovimiento(t, pool, u1, transaccionDeGrupo(grupo{clave: fmt.Sprintf("entry:%d", idGMF)}))
	if gmf.Direccion != "salida" || gmf.Monto != 2000 || gmf.Contraparte != textoGMF {
		t.Errorf("GMF = %+v", gmf)
	}
	if gmf.Categoria != string(CategoriaComisiones) || gmf.Origen != "contraparte_interna" {
		t.Errorf("categoría del GMF = %q, origen %q", gmf.Categoria, gmf.Origen)
	}

	segundo, err := servicio.Enriquecer(ctx, 1000)
	if err != nil {
		t.Fatalf("segundo lote: %v", err)
	}
	if segundo.Asientos != 0 {
		t.Errorf("el segundo lote procesó %d asientos, want 0", segundo.Asientos)
	}

	insertarMemoria(t, pool, u1, "persona", "llave", u2, CategoriaCompras)
	if _, err := servicio.Reconstruir(ctx, 1000); err != nil {
		t.Fatalf("Reconstruir con memoria: %v", err)
	}
	conMemoria := leerMovimiento(t, pool, u1, transferencia)
	if conMemoria.Categoria != string(CategoriaCompras) || conMemoria.Origen != "memoria" {
		t.Errorf("con memoria = %+v", conMemoria)
	}

	antes, err := servicio.Huella(ctx)
	if err != nil {
		t.Fatalf("Huella: %v", err)
	}
	despues, err := servicio.Reconstruir(ctx, 1000)
	if err != nil {
		t.Fatalf("segunda reconstrucción: %v", err)
	}
	if antes != despues {
		t.Errorf("la huella cambió entre reconstrucciones: %s y %s", antes, despues)
	}
}
